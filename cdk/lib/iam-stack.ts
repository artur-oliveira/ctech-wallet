import * as cdk from 'aws-cdk-lib';
import {aws_dynamodb} from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import {Construct} from 'constructs';
import {Environment} from './types';
import {
  APPEND_ONLY_TABLES,
  AUDIT_ANONYMIZABLE_ATTRIBUTES,
  instanceProfileName,
  instanceRoleName,
  S3_PREFIX,
  SERVICE,
  SSM_ACCOUNT,
  SSM_SHARED,
  SSM_WALLET,
  TABLE_AUDIT,
  TABLE_LEDGER,
} from './constants';

interface IAMStackProps extends cdk.StackProps {
  environment: Environment;
  deploymentsBucketArn: string;
  logsBucketArn: string;
  dynamoDBTables: Map<string, aws_dynamodb.TableV2>;
  pixGatewayOutboundFunctionArn: string;
  /** The wallet's account-deletion SQS queue (ErasureStack). */
  erasureQueueArn: string;
  /** wallet_erasure_retention: restricted, granted write-only below. */
  retentionTableArn: string;
}

/**
 * EC2 instance role for the wallet API.
 * The reconcile Lambda has its own role (see reconcile-stack.ts) — it is a
 * different principal and a narrower blast radius.
 */
export class IAMStack extends cdk.Stack {
  public readonly apiRole: iam.Role;
  public readonly instanceProfileName: string;

