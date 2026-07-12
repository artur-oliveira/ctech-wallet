import * as cdk from 'aws-cdk-lib';
import {Duration} from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as scheduler from 'aws-cdk-lib/aws-scheduler';
import * as schedulerTargets from 'aws-cdk-lib/aws-scheduler-targets';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {aws_dynamodb} from 'aws-cdk-lib';
import {Construct} from 'constructs';
import path from 'node:path';
import {spawnSync} from 'child_process';
import {Environment} from './types';
import {
  SERVICE,
  SSM_ACCOUNT,
  SSM_WALLET,
  reconcileFunctionName,
  reconcileRoleName,
  tablePrefix,
  TABLE_LEDGER,
} from './constants';

const API_DIR = path.join(__dirname, '../../api');

/** Tables the reconciliation job touches. */
const RECONCILE_TABLES = ['wallets', 'ledger_entries', 'idempotency', 'withdrawals'];

/** How often the job sweeps withdrawals stuck in `processing`. */
const RECONCILE_RATE_MINUTES = 5;

// resolveGo returns the absolute path to the go binary.
// Checks PATH first, then falls back to ~/sdk/go*/bin/go (Google's default SDK dir).
function resolveGo(): string {
  const lookup = spawnSync('bash', ['-c',
    'which go 2>/dev/null || ls "${HOME}/sdk/go"*/bin/go 2>/dev/null | sort -rV | head -1',
  ], {stdio: 'pipe', env: process.env});
  if (lookup.status === 0 && lookup.stdout) {
    const found = lookup.stdout.toString().trim();
    if (found) return found;
  }
  return 'go';
}

// goCode builds a Go Lambda binary from the api module.
// Local bundling (no Docker) is attempted first; Docker is the fallback.
function goCode(cmd: string): lambda.AssetCode {
  return lambda.Code.fromAsset(API_DIR, {
    bundling: {
      local: {
        tryBundle(outputDir: string): boolean {
          const r = spawnSync(
            resolveGo(),
            ['build', '-tags', 'lambda.norpc', '-ldflags', '-s -w', '-o', path.join(outputDir, 'bootstrap'), `./cmd/${cmd}`],
            {
              cwd: API_DIR,
              env: {...process.env, GOOS: 'linux', GOARCH: 'arm64', CGO_ENABLED: '0'},
              stdio: ['ignore', 'pipe', 'pipe'],
            },
          );
          if (r.status !== 0) process.stderr.write(r.stderr ?? Buffer.alloc(0));
          return r.status === 0;
        },
      },
      image: lambda.Runtime.PROVIDED_AL2023.bundlingImage,
      // GOCACHE/GOPATH must be writable; Docker runs as uid 1000:1000 with no HOME.
      environment: {GOCACHE: '/tmp/go-build', GOPATH: '/tmp/go'},
      command: [
        'bash', '-c',
        `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -ldflags '-s -w' -o /asset-output/bootstrap ./cmd/${cmd}`,
      ],
    },
  });
}

interface ReconcileStackProps extends cdk.StackProps {
  environment: Environment;
  dynamoDBTables: Map<string, aws_dynamodb.TableV2>;
  /** Inter partner-bank API base URL. */
  interBaseUrl: string;
}

/**
 * Withdrawal reconciliation job (Financial Safety Invariant 8: "no money left in
 * limbo"). Sweeps withdrawals stuck in `processing` — queries the bank per payout
 * and completes or reverses it, alarming on any failed credit-back.
 *
 * Deliberately NOT in the VPC: the job does not take the Valkey per-wallet lock
 * (cmd/reconcile builds a memory-backed locker), and it only needs DynamoDB, the
 * Inter API and ctech-account — all reachable over the public internet. Keeping it
 * out avoids ENI cold starts and a NAT/egress dependency. If it ever needs Valkey,
 * it must move into the VPC with the API's security group.
 */
export class ReconcileStack extends cdk.Stack {
  public readonly functionName: string;

