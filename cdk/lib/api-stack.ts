import * as cdk from 'aws-cdk-lib';
import {Duration} from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as autoscaling from 'aws-cdk-lib/aws-autoscaling';
import {AdditionalHealthCheckType} from 'aws-cdk-lib/aws-autoscaling';
import * as elbv2 from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {Environment} from './types';
import {
  ALB_LISTENER_PRIORITY,
  API_CURRENT_ARTIFACT_KEY,
  APP_PORT,
  HEALTH_CHECK_PATH,
  NGINX_PORT,
  S3_PREFIX,
  SERVICE,
  SSM_ACCOUNT,
  SSM_SHARED,
  SSM_WALLET,
  VALKEY_DB,
  asgName,
  tablePrefix,
} from './constants';

interface ApiStackProps extends cdk.StackProps {
  environment: Environment;
  // Must be a concrete string (not a token): ec2.Vpc.fromLookup resolves
  // subnet/AZ metadata at synthesis time. CI reads /ctech/{env}/network/vpc-id
  // from SSM into CTECH_VPC_ID before running cdk deploy.
  vpcId: string;
  /** ALB host header, e.g. wallet-api-dev.aoctech.app */
  domainName: string;
  /** CloudFront host, e.g. wallet-dev.aoctech.app — used for CORS. */
  appDomainName: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  /** Inter partner-bank API base URL (sandbox vs production differ). */
  interBaseUrl: string;
  /** Receiving PIX key for immediate charges (cob). Not a secret. */
  interPixKey: string;
}

