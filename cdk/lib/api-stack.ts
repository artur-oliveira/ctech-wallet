import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {Ec2ScriptRunner, HaproxyEc2Service} from '@aoctech/cdk';
import {Environment} from './types';
import {
  API_CURRENT_ARTIFACT_KEY,
  APP_PORT,
  asgName,
  HEALTH_CHECK_PATH,
  NGINX_PORT,
  S3_PREFIX,
  SERVICE,
  SSM_ACCOUNT,
  SSM_SHARED,
  SSM_WALLET,
  tablePrefix,
  VALKEY_DB,
} from './constants';

interface ApiStackProps extends cdk.StackProps {
  environment: Environment;
  // Must be a concrete string (not a token): ec2.Vpc.fromLookup resolves
  // subnet/AZ metadata at synthesis time. CI reads /ctech/{env}/network/vpc-id
  // from SSM into CTECH_VPC_ID before running cdk deploy.
  vpcId: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  /**
   * pix-gateway's outbound Lambda function name — api invokes it for every
   * PixClient call (LambdaPixClient). api no longer talks to Inter directly;
   * see docs/specs/2026-07-13-pix-gateway-lambda-design.md.
   */
  pixGatewayFunctionName: string;
  // Session Manager. **Off by default**: deploys replace the instances through an
  // ASG instance refresh, so nothing needs SSM RunCommand any more, and the
  // agent costs ~70 MiB of RSS on a t4g.nano. On means a shell back onto the box.
  enableSsmAgent?: boolean;
}