  constructor(scope: Construct, id: string, props: IAMStackProps) {
    super(scope, id, props);

    const {environment, deploymentsBucketArn, logsBucketArn, dynamoDBTables, pixGatewayOutboundFunctionArn, erasureQueueArn, retentionTableArn} = props;

    this.apiRole = new iam.Role(this, 'ApiExecutionRole', {
      roleName: instanceRoleName(environment),
      assumedBy: new iam.ServicePrincipal('ec2.amazonaws.com'),
      managedPolicies: [
        // Required for the GitHub Actions rolling deploy (ssm:SendCommand →
        // AWS-RunShellScript → /opt/app/deploy.sh) and for Session Manager.
        iam.ManagedPolicy.fromAwsManagedPolicyName('AmazonSSMManagedInstanceCore'),
        iam.ManagedPolicy.fromAwsManagedPolicyName('CloudWatchAgentServerPolicy'),
      ],
    });

    this.instanceProfileName = instanceProfileName(environment);
    new iam.CfnInstanceProfile(this, 'ApiInstanceProfile', {
      instanceProfileName: this.instanceProfileName,
      roles: [this.apiRole.roleName],
    });

    // ── DynamoDB ──────────────────────────────────────────────────────────────
    // Tables *and* their indexes (arn/index/*) — gsi_user / gsi_idem / gsi_status
    // are queried on every read path.
    //
    // The append-only tables (the ledger and the audit log) are deliberately
    // granted a NARROWER action set than the others: the role can create and read
    // entries but has no UpdateItem/DeleteItem on them at all. Enforcing that in
    // IAM means a bug — or a compromised instance — cannot rewrite or erase the
    // record, only add to it. The ledger proves what happened to the money
    // (Financial Safety Invariant 2); the audit log proves what the user agreed to.
    //
    // No dynamodb:Scan anywhere: access is GetItem > Query only (see CLAUDE.md).
    const appendOnly: readonly string[] = APPEND_ONLY_TABLES;
    // Legacy tables are kept provisioned but must receive NO access (the API no
    // longer reads them and their rows migrate to the new tables out-of-band).
    const appendOnlyArns = [...dynamoDBTables.entries()]
      .filter(([name]) => appendOnly.includes(name))
      .flatMap(([, it]) => [it.tableArn, `${it.tableArn}/index/*`]);
    const mutableTableArns = [...dynamoDBTables.entries()]
      .filter(([name]) => !appendOnly.includes(name))
      .flatMap(([, it]) => [it.tableArn, `${it.tableArn}/index/*`]);

    const arnsOf = (name: string) => {
      const t = dynamoDBTables.get(name)!;
      return [t.tableArn, `${t.tableArn}/index/*`];
    };

    this.apiRole.addManagedPolicy(new iam.ManagedPolicy(this, 'DynamoPolicy', {
      managedPolicyName: `${environment}-${SERVICE}-dynamodb-policy`,
      statements: [
        // Balances, idempotency guards, deposits, withdrawals, users.
        new iam.PolicyStatement({
          actions: [
            'dynamodb:GetItem',
            'dynamodb:PutItem',
            'dynamodb:UpdateItem',
            'dynamodb:Query',
            'dynamodb:BatchGetItem',
            // Balance mutations are conditional TransactWriteItems.
            'dynamodb:ConditionCheckItem',
            // Health probe: DescribeTable on the wallets table. Resource-scoped,
            // unlike dynamodb:ListTables which would need Resource "*".
            'dynamodb:DescribeTable',
          ],
          resources: mutableTableArns,
        }),
        // Append-only: create + read, never update, never delete.
        new iam.PolicyStatement({
          actions: [
            'dynamodb:GetItem',
            'dynamodb:PutItem',
            'dynamodb:Query',
            'dynamodb:BatchGetItem',
            'dynamodb:ConditionCheckItem',
          ],
          resources: appendOnlyArns,
        }),
        new iam.PolicyStatement({
          effect: iam.Effect.DENY,
          actions: ['dynamodb:DeleteItem'],
          resources: appendOnlyArns,
        }),
        // The ledger is never updated (Invariant #2) — unconditional.
        new iam.PolicyStatement({
          sid: 'LedgerNeverUpdated',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:UpdateItem'],
          resources: arnsOf(TABLE_LEDGER),
        }),
        // wallet_audit: the account-deletion purge may REMOVE ip/user_agent
        // (LGPD anonymization) and nothing else. Any other attribute, or a
        // request without dynamodb:Attributes, stays denied.
        new iam.PolicyStatement({
          sid: 'AuditAnonymizeIpUaOnly',
          actions: ['dynamodb:UpdateItem'],
          resources: [dynamoDBTables.get(TABLE_AUDIT)!.tableArn],
          conditions: {
            'ForAllValues:StringEquals': {'dynamodb:Attributes': AUDIT_ANONYMIZABLE_ATTRIBUTES},
            Null: {'dynamodb:Attributes': 'false'},
          },
        }),
        new iam.PolicyStatement({
          sid: 'AuditNoOtherUpdate',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:UpdateItem'],
          resources: arnsOf(TABLE_AUDIT),
          conditions: {'ForAnyValue:StringNotEquals': {'dynamodb:Attributes': AUDIT_ANONYMIZABLE_ATTRIBUTES}},
        }),
        new iam.PolicyStatement({
          sid: 'AuditNoUnscopedUpdate',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:UpdateItem'],
          resources: arnsOf(TABLE_AUDIT),
          conditions: {Null: {'dynamodb:Attributes': 'true'}},
        }),
      ],
    }));

    // ── Account deletion (LGPD) ───────────────────────────────────────────────
    this.apiRole.addManagedPolicy(new iam.ManagedPolicy(this, 'ErasurePolicy', {
      managedPolicyName: `${environment}-${SERVICE}-erasure-policy`,
      statements: [
        new iam.PolicyStatement({
          sid: 'ErasureQueueConsume',
          actions: ['sqs:ReceiveMessage', 'sqs:DeleteMessage', 'sqs:ChangeMessageVisibility', 'sqs:GetQueueAttributes'],
          resources: [erasureQueueArn],
        }),
        // Retained PII (Lei 9.613, 5 years) is written once at purge and never
        // read back by the application: support reads it with its own role.
        new iam.PolicyStatement({
          sid: 'RetentionWriteOnly',
          actions: ['dynamodb:PutItem'],
          resources: [retentionTableArn],
        }),
        new iam.PolicyStatement({
          sid: 'RetentionNoRead',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:GetItem', 'dynamodb:BatchGetItem', 'dynamodb:Query', 'dynamodb:Scan',
            'dynamodb:UpdateItem', 'dynamodb:DeleteItem', 'dynamodb:BatchWriteItem'],
          resources: [retentionTableArn, `${retentionTableArn}/index/*`],
        }),
      ],
    }));

