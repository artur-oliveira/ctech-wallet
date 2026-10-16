#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';

import {DynamoDBStack} from '../lib/dynamodb-stack';
import {ErasureStack} from '../lib/erasure-stack';
import {IAMStack} from '../lib/iam-stack';
import {ApiStack} from '../lib/api-stack';
import {ReconcileStack} from '../lib/reconcile-stack';
import {OidcStack} from '../lib/oidc-stack';
import {Environment} from '../lib/types';
import {
  API_DOMAIN_PREFIX,
  AWS_ACCOUNT,
  AWS_REGION,
  domainForEnv,
  GITHUB_REPO_DEFAULT,
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

// Account-deletion saga participant queue (subscribes to ctech-account's topic).
const erasureStack = new ErasureStack(app, id('Erasure'), {
  env,
  environment: ENVIRONMENT,
  description: `CTech Wallet account-deletion participant queue - ${ENVIRONMENT}`,
});

const iamStack = new IAMStack(app, id('IAM'), {
  env,
  environment: ENVIRONMENT,
  deploymentsBucketArn: `arn:aws:s3:::${CTECH_DEPLOYMENTS_BUCKET}`,
  logsBucketArn: `arn:aws:s3:::${CTECH_LOGS_BUCKET}`,
  dynamoDBTables: dynamodbStack.tables,
  erasureQueueArn: erasureStack.queueArn,
  retentionTableArn: dynamodbStack.retentionTable.tableArn,
  description: `CTech Wallet IAM Roles - ${ENVIRONMENT}`,
});
iamStack.addStackDependency(dynamodbStack);
iamStack.addStackDependency(erasureStack);

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
  erasureQueueUrl: erasureStack.queueUrl,
  enableSsmAgent: ENABLE_SSM_AGENT,
  // Alpine/ARM is the default (same rollout as ctech-account); OS_FAMILY=al2023
  // is the one-line rollback if this ever needs to revert.
  osFamily: (process.env.OS_FAMILY as 'al2023' | 'alpine' | undefined) ?? 'alpine',
  description: `CTech Wallet API (EC2 + ASG + ALB) - ${ENVIRONMENT}`,
});
// instanceProfileName is a plain string, not a CFN token — CDK cannot infer the
// dependency. Force it so the instance profile exists before the ASG validates
// the launch template.
apiStack.addStackDependency(iamStack);
apiStack.addStackDependency(erasureStack);

// =====================
// Withdrawal reconciliation (Lambda + EventBridge Scheduler)
// =====================
// cmd/reconcile asks Billing about processing payouts and unconfirmed deposits.
const reconcileStack = new ReconcileStack(app, id('Reconcile'), {
  env,
  environment: ENVIRONMENT,
  dynamoDBTables: dynamodbStack.tables,
  description: `CTech Wallet withdrawal reconciliation - ${ENVIRONMENT}`,
});
reconcileStack.addStackDependency(dynamodbStack);
