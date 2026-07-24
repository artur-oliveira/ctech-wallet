import * as cdk from 'aws-cdk-lib';
import {aws_dynamodb} from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import {Construct} from 'constructs';
import {Environment} from './types';
import {
    APPEND_ONLY_TABLES,
    instanceProfileName,
    instanceRoleName,
    S3_PREFIX,
    SERVICE,
    SSM_ACCOUNT,
    SSM_WALLET,
} from './constants';

interface IAMStackProps extends cdk.StackProps {
    environment: Environment;
    deploymentsBucketArn: string;
    logsBucketArn: string;
    dynamoDBTables: Map<string, aws_dynamodb.TableV2>;
    pixGatewayOutboundFunctionArn: string;
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

        const {environment, deploymentsBucketArn, logsBucketArn, dynamoDBTables, pixGatewayOutboundFunctionArn} = props;

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
                    actions: ['dynamodb:UpdateItem', 'dynamodb:DeleteItem'],
                    resources: appendOnlyArns,
                }),
            ],
        }));

        // ── SSM ───────────────────────────────────────────────────────────────────
        // api's role is scoped to exactly the two parameters it still reads —
        // wallet-client-id/secret (for the internal:kyc M2M call to ctech-account) —
        // plus ctech-account's own namespace and the shared /ctech/{env}/* values.
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
                        `arn:aws:ssm:*:*:parameter${accountSsm.namespace}/*`,
                        `arn:aws:ssm:*:*:parameter/ctech/${environment}/*`,
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

        new cdk.CfnOutput(this, 'ApiRoleArn', {value: this.apiRole.roleArn});
    }
}