    // ── SSM ───────────────────────────────────────────────────────────────────
    // api's role is scoped to the exact parameters it reads. Never grant
    // whole account/shared namespaces: an API compromise must not become a
    // cross-service secret compromise.
    // The Inter mTLS keypair, OAuth client secret, and webhook secret moved to
    // pix-gateway's own IAM role (see pix-gateway-stack.ts) — api no longer
    // talks to Inter at all (docs/specs/2026-07-13-pix-gateway-lambda-design.md).
    const walletSsm = SSM_WALLET(environment);
    const accountSsm = SSM_ACCOUNT(environment);
    this.apiRole.addManagedPolicy(new iam.ManagedPolicy(this, 'SsmPolicy', {
      managedPolicyName: `${environment}-${SERVICE}-ssm-policy`,
      statements: [
        new iam.PolicyStatement({
          actions: ['ssm:GetParameter'],
          resources: [
            `arn:aws:ssm:*:*:parameter${walletSsm.walletClientId}`,
            `arn:aws:ssm:*:*:parameter${walletSsm.walletClientSecret}`,
            // Dedicated ack client of the account-deletion saga (not the KYC client).
            `arn:aws:ssm:*:*:parameter${walletSsm.erasureClientId}`,
            `arn:aws:ssm:*:*:parameter${walletSsm.erasureClientSecret}`,
            `arn:aws:ssm:*:*:parameter${accountSsm.internalBaseUrl}`,
            `arn:aws:ssm:*:*:parameter${accountSsm.appUrl}`,
            `arn:aws:ssm:*:*:parameter${accountSsm.internalJwksUrl}`,
            `arn:aws:ssm:*:*:parameter${walletSsm.appUrl}`,
            `arn:aws:ssm:*:*:parameter${SSM_SHARED(environment).valkeyUrl}`,
            // M2M sandbox-purchase client registry (webhook notify-back
            // config) — optional; unset means no M2M client is registered yet.
            `arn:aws:ssm:*:*:parameter${walletSsm.m2mClients}`,
            // Billing, which owns the Inter connection.
            `arn:aws:ssm:*:*:parameter${walletSsm.billingUrl}`,
            `arn:aws:ssm:*:*:parameter${walletSsm.billingClientId}`,
            `arn:aws:ssm:*:*:parameter${walletSsm.billingClientSecret}`,
            `arn:aws:ssm:*:*:parameter${walletSsm.billingEventSecret}`,
          ],
        }),
      ],
    }));

    // ── Lambda ────────────────────────────────────────────────────────────────
    // api invokes pix-gateway's outbound function synchronously for every
    // PixClient call (LambdaPixClient) — this is the only Lambda permission the
    // api role needs; it never invokes the webhook function (that one is only
    // ever triggered by API Gateway).
    this.apiRole.addToPolicy(new iam.PolicyStatement({
      actions: ['lambda:InvokeFunction'],
      resources: [pixGatewayOutboundFunctionArn],
    }));

    // ── S3 ────────────────────────────────────────────────────────────────────
    // Read release artifacts; write rotated logs. Both scoped to the ctech-wallet
    // prefix inside the shared ctech-cdk buckets.
    this.apiRole.addManagedPolicy(new iam.ManagedPolicy(this, 'ApiS3Policy', {
      managedPolicyName: `${environment}-${SERVICE}-api-s3-policy`,
      statements: [
        new iam.PolicyStatement({
          actions: ['s3:GetObject'],
          resources: [`${deploymentsBucketArn}/${S3_PREFIX}/*`],
        }),
        new iam.PolicyStatement({
          actions: ['s3:PutObject'],
          resources: [`${logsBucketArn}/${S3_PREFIX}/*`],
        }),
      ],
    }));

    // ── CloudWatch Logs ───────────────────────────────────────────────────────
    // CloudWatchAgentServerPolicy covers the agent; this scopes the app's own
    // log groups explicitly.
    this.apiRole.addManagedPolicy(new iam.ManagedPolicy(this, 'ApiLogsPolicy', {
      managedPolicyName: `${environment}-${SERVICE}-api-logs-policy`,
      statements: [
        new iam.PolicyStatement({
          actions: [
            'logs:CreateLogStream',
            'logs:PutLogEvents',
            'logs:DescribeLogStreams',
            'logs:DescribeLogGroups',
          ],
          resources: [
            `arn:aws:logs:${this.region}:${this.account}:log-group:/${SERVICE}/${environment}/*`,
          ],
        }),
      ],
    }));

    // ── EC2 ───────────────────────────────────────────────────────────────────
    // update-realip.sh reads the AWS-managed CloudFront origin-facing prefix
    // list. Both actions are read-only and do not support resource-level
    // permissions, so Resource must be *.
    this.apiRole.addToPrincipalPolicy(new iam.PolicyStatement({
      actions: ['ec2:DescribeManagedPrefixLists', 'ec2:GetManagedPrefixListEntries'],
      resources: ['*'],
    }));

    // The shared bootstrap scripts published by ctech-cdk's Ec2ScriptsStack.
    // Both buckets are granted unconditionally: which one userData actually
    // pulls from is an instance-launch-time decision (osFamily), not a
    // deploy-time IAM decision, and granting both costs nothing.
    this.apiRole.addToPrincipalPolicy(new iam.PolicyStatement({
      sid: 'ReadSharedEc2BootstrapScripts',
      actions: ['s3:GetObject'],
      resources: [
        `arn:aws:s3:::${environment}-ctech-ec2-scripts/*`,
        `arn:aws:s3:::${environment}-ctech-ec2-scripts-alpine/*`,
      ],
    }));

    new cdk.CfnOutput(this, 'ApiRoleArn', {value: this.apiRole.roleArn});
  }
}