export class ApiStack extends cdk.Stack {
  public readonly asgName: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);

    const {
      environment,
      vpcId,
      instanceProfileName,
      deploymentsBucketName,
      logsBucketName,
      pixGatewayFunctionName,
      enableSsmAgent = false,
    } = props;

    const shared = SSM_SHARED(environment);
    const wallet = SSM_WALLET(environment);
    const account = SSM_ACCOUNT(environment);

    // ── Shared infrastructure from ctech-cdk ──────────────────────────────────
    const vpc = ec2.Vpc.fromLookup(this, 'Vpc', {vpcId});

    const albSgId = ssm.StringParameter.valueForStringParameter(this, shared.albSgId);
    const edgeSg = ec2.SecurityGroup.fromSecurityGroupId(this, 'EdgeSg', albSgId);

    const isProd = environment === 'prod';
    const svcName = `${SERVICE}`
    this.asgName = asgName(environment);
    const logRetention: logs.RetentionDays = isProd ? logs.RetentionDays.ONE_MONTH : logs.RetentionDays.ONE_WEEK;
    const logGroupApp = `/${svcName}/${environment}/app`;
    const logGroupNginx = `/${svcName}/${environment}/nginx`;

    // ── User Data ─────────────────────────────────────────────────────────────
    // ── User Data ─────────────────────────────────────────────────────────────
    // Every shared bootstrap step lives in ctech-cdk's assets/ec2 and is fetched
    // from S3 at boot; the prefix is their content hash, read from SSM at deploy
    // time, so editing a shared script versions this launch template.
    const scripts = new Ec2ScriptRunner(this, 'Scripts', {environment});
    const userData = ec2.UserData.forLinux();
    scripts.install(userData);

    scripts.run(userData, 'setup-base.sh', svcName, 'nginx');
    scripts.run(userData, 'setup-swap.sh', '256');
    scripts.run(userData, 'setup-dualstack.sh');
    scripts.run(userData, 'setup-cloudflare-ca.sh');

    // setup-base.sh installs the SSM agent and setup-dualstack.sh starts it, so
    // this is what stops it again.
    if (!enableSsmAgent) {
      userData.addCommands('systemctl disable --now amazon-ssm-agent 2>/dev/null || true');
    }

    userData.addCommands(
      // Static env file (loaded by systemd EnvironmentFile=). CDK tokens are
      // substituted at synthesis time; bash does not expand them. Only non-secret
      // values live here — secrets are read from SSM at every service start.
      `cat > /etc/app-static.env << 'ENV'`,
      `ENVIRONMENT=${environment}`,
      // repositories.NewBase joins prefix + "_" + table → "${environment}_wallets".
      `TABLE_PREFIX=${tablePrefix(environment)}`,
      `AWS_REGION=${this.region}`,
      `AWS_USE_DUALSTACK_ENDPOINT=true`,
      `PORT=${APP_PORT}`,
      `GAMBLING_ENABLED=true`,
      `PIX_GATEWAY_FUNCTION_NAME=${pixGatewayFunctionName}`,
      `TRUSTED_PROXIES=127.0.0.1`,
      `ENV`,
    );

    // api no longer reads any Inter secret or the mTLS keypair — all Inter
    // contact moved to pix-gateway (docs/specs/2026-07-13-pix-gateway-lambda-design.md).
    scripts.run(userData, 'setup-ssm-env.sh',
      `VALKEY_BASE=${shared.valkeyUrl}`,
      `CTECH_URL=${account.internalBaseUrl}`,
      `CTECH_ISSUER_URL=${account.appUrl}`,
      `CTECH_JWKS_URL=${account.internalJwksUrl}`,
      `SERVICE_AUDIENCE=${wallet.appUrl}`,
      `WALLET_CLIENT_ID=${wallet.walletClientId}`,
      `WALLET_CLIENT_SECRET=${wallet.walletClientSecret}`,
    );

    // The shared Valkey URL carries no DB number; each service appends the one it
    // owns, so per-wallet SETNX locks never share a keyspace. An empty base leaves
    // VALKEY_URL empty and the app falls back to the in-memory backend.
    userData.addCommands(
      `cat > /opt/app/service-env.sh << 'SERVICEENV'`,
      `if [ -n "$VALKEY_BASE" ]; then VALKEY_URL="\${VALKEY_BASE%/}/${VALKEY_DB}"; else VALKEY_URL=""; fi`,
      `CORS_ALLOWED_ORIGINS="$SERVICE_AUDIENCE"`,
      `export VALKEY_URL CORS_ALLOWED_ORIGINS`,
      `SERVICEENV`,
      `chmod 0755 /opt/app/service-env.sh`,
    );

    // The app's WebSocket upgrader rejects a request whose Upgrade/Connection
    // headers were not forwarded ("not using the websocket protocol").
    // $connection_upgrade comes from the map in the shared nginx.conf.
    userData.addCommands(
      `cat > /etc/nginx/conf.d/location-ws.conf << 'WSLOC'`,
      `location = /v1.0/ws {`,
      `    proxy_pass http://app;`,
      `    proxy_http_version 1.1;`,
      `    proxy_set_header Upgrade $http_upgrade;`,
      `    proxy_set_header Connection $connection_upgrade;`,
      `    proxy_set_header Host $host;`,
      `    proxy_set_header X-Real-IP $remote_addr;`,
      `    proxy_set_header X-Forwarded-For $remote_addr;`,
      `    proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;`,
      `    proxy_read_timeout 3600s;`,
      `    proxy_send_timeout 3600s;`,
      `    proxy_buffering off;`,
      `}`,
      `WSLOC`,
    );

    scripts.run(userData, 'setup-realip.sh', vpc.vpcCidrBlock);
    scripts.run(userData, 'setup-nginx.sh', `${NGINX_PORT}`, `${APP_PORT}`, HEALTH_CHECK_PATH, '100', '1m');
    scripts.run(userData, 'setup-app-service.sh', 'CTech Wallet API', 'app',
      'network.target nginx.service');
    scripts.run(userData, 'setup-deploy.sh', deploymentsBucketName, 'app',
      `http://127.0.0.1:${NGINX_PORT}${HEALTH_CHECK_PATH}`);
    scripts.run(userData, 'setup-logs.sh', logsBucketName, S3_PREFIX, SERVICE,
      '/var/log/app', '/var/log/nginx');

    // Logs only. No `metrics` block: EC2 already publishes CPUUtilization and
    // CPUCreditBalance for free, and every custom series this service used to
    // publish was either that again or a number nobody alarmed on.
    // {instance_id} is resolved by the CW agent at runtime, not by bash.
    userData.addCommands(
      `cat > /tmp/cwagent.json << 'CWA'`,
      JSON.stringify({
        agent: {metrics_collection_interval: 60},
        logs: {
          logs_collected: {
            files: {
              collect_list: [
                {file_path: '/var/log/app/app.log', log_group_name: logGroupApp, log_stream_name: '{instance_id}'},
                {file_path: '/var/log/nginx/access.log', log_group_name: logGroupNginx, log_stream_name: '{instance_id}/access'},
                {file_path: '/var/log/nginx/error.log', log_group_name: logGroupNginx, log_stream_name: '{instance_id}/error'},
              ],
            },
          },
        },
      }),
      `CWA`,
    );
    scripts.run(userData, 'setup-cloudwatch-agent.sh', '/tmp/cwagent.json');
    scripts.run(userData, 'bootstrap-deploy.sh', deploymentsBucketName, API_CURRENT_ARTIFACT_KEY);

    // ctech-lbalancer still owns the bootstrap route and private CNAME.
    const service = new HaproxyEc2Service(this, 'ApiService', {
      vpc,
      edgeSecurityGroup: edgeSg,
      appPort: NGINX_PORT,
      userData,
      instanceProfileName,
      securityGroupName: `${environment}-${svcName}-api-sg`,
      securityGroupDescription: 'ctech-wallet API instances',
      appLogGroupName: logGroupApp,
      nginxLogGroupName: logGroupNginx,
      logRetention,
      logRemovalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
      asgName: this.asgName,
      minCapacity: 1,
      maxCapacity: 1,
      // The ASG runs only inside a narrow daytime window: up at 11:55 and down
      // at 13:15 America/Sao_Paulo. Outside it the service is off — inbound
      // webhooks fail and nothing is reachable. Deliberate for a development
      // environment on a single t4g.nano.
      // schedule: {enableCron: '55 11 * * *', disableCron: '15 13 * * *'},
      spot: {},
    });

    // ── Outputs ───────────────────────────────────────────────────────────────
    new cdk.CfnOutput(this, 'AsgName', {value: service.autoScalingGroup.autoScalingGroupName, exportName: `${id}-asg-name`});
    new cdk.CfnOutput(this, 'AppLogGroupName', {
      value: service.appLogGroup.logGroupName,
      exportName: `${id}-app-log-group`,
    });
    new cdk.CfnOutput(this, 'NginxLogGroupName', {
      value: service.nginxLogGroup!.logGroupName,
      exportName: `${id}-nginx-log-group`,
    });
  }
}