export class ApiStack extends cdk.Stack {
  public readonly asgName: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);

    const {
      environment,
      vpcId,
      domainName,
      appDomainName,
      instanceProfileName,
      deploymentsBucketName,
      logsBucketName,
      interBaseUrl,
      interPixKey,
    } = props;

    const shared = SSM_SHARED(environment);
    const wallet = SSM_WALLET(environment);
    const account = SSM_ACCOUNT(environment);

    // ── Shared infrastructure from ctech-cdk ──────────────────────────────────
    const vpc = ec2.Vpc.fromLookup(this, 'Vpc', {vpcId});

    const albSgId = ssm.StringParameter.valueForStringParameter(this, shared.albSgId);
    const albSg = ec2.SecurityGroup.fromSecurityGroupId(this, 'AlbSg', albSgId);

    const apiSecurityGroup = new ec2.SecurityGroup(this, 'ApiSg', {
      vpc,
      securityGroupName: `${environment}-${SERVICE}-api-sg`,
      description: 'ctech-wallet API instances',
      allowAllOutbound: true,
      allowAllIpv6Outbound: true,
    });
    apiSecurityGroup.addIngressRule(albSg, ec2.Port.tcp(NGINX_PORT), 'ALB to API');

    const httpsListenerArn = ssm.StringParameter.valueForStringParameter(
      this, shared.httpsListenerArn,
    );
    const httpsListener = elbv2.ApplicationListener.fromApplicationListenerAttributes(
      this, 'HttpsListener',
      {listenerArn: httpsListenerArn, securityGroup: albSg},
    );

    const isProd = environment === 'prod';
    this.asgName = asgName(environment);
    const logRetention: logs.RetentionDays = isProd ? logs.RetentionDays.ONE_MONTH : logs.RetentionDays.ONE_WEEK;
    const logGroupApp = `/${SERVICE}/${environment}/app`;
    const logGroupNginx = `/${SERVICE}/${environment}/nginx`;

    // ── CloudWatch Log Groups ─────────────────────────────────────────────────
    const appLogGroup = new logs.LogGroup(this, 'AppLogGroup', {
      logGroupName: logGroupApp,
      retention: logRetention,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
    });

    const nginxLogGroup = new logs.LogGroup(this, 'NginxLogGroup', {
      logGroupName: logGroupNginx,
      retention: logRetention,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
    });

    // ── HTTP status code metric filters (nginx JSON access log) ───────────────
    for (const [name, pattern] of [
      ['HTTP2XX', '{ ($.status >= 200) && ($.status < 300) }'],
      ['HTTP3XX', '{ ($.status >= 300) && ($.status < 400) }'],
      ['HTTP4XX', '{ ($.status >= 400) && ($.status < 500) }'],
      ['HTTP5XX', '{ $.status >= 500 }'],
    ] as [string, string][]) {
      new logs.MetricFilter(this, `${name}Filter`, {
        logGroup: nginxLogGroup,
        metricNamespace: `CtechWallet/${environment}`,
        metricName: name,
        filterPattern: logs.FilterPattern.literal(pattern),
        metricValue: '1',
        defaultValue: 0,
      });
    }

    // ── User Data ─────────────────────────────────────────────────────────────
    const userData = ec2.UserData.forLinux();

    userData.addCommands(
      // ── Packages + directories ───────────────────────────────────────────────
      'dnf install -y nginx amazon-cloudwatch-agent amazon-ssm-agent unzip jq',
      'useradd --system --no-create-home --shell /sbin/nologin webapp',
      'mkdir -p /opt/app/releases /var/log/app /etc/nginx/conf.d',
      'chown -R webapp:webapp /opt/app /var/log/app',

      // ── Swap (256 MB) ────────────────────────────────────────────────────────
      // Prevents OOM on t4g.micro (1 GB RAM) under memory pressure.
      'if [ ! -f /var/swapfile ]; then',
      '  dd if=/dev/zero of=/var/swapfile bs=1M count=256',
      '  chmod 600 /var/swapfile',
      '  mkswap /var/swapfile',
      '  swapon /var/swapfile',
      '  echo "/var/swapfile swap swap defaults 0 0" >> /etc/fstab',
      'fi',

      // ── System-wide dual-stack endpoint (SSM agent, CW agent, CLI) ───────────
      'echo "AWS_USE_DUALSTACK_ENDPOINT=true" >> /etc/environment',

      // ── SSM agent: force IPv6 dual-stack endpoint ────────────────────────────
      // Without this the agent cannot connect: instances have no public IPv4.
      `mkdir -p /etc/amazon/ssm`,
      `cat > /etc/amazon/ssm/amazon-ssm-agent.json << 'SSM'`,
      `{ "Agent": { "UseDualStackEndpoint": true } }`,
      `SSM`,
      'systemctl enable amazon-ssm-agent',
      'systemctl restart amazon-ssm-agent',

      // ── nginx: listens :8080, proxies to app :8000 ───────────────────────────
      // Quoted delimiter prevents bash from expanding nginx $variables.
      // Unlike ctech-dfe the wallet is not multi-tenant (no organization header),
      // so rate limiting is keyed by IP only.
      `cat > /etc/nginx/nginx.conf << 'NGINX'`,
      `user nginx;`,
      `pid /run/nginx.pid;`,
      `worker_processes auto;`,
      `worker_rlimit_nofile 65535;`,
      `error_log /var/log/nginx/error.log warn;`,
      ``,
      `events {`,
      `    worker_connections 8192;`,
      `    use epoll;`,
      `    multi_accept on;`,
      `}`,
      ``,
      `http {`,
      `    include /etc/nginx/mime.types;`,
      `    default_type application/octet-stream;`,
      ``,
      `    # Written by /opt/app/update-realip.sh: set_real_ip_from for the ALB and for`,
      `    # CloudFront's origin-facing ranges, so $remote_addr below is the real viewer`,
      `    # IP and not the proxy's. The glob keeps nginx bootable if the file is absent.`,
      `    include /etc/nginx/conf.d/realip*.conf;`,
      ``,
      `    log_format json_log escape=json '{"remote_addr":"$remote_addr","status":$status,"request":"$request","body_bytes_sent":$body_bytes_sent,"request_time":$request_time,"upstream_response_time":"$upstream_response_time"}';`,
      ``,
      `    include /usr/share/nginx/modules/*.conf;`,
      ``,
      `    sendfile on;`,
      `    tcp_nopush on;`,
      `    tcp_nodelay on;`,
      `    keepalive_timeout 30;`,
      `    keepalive_requests 10000;`,
      `    reset_timedout_connection on;`,
      `    open_file_cache max=1000 inactive=20s;`,
      `    open_file_cache_valid 30s;`,
      `    open_file_cache_min_uses 2;`,
      `    open_file_cache_errors on;`,
      ``,
      `    types_hash_max_size 2048;`,
      `    types_hash_bucket_size 128;`,
      ``,
      `    client_header_timeout 15s;`,
      `    client_body_timeout 30s;`,
      `    send_timeout 30s;`,
      ``,
      `    client_max_body_size 1m;`,
      `    client_body_buffer_size 128k;`,
      `    client_header_buffer_size 1k;`,
      `    large_client_header_buffers 4 8k;`,
      ``,
      `    gzip on;`,
      `    gzip_vary on;`,
      `    gzip_proxied any;`,
      `    gzip_comp_level 5;`,
      `    gzip_min_length 1024;`,
      `    gzip_buffers 16 8k;`,
      `    gzip_http_version 1.1;`,
      `    gzip_types application/json application/problem+json application/javascript text/plain text/css;`,
      ``,
      `    server_tokens off;`,
      `    proxy_hide_header X-Powered-By;`,
      `    add_header X-Content-Type-Options nosniff always;`,
      `    add_header X-Frame-Options DENY always;`,
      `    add_header Referrer-Policy strict-origin-when-cross-origin always;`,
      ``,
      `    # $binary_remote_addr is the viewer's IP, not the ALB's, only because the`,
      `    # realip module rewrote it (see the include above). Without that the whole`,
      `    # req_by_ip zone collapses onto the ALB's private IP and the rate becomes a`,
      `    # shared ceiling for every client at once.`,
      `    limit_req_zone $binary_remote_addr zone=req_by_ip:10m rate=100r/s;`,
      `    limit_conn_zone $binary_remote_addr zone=conn_by_ip:10m;`,
      `    limit_req_status  429;`,
      `    limit_conn_status 429;`,
      ``,
      `    upstream app {`,
      `        server 127.0.0.1:${APP_PORT};`,
      `        keepalive 256;`,
      `        keepalive_requests 10000;`,
      `        keepalive_timeout 60s;`,
      `    }`,
      ``,
      `    server {`,
      `        listen ${NGINX_PORT} default_server reuseport;`,
      `        server_name _;`,
      `        access_log /var/log/nginx/access.log json_log;`,
      `        error_log /var/log/nginx/error.log;`,
      ``,
      `        location = ${HEALTH_CHECK_PATH} {`,
      `            proxy_pass http://app;`,
      `            proxy_http_version 1.1;`,
      `            proxy_set_header Connection "";`,
      `            proxy_set_header Host $host;`,
      `            proxy_set_header X-Real-IP $remote_addr;`,
      // Overwrite rather than append: $proxy_add_x_forwarded_for would carry through
      // whatever X-Forwarded-For the client sent, and the Go app trusts the leftmost
      // entry. $remote_addr is the realip-resolved viewer IP, which a client cannot forge.
      `            proxy_set_header X-Forwarded-For $remote_addr;`,
      `            proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;`,
      `            proxy_connect_timeout 5s;`,
      `            proxy_read_timeout 5s;`,
      `            access_log off;`,
      `        }`,
      ``,
      `        location / {`,
      `            limit_req zone=req_by_ip burst=200 nodelay;`,
      `            limit_conn conn_by_ip 100;`,
      ``,
      `            proxy_pass http://app;`,
      `            proxy_http_version 1.1;`,
      `            proxy_set_header Connection "";`,
      `            proxy_set_header Host $host;`,
      `            proxy_set_header X-Real-IP $remote_addr;`,
      // Overwrite rather than append: $proxy_add_x_forwarded_for would carry through
      // whatever X-Forwarded-For the client sent, and the Go app trusts the leftmost
      // entry. $remote_addr is the realip-resolved viewer IP, which a client cannot forge.
      `            proxy_set_header X-Forwarded-For $remote_addr;`,
      `            proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;`,
      `            proxy_connect_timeout 10s;`,
      `            proxy_send_timeout 60s;`,
      `            proxy_read_timeout 60s;`,
      `            proxy_buffering on;`,
      `            proxy_buffer_size 8k;`,
      `            proxy_buffers 16 16k;`,
      `            proxy_busy_buffers_size 32k;`,
      `        }`,
      `    }`,
      `}`,
      `NGINX`,

      // ── realip: trust the ALB and CloudFront, nobody else ─────────────────────
      // Without this, $remote_addr is the ALB's private IP: every client collapses
      // into one rate-limit bucket. Walking X-Forwarded-For right-to-left and
      // discarding only trusted hops is what makes the resolved IP unforgeable —
      // taking the leftmost entry instead would let a client spoof the header.
      //
      // CloudFront's origin-facing ranges change over time, so they are fetched from
      // AWS rather than pinned in the template, and refreshed by a daily timer.
      `cat > /opt/app/update-realip.sh << 'REALIP'`,
      `#!/bin/bash`,
      `set -euo pipefail`,
      `CONF=/etc/nginx/conf.d/realip.conf`,
      `TMP=$(mktemp)`,
      // connect-timeout/max-time: this host has no AAAA record, so an IPv6-only
      // instance without NAT64 can hang here indefinitely on the TCP handshake —
      // a bare --retry never kicks in because the connection attempt itself never
      // fails. Bounding it lets the || fallback in the caller actually run instead
      // of blocking cloud-init's runcmd (and the ASG) forever.
      `RANGES=$(curl -sf --connect-timeout 5 --max-time 15 --retry 3 --retry-delay 2 https://ip-ranges.amazonaws.com/ip-ranges.json)`,
      `PREFIXES=$(echo "$RANGES" | jq -r '(.prefixes[] | select(.service == "CLOUDFRONT_ORIGIN_FACING") | .ip_prefix), (.ipv6_prefixes[] | select(.service == "CLOUDFRONT_ORIGIN_FACING") | .ipv6_prefix)')`,
      // A partial list is worse than the old file: an unlisted edge would be treated
      // as the client and become the rate-limit key. Bail and keep what we have.
      `if [ "$(echo "$PREFIXES" | grep -c .)" -lt 10 ]; then`,
      `  echo "Refusing to write realip.conf: only $(echo "$PREFIXES" | grep -c .) CloudFront prefixes returned" >&2`,
      `  exit 1`,
      `fi`,
      `{`,
      `  echo "# Generated by /opt/app/update-realip.sh — do not edit."`,
      `  echo "set_real_ip_from __VPC_CIDR__;"`,
      `  echo "$PREFIXES" | sed -e 's|^|set_real_ip_from |' -e 's|$|;|'`,
      `  echo "real_ip_header X-Forwarded-For;"`,
      `  echo "real_ip_recursive on;"`,
      `} > "$TMP"`,
      `install -m 644 "$TMP" "$CONF"`,
      `rm -f "$TMP"`,
      // nginx -t reads the live config, so a bad file is caught before it is served.
      `if ! nginx -t 2>/dev/null; then`,
      `  echo "nginx rejected the generated realip.conf — reverting" >&2`,
      `  rm -f "$CONF"`,
      `  exit 1`,
      `fi`,
      // Guarded with `if` rather than `&&`: under `set -e`, a false `&&` chain as the
      // last statement would exit non-zero on the bootstrap run, when nginx is not up yet.
      `if systemctl is-active --quiet nginx; then`,
      `  systemctl reload nginx`,
      `fi`,
      `REALIP`,
      `sed -i 's|__VPC_CIDR__|${vpc.vpcCidrBlock}|g' /opt/app/update-realip.sh`,
      `chmod +x /opt/app/update-realip.sh`,

      `cat > /etc/systemd/system/update-realip.service << 'REALIPSVC'`,
      `[Unit]`,
      `Description=Refresh nginx realip trusted proxy ranges`,
      `After=network-online.target`,
      `Wants=network-online.target`,
      ``,
      `[Service]`,
      `Type=oneshot`,
      `ExecStart=/opt/app/update-realip.sh`,
      `REALIPSVC`,

      `cat > /etc/systemd/system/update-realip.timer << 'REALIPTIMER'`,
      `[Unit]`,
      `Description=Daily refresh of nginx realip trusted proxy ranges`,
      ``,
      `[Timer]`,
      `OnCalendar=daily`,
      `RandomizedDelaySec=1h`,
      `Persistent=true`,
      ``,
      `[Install]`,
      `WantedBy=timers.target`,
      `REALIPTIMER`,

      // Generate the file before nginx first starts, so no request is ever served
      // with the ALB as the rate-limit key.
      `/opt/app/update-realip.sh || echo "realip bootstrap failed — rate limiting will key on the ALB until the timer succeeds"`,
      `systemctl enable nginx`,
      `systemctl start nginx`,
      `systemctl daemon-reload`,
      `systemctl enable --now update-realip.timer`,

      // ── CloudWatch agent ─────────────────────────────────────────────────────
      `mkdir -p /etc/systemd/system/amazon-cloudwatch-agent.service.d`,
      `cat > /etc/systemd/system/amazon-cloudwatch-agent.service.d/override.conf << 'CWAENV'`,
      `[Service]`,
      `Environment=AWS_USE_DUALSTACK_ENDPOINT=true`,
      `CWAENV`,

      // {instance_id} is resolved by the CW agent at runtime, not by bash.
      `cat > /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json << 'CWA'`,
      `{`,
      `  "logs": {`,
      `    "logs_collected": {`,
      `      "files": {`,
      `        "collect_list": [`,
      `          {"file_path":"/var/log/app/app.log","log_group_name":"${logGroupApp}","log_stream_name":"{instance_id}"},`,
      `          {"file_path":"/var/log/nginx/access.log","log_group_name":"${logGroupNginx}","log_stream_name":"{instance_id}/access"},`,
      `          {"file_path":"/var/log/nginx/error.log","log_group_name":"${logGroupNginx}","log_stream_name":"{instance_id}/error"}`,
      `        ]`,
      `      }`,
      `    }`,
      `  }`,
      `}`,
      `CWA`,
      `/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json -s`,

      // ── Static env file (loaded by systemd EnvironmentFile=) ─────────────────
      // CDK tokens are substituted at synthesis time; bash does not expand them.
      // Only non-secret values live here. Secrets come from SSM in start.sh.
      `cat > /etc/app-static.env << 'ENV'`,
      `ENVIRONMENT=${environment}`,
      // repositories.NewBase joins prefix + "_" + table → "${environment}_wallets".
      `TABLE_PREFIX=${tablePrefix(environment)}`,
      `AWS_REGION=${this.region}`,
      `AWS_USE_DUALSTACK_ENDPOINT=true`,
      `PORT=${APP_PORT}`,
      `SERVICE_AUDIENCE=https://${domainName}`,
      `INTER_BASE_URL=${interBaseUrl}`,
      `INTER_PIX_KEY=${interPixKey}`,
      `TRUSTED_PROXIES=127.0.0.1`,
      `CORS_ALLOWED_ORIGINS=https://${appDomainName}`,
      `ENV`,

      // ── start.sh: fetches secrets from SSM then exec-replaces into the binary
      // $ENVIRONMENT comes from systemd EnvironmentFile at runtime.
      //
      // NOT fetched here: the Inter mTLS certificate and private key
      // (/ctech-wallet/{env}/inter/mtls-{cert,key}). The Go app reads and decrypts
      // them from SSM itself at boot (internal/secrets) so the bank certificate can
      // be rotated without a redeploy and the PEMs never travel through shell env.
      `cat > /opt/app/start.sh << 'START'`,
      `#!/bin/bash`,
      // APP_VERSION ships inside the release artifact (release.env), written by CI.
      `if [ -f /opt/app/current/release.env ]; then set -a; . /opt/app/current/release.env; set +a; fi`,
      // Valkey base URL is written by the shared Valkey instance at boot and carries
      // no DB number. Each service appends the DB it owns: /0 and /1 are already
      // taken by ctech-dfe and ctech-account, so the wallet uses /2. Its per-wallet
      // SETNX locks must never share a keyspace with another service.
      `VALKEY_BASE=$(aws ssm get-parameter --name "${shared.valkeyUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      // Falls back to empty → the app uses the in-memory cache backend instead of crashing.
      `if [ -n "$VALKEY_BASE" ]; then VALKEY_URL="\${VALKEY_BASE%/}/${VALKEY_DB}"; else VALKEY_URL=""; fi`,
      `CTECH_URL=$(aws ssm get-parameter --name "${account.baseUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `CTECH_JWKS_URL=$(aws ssm get-parameter --name "${account.jwksUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      // Wallet's own M2M client — used to call ctech-account internal:kyc.
      `WALLET_CLIENT_ID=$(aws ssm get-parameter --name "${wallet.walletClientId}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `WALLET_CLIENT_SECRET=$(aws ssm get-parameter --name "${wallet.walletClientSecret}" --with-decryption --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      // Inter partner bank (short secrets only — see the mTLS note above).
      `INTER_CLIENT_ID=$(aws ssm get-parameter --name "${wallet.interClientId}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `INTER_CLIENT_SECRET=$(aws ssm get-parameter --name "${wallet.interClientSecret}" --with-decryption --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `INTER_WEBHOOK_SECRET=$(aws ssm get-parameter --name "${wallet.interWebhookSecret}" --with-decryption --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `export VALKEY_URL CTECH_URL CTECH_JWKS_URL`,
      `export WALLET_CLIENT_ID WALLET_CLIENT_SECRET`,
      `export INTER_CLIENT_ID INTER_CLIENT_SECRET INTER_WEBHOOK_SECRET`,
      `exec /opt/app/current/app`,
      `START`,
      `chmod +x /opt/app/start.sh`,

      // ── systemd app.service ──────────────────────────────────────────────────
      `cat > /etc/systemd/system/app.service << 'SVC'`,
      `[Unit]`,
      `Description=CTech Wallet API`,
      `After=network.target nginx.service`,
      `StartLimitIntervalSec=300`,
      `StartLimitBurst=5`,
      ``,
      `[Service]`,
      `User=webapp`,
      `Group=webapp`,
      `WorkingDirectory=/opt/app/current`,
      `Environment=HOME=/opt/app`,
      `EnvironmentFile=/etc/app-static.env`,
      `ExecStartPre=/bin/test -x /opt/app/current/app`,
      `ExecStart=/opt/app/start.sh`,
      `StandardOutput=append:/var/log/app/app.log`,
      `StandardError=append:/var/log/app/app.log`,
      `Restart=on-failure`,
      `RestartSec=30`,
      ``,
      `[Install]`,
      `WantedBy=multi-user.target`,
      `SVC`,
      `systemctl daemon-reload`,
      `systemctl enable app`,

      // ── deploy.sh: called by SSM RunCommand from GitHub Actions ──────────────
      // Expects a zip containing a pre-built `app` binary (linux/arm64).
      // __BUCKET__ is replaced by sed so bash $variables are not expanded at write
      // time (quoted 'DEPLOY' delimiter).
      `cat > /opt/app/deploy.sh << 'DEPLOY'`,
      `#!/bin/bash`,
      `set -euo pipefail`,
      `S3_KEY="$1"`,
      `RELEASE_DIR="/opt/app/releases/$(date +%Y%m%d_%H%M%S)"`,
      `mkdir -p "$RELEASE_DIR"`,
      `echo "Downloading release: $S3_KEY"`,
      `aws s3 cp "s3://__BUCKET__/$S3_KEY" /tmp/release.zip`,
      `unzip -o /tmp/release.zip -d "$RELEASE_DIR"`,
      `chmod +x "$RELEASE_DIR/app"`,
      `chown -R webapp:webapp "$RELEASE_DIR"`,
      `ln -sfT "$RELEASE_DIR" /opt/app/current`,
      `systemctl restart app 2>/dev/null || systemctl start app`,
      `for i in {1..60}; do`,
      `  if curl -sf http://127.0.0.1:${NGINX_PORT}${HEALTH_CHECK_PATH} >/dev/null; then`,
      `    echo "Health check passed"`,
      `    break`,
      `  fi`,
      `  if systemctl is-failed --quiet app; then`,
      `    echo "Application failed to start"`,
      `    journalctl -u app --no-pager -n 100 || true`,
      `    exit 1`,
      `  fi`,
      `  sleep 2`,
      `done`,
      `curl -sf http://127.0.0.1:${NGINX_PORT}${HEALTH_CHECK_PATH} >/dev/null || {`,
      `  echo "Timed out waiting for health check"`,
      `  exit 1`,
      `}`,
      `ls -dt /opt/app/releases/*/ 2>/dev/null | tail -n +2 | xargs rm -rf 2>/dev/null || true`,
      `echo "Deployment successful"`,
      `DEPLOY`,
      `sed -i 's|__BUCKET__|${deploymentsBucketName}|g' /opt/app/deploy.sh`,
      `chmod +x /opt/app/deploy.sh`,

      // ── upload-logs.sh: bundles rotated logs and ships to S3 ─────────────────
      // IMDSv2 token required (requireImdsv2 is enforced on this instance).
      `cat > /opt/app/upload-logs.sh << 'UPLOAD'`,
      `#!/bin/bash`,
      `TOKEN=$(curl -sf -X PUT "http://169.254.169.254/latest/api/token" \\`,
      `    -H "X-aws-ec2-metadata-token-ttl-seconds: 60")`,
      `INSTANCE_ID=$(curl -sf -H "X-aws-ec2-metadata-token: $TOKEN" \\`,
      `    "http://169.254.169.254/latest/meta-data/instance-id" || echo "unknown")`,
      `DATE=$(date +%Y%m%d)`,
      `BUCKET="__LOG_BUCKET__"`,
      `ARCHIVE="/tmp/\${DATE}-\${INSTANCE_ID}.tar.gz"`,
      `ROTATED=$(find /var/log/app /var/log/nginx -name "*-\${DATE}.gz" 2>/dev/null)`,
      `[ -z "$ROTATED" ] && exit 0`,
      `tar czf "$ARCHIVE" $ROTATED 2>/dev/null || exit 0`,
      `aws s3 cp "$ARCHIVE" "s3://\${BUCKET}/${S3_PREFIX}/\${DATE}-\${INSTANCE_ID}.tar.gz" --region ${this.region} || exit 0`,
      `find /var/log/app /var/log/nginx -name "*-\${DATE}.gz" -delete`,
      `rm -f "$ARCHIVE"`,
      `UPLOAD`,
      `sed -i 's|__LOG_BUCKET__|${logsBucketName}|g' /opt/app/upload-logs.sh`,
      `chmod +x /opt/app/upload-logs.sh`,

      // ── logrotate: daily, gzip, copytruncate, ship to S3 ─────────────────────
      `cat > /etc/logrotate.d/${SERVICE} << 'LOGROTATE'`,
      `/var/log/app/app.log`,
      `/var/log/nginx/access.log`,
      `/var/log/nginx/error.log {`,
      `    daily`,
      `    compress`,
      `    copytruncate`,
      `    missingok`,
      `    notifempty`,
      `    dateext`,
      `    dateformat -%Y%m%d`,
      `    rotate 1`,
      `    sharedscripts`,
      `    postrotate`,
      `        /opt/app/upload-logs.sh`,
      `    endscript`,
      `}`,
      `LOGROTATE`,

      // ── Bootstrap: deploy current.zip if it already exists in S3 ─────────────
      `aws s3api head-object --bucket "${deploymentsBucketName}" --key "${API_CURRENT_ARTIFACT_KEY}" 2>/dev/null && /opt/app/deploy.sh ${API_CURRENT_ARTIFACT_KEY} || echo "No bootstrap artifact, waiting for first deploy"`,
    );

    // ── Launch Template ───────────────────────────────────────────────────────
    const instanceProfile = iam.InstanceProfile.fromInstanceProfileName(
      this, 'InstanceProfile', instanceProfileName,
    );

    const launchTemplate = new ec2.LaunchTemplate(this, 'LaunchTemplate', {
      launchTemplateName: `${this.asgName}-lt`,
      instanceType: ec2.InstanceType.of(ec2.InstanceClass.T4G, ec2.InstanceSize.MICRO),
      machineImage: ec2.MachineImage.latestAmazonLinux2023({
        cpuType: ec2.AmazonLinuxCpuType.ARM_64,
        edition: ec2.AmazonLinuxEdition.MINIMAL,
      }),
      blockDevices: [{
        deviceName: '/dev/xvda',
        volume: ec2.BlockDeviceVolume.ebs(3, {
          volumeType: ec2.EbsDeviceVolumeType.GP3,
          deleteOnTermination: true,
        }),
      }],
      userData,
      instanceProfile,
      requireImdsv2: true,
      // securityGroup is passed so CDK can resolve IConnectable for
      // attachToApplicationTargetGroup. The generated SecurityGroupIds property is
      // deleted below and moved into NetworkInterfaces, the only place
      // AssociatePublicIpAddress and Ipv6AddressCount can be set.
      securityGroup: apiSecurityGroup,
    });

    const cfnLT = launchTemplate.node.defaultChild as ec2.CfnLaunchTemplate;

    // AWS rejects a launch template that sets both SecurityGroupIds and
    // NetworkInterfaces, so move the SG into NetworkInterfaces to get
    // IPv6-only instances with no public IPv4.
    cfnLT.addPropertyDeletionOverride('LaunchTemplateData.SecurityGroupIds');
    cfnLT.addPropertyOverride('LaunchTemplateData.NetworkInterfaces', [{
      DeviceIndex: 0,
      Groups: [apiSecurityGroup.securityGroupId],
      AssociatePublicIpAddress: false,
      Ipv6AddressCount: 1,
    }]);

    // ── Target Group ──────────────────────────────────────────────────────────
    const targetGroup = new elbv2.ApplicationTargetGroup(this, 'TargetGroup', {
      targetGroupName: `${this.asgName}-tg`,
      vpc,
      port: NGINX_PORT,
      protocol: elbv2.ApplicationProtocol.HTTP,
      targetType: elbv2.TargetType.INSTANCE,
      healthCheck: {
        path: HEALTH_CHECK_PATH,
        interval: cdk.Duration.seconds(15),
        timeout: cdk.Duration.seconds(5),
        healthyThresholdCount: 2,
        unhealthyThresholdCount: 5,
        healthyHttpCodes: '200,207',
      },
      deregistrationDelay: cdk.Duration.seconds(30),
    });

    // ── Auto Scaling Group ────────────────────────────────────────────────────
    const asg = new autoscaling.AutoScalingGroup(this, 'ASG', {
      autoScalingGroupName: this.asgName,
      vpc,
      vpcSubnets: {subnetType: ec2.SubnetType.PUBLIC},
      launchTemplate,
      minCapacity: 1,
      maxCapacity: isProd ? 3 : 1,
      cooldown: cdk.Duration.seconds(120),
      healthChecks: autoscaling.HealthChecks.withAdditionalChecks({
        additionalTypes: [AdditionalHealthCheckType.ELB],
        gracePeriod: Duration.seconds(120),
      }),
    });

    asg.attachToApplicationTargetGroup(targetGroup);

    // ── ALB Listener Rule (shared listener from ctech-cdk) ────────────────────
    // Priority must be unique across services: 10=dfe, 20=accounts, 30=wallet.
    new elbv2.ApplicationListenerRule(this, 'ListenerRule', {
      listener: httpsListener,
      priority: ALB_LISTENER_PRIORITY,
      conditions: [
        elbv2.ListenerCondition.hostHeaders([domainName]),
        elbv2.ListenerCondition.pathPatterns(['/*']),
      ],
      action: elbv2.ListenerAction.forward([targetGroup]),
    });

    // ── Outputs ───────────────────────────────────────────────────────────────
    new cdk.CfnOutput(this, 'AsgName', {value: this.asgName, exportName: `${id}-asg-name`});
    new cdk.CfnOutput(this, 'AppLogGroupName', {
      value: appLogGroup.logGroupName,
      exportName: `${id}-app-log-group`,
    });
    new cdk.CfnOutput(this, 'NginxLogGroupName', {
      value: nginxLogGroup.logGroupName,
      exportName: `${id}-nginx-log-group`,
    });
  }
}
