#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';

import {DynamoDBStack} from '../lib/dynamodb-stack';
import {IAMStack} from '../lib/iam-stack';
import {ApiStack} from '../lib/api-stack';
import {ReconcileStack} from '../lib/reconcile-stack';
import {PixGatewayStack} from '../lib/pix-gateway-stack';
import {OidcStack} from '../lib/oidc-stack';
import {Environment} from '../lib/types';
import {
  API_DOMAIN_PREFIX,
  AWS_ACCOUNT,
  AWS_REGION,
  domainForEnv,
  GITHUB_REPO_DEFAULT,
  PIX_CERT_ARN,
  tablePrefix,
} from '../lib/constants';

const app = new cdk.App();

const ENVIRONMENT = (process.env.ENVIRONMENT || 'dev') as Environment;
const GITHUB_REPO = process.env.GITHUB_REPO || GITHUB_REPO_DEFAULT;

// VPC is managed by ctech-cdk. The ID must be a concrete string (not a token)
// because ec2.Vpc.fromLookup resolves subnet/AZ metadata at synthesis time.
// The CI workflow reads /ctech/{env}/network/vpc-id from SSM and exports it.
const CTECH_VPC_ID = process.env.CTECH_VPC_ID || 'vpc-0adfd86727d17445b';
// Shared S3 buckets owned by ctech-cdk. CI reads these from SSM
// (/ctech/{env}/s3/deployments-bucket and /ctech/{env}/s3/logs-bucket)
// and sets them as env vars before running cdk deploy.
const CTECH_DEPLOYMENTS_BUCKET = process.env.CTECH_DEPLOYMENTS_BUCKET || `${ENVIRONMENT}-ctech-deployments`;
const CTECH_LOGS_BUCKET = process.env.CTECH_LOGS_BUCKET || `${ENVIRONMENT}-ctech-application-logs`;
// Session Manager on the API instances. **On**: CI deploys over SSM RunCommand
// (/opt/app/deploy.sh), which needs the agent running; it also gets a shell
// back onto the box for debugging. Costs ~70 MiB of RSS on a t4g.nano.
const ENABLE_SSM_AGENT = true;

// Inter partner bank. Neither value is a secret (the client secret, webhook secret
// and mTLS PEMs all live in SSM), but they differ per environment:
//   - INTER_BASE_URL: production vs the bank's sandbox host.
//   - INTER_PIX_KEY: the receiving key immediate charges (cob) are created against.
// Supply them per environment via env var or `cdk deploy -c interBaseUrl=...`.
// The default matches api/internal/config/config.go (production Inter) — dev and
// stage MUST override it, or they would create charges against the real bank.
const INTER_BASE_URL =
  process.env.INTER_BASE_URL
  || (app.node.tryGetContext('interBaseUrl') as string | undefined)
  || 'https://cdpj.partners.bancointer.com.br';
const INTER_PIX_KEY =
  process.env.INTER_PIX_KEY
  || (app.node.tryGetContext('interPixKey') as string | undefined)
  || '61555ce6-da51-4a80-9012-0c18576e5111';

const env = {account: AWS_ACCOUNT, region: AWS_REGION};

// Cost allocation tags — applied to every resource in every stack.
// Requires manual activation as a cost allocation tag in the Billing console
// (Billing > Cost Allocation Tags) before it appears as a Cost Explorer group-by key.
cdk.Tags.of(app).add('Project', 'ctech-wallet');
cdk.Tags.of(app).add('Environment', ENVIRONMENT);

const id = (name: string) =>
  `CtechWallet-${ENVIRONMENT.charAt(0).toUpperCase() + ENVIRONMENT.slice(1)}-${name}`;

// =====================
// Global stack (GitHub Actions OIDC roles)
// =====================
new OidcStack(app, 'CtechWallet-Global-OIDC', {
  env,
  githubRepo: GITHUB_REPO,
  deploymentsBucket: CTECH_DEPLOYMENTS_BUCKET,
  description: 'CTech Wallet GitHub Actions deployment roles (global)',
});

// =====================
// Base infrastructure
// =====================
const dynamodbStack = new DynamoDBStack(app, id('DynamoDB'), {
  env,
  environment: ENVIRONMENT,
  tablePrefix: tablePrefix(ENVIRONMENT),
  description: `CTech Wallet DynamoDB - ${ENVIRONMENT}`,
});

// =====================
// pix-gateway (2 Lambdas + mTLS HTTP API custom domain)
// =====================
const pixGatewayStack = new PixGatewayStack(app, id('PixGateway'), {
  env,
  environment: ENVIRONMENT,
  certificateArn: PIX_CERT_ARN,
  interBaseUrl: INTER_BASE_URL,
  interPixKey: INTER_PIX_KEY,
  walletApiUrl: `https://${domainForEnv(ENVIRONMENT, API_DOMAIN_PREFIX)}`,
  description: `CTech Wallet pix-gateway (Inter integration Lambdas) - ${ENVIRONMENT}`,
});

const iamStack = new IAMStack(app, id('IAM'), {
  env,
  environment: ENVIRONMENT,
  deploymentsBucketArn: `arn:aws:s3:::${CTECH_DEPLOYMENTS_BUCKET}`,
  logsBucketArn: `arn:aws:s3:::${CTECH_LOGS_BUCKET}`,
  dynamoDBTables: dynamodbStack.tables,
  pixGatewayOutboundFunctionArn: pixGatewayStack.outboundFunctionArn,
  description: `CTech Wallet IAM Roles - ${ENVIRONMENT}`,
});
iamStack.addStackDependency(dynamodbStack);
iamStack.addStackDependency(pixGatewayStack);

// =====================
// API (EC2 + ASG, shared ALB from ctech-cdk)
// =====================
const apiStack = new ApiStack(app, id('API'), {
  env,
  environment: ENVIRONMENT,
  vpcId: CTECH_VPC_ID,
  instanceProfileName: iamStack.instanceProfileName,
  deploymentsBucketName: CTECH_DEPLOYMENTS_BUCKET,
  logsBucketName: CTECH_LOGS_BUCKET,
  pixGatewayFunctionName: pixGatewayStack.outboundFunctionName,
  enableSsmAgent: ENABLE_SSM_AGENT,
  description: `CTech Wallet API (EC2 + ASG + ALB) - ${ENVIRONMENT}`,
});
// instanceProfileName is a plain string, not a CFN token — CDK cannot infer the
// dependency. Force it so the instance profile exists before the ASG validates
// the launch template.
apiStack.addStackDependency(iamStack);
apiStack.addStackDependency(pixGatewayStack);

// =====================
// Withdrawal reconciliation (Lambda + EventBridge Scheduler)
// =====================
// cmd/reconcile invokes pix-gateway's outbound Lambda (LambdaPixClient) for its
// QueryTransfer calls, same as api's server — it no longer builds InterClient
// directly (the design spec's Solution section: "services/reconcile.go keep
// calling [PixClient] exactly as today. Only the implementation swaps" — the
// swap is not scoped to cmd/server only).
const reconcileStack = new ReconcileStack(app, id('Reconcile'), {
  env,
  environment: ENVIRONMENT,
  dynamoDBTables: dynamodbStack.tables,
  pixGatewayOutboundFunctionArn: pixGatewayStack.outboundFunctionArn,
  pixGatewayOutboundFunctionName: pixGatewayStack.outboundFunctionName,
  description: `CTech Wallet withdrawal reconciliation - ${ENVIRONMENT}`,
});
reconcileStack.addStackDependency(dynamodbStack);
reconcileStack.addStackDependency(pixGatewayStack);