  constructor(scope: Construct, id: string, props: ReconcileStackProps) {
    super(scope, id, props);

    const {environment, dynamoDBTables, interBaseUrl} = props;

    this.functionName = reconcileFunctionName(environment);

    const role = new iam.Role(this, 'ReconcileRole', {
      roleName: reconcileRoleName(environment),
      assumedBy: new iam.ServicePrincipal('lambda.amazonaws.com'),
      managedPolicies: [
        // CloudWatch Logs (CreateLogGroup/Stream + PutLogEvents).
        iam.ManagedPolicy.fromAwsManagedPolicyName('service-role/AWSLambdaBasicExecutionRole'),
      ],
    });

    // ── DynamoDB: read/write on the four tables the job touches (+ their indexes;
    // it scans gsi_status to find processing withdrawals). Not pix_deposits/users.
    // The ledger is append-only (Financial Safety Invariant 2). The job credits a
    // reversal entry, so it needs PutItem — but never UpdateItem/DeleteItem on the
    // ledger. Denying those in IAM means even a compromised reconcile job cannot
    // rewrite or erase the audit trail of the money it moves.
    const ledgerArn = dynamoDBTables.get(TABLE_LEDGER)!.tableArn;
    const mutableArns = RECONCILE_TABLES.filter(n => n !== TABLE_LEDGER).flatMap(name => {
      const t = dynamoDBTables.get(name);
      if (!t) throw new Error(`reconcile-stack: table ${name} not found in DynamoDBStack`);
      return [t.tableArn, `${t.tableArn}/index/*`];
    });

    role.addToPolicy(new iam.PolicyStatement({
      actions: [
        'dynamodb:GetItem',
        'dynamodb:PutItem',
        'dynamodb:UpdateItem',
        'dynamodb:DeleteItem',
        'dynamodb:Query',
        'dynamodb:BatchGetItem',
        'dynamodb:BatchWriteItem',
        'dynamodb:ConditionCheckItem',
      ],
      resources: mutableArns,
    }));
    role.addToPolicy(new iam.PolicyStatement({
      actions: [
        'dynamodb:GetItem',
        'dynamodb:PutItem',
        'dynamodb:Query',
        'dynamodb:BatchGetItem',
        'dynamodb:ConditionCheckItem',
      ],
      resources: [ledgerArn, `${ledgerArn}/index/*`],
    }));
    role.addToPolicy(new iam.PolicyStatement({
      effect: iam.Effect.DENY,
      actions: ['dynamodb:UpdateItem', 'dynamodb:DeleteItem'],
      resources: [ledgerArn, `${ledgerArn}/index/*`],
    }));

    // ── SSM: Inter credentials + mTLS PEMs (SecureString, AWS-managed
    // alias/aws/ssm key → no explicit kms:Decrypt needed) and the account base URL.
    // Same caveat as iam-stack: a customer-managed KMS key would require an
    // explicit kms:Decrypt statement here.
    role.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:GetParameter'],
      resources: [
        `arn:aws:ssm:*:*:parameter${SSM_WALLET(environment).namespace}/*`,
        `arn:aws:ssm:*:*:parameter${SSM_ACCOUNT(environment).namespace}/*`,
      ],
    }));

    const fn = new lambda.Function(this, 'ReconcileFunction', {
      functionName: this.functionName,
      runtime: lambda.Runtime.PROVIDED_AL2023,
      handler: 'bootstrap',
      code: goCode('reconcile'),
      role,
      architecture: lambda.Architecture.ARM_64,
      timeout: Duration.minutes(5),
      memorySize: 256,
      environment: {
        ENVIRONMENT: environment,
        TABLE_PREFIX: tablePrefix(environment),
        AWS_USE_DUALSTACK_ENDPOINT: 'true',
        INTER_BASE_URL: interBaseUrl,
        // Non-secret values, resolved from SSM at deploy time (they land in the
        // CFN template as plaintext — never do this with a SecureString).
        CTECH_URL: ssm.StringParameter.valueForStringParameter(this, SSM_ACCOUNT(environment).baseUrl),
        INTER_CLIENT_ID: ssm.StringParameter.valueForStringParameter(this, SSM_WALLET(environment).interClientId),
        WALLET_CLIENT_ID: ssm.StringParameter.valueForStringParameter(this, SSM_WALLET(environment).walletClientId),
        // AWS_REGION is a reserved Lambda variable — set by the runtime, never here.
      },
      // NOTE: a Lambda has no /opt/app/start.sh, so INTER_CLIENT_SECRET,
      // INTER_WEBHOOK_SECRET and WALLET_CLIENT_SECRET cannot be exported into the
      // environment the way the EC2 API does — and a SecureString must never be
      // resolved into a CFN template. cmd/reconcile must read them from SSM itself
      // at start-up (extend internal/secrets, which already holds an SSM client and
      // loads the mTLS PEMs). The ssm:GetParameter grant above already covers them,
      // so this needs no IAM change.
    });

    new scheduler.Schedule(this, 'ReconcileSchedule', {
      scheduleName: `${environment}-${SERVICE}-reconcile-schedule`,
      description: 'Resolves withdrawals stuck in processing (complete or reverse)',
      schedule: scheduler.ScheduleExpression.rate(Duration.minutes(RECONCILE_RATE_MINUTES)),
      target: new schedulerTargets.LambdaInvoke(fn),
      // Unlike ctech-dfe's dispatcher, this one is enabled: money in limbo must be
      // resolved without human intervention.
      enabled: true,
    });

    new cdk.CfnOutput(this, 'ReconcileFunctionName', {
      value: this.functionName,
      exportName: `${id}-function-name`,
    });
  }
}
