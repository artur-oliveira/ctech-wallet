#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';

import {DynamoDBStack} from '../lib/dynamodb-stack';
import {IAMStack} from '../lib/iam-stack';
import {ApiStack} from '../lib/api-stack';
import {FrontendStack} from '../lib/frontend-stack';
import {ReconcileStack} from '../lib/reconcile-stack';
import {OidcStack} from '../lib/oidc-stack';
import {Environment} from '../lib/types';
import {
    API_DOMAIN_PREFIX,
    APP_DOMAIN_PREFIX,
    AWS_ACCOUNT,
    AWS_REGION,
    CERT_ARN,
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

const iamStack = new IAMStack(app, id('IAM'), {
    env,
    environment: ENVIRONMENT,
    deploymentsBucketArn: `arn:aws:s3:::${CTECH_DEPLOYMENTS_BUCKET}`,
    logsBucketArn: `arn:aws:s3:::${CTECH_LOGS_BUCKET}`,
    dynamoDBTables: dynamodbStack.tables,
    description: `CTech Wallet IAM Roles - ${ENVIRONMENT}`,
});
iamStack.addDependency(dynamodbStack);

// =====================
// API (EC2 + ASG, shared ALB from ctech-cdk)
// =====================
const apiStack = new ApiStack(app, id('API'), {
    env,
    environment: ENVIRONMENT,
    vpcId: CTECH_VPC_ID,
    domainName: domainForEnv(ENVIRONMENT, API_DOMAIN_PREFIX),
    appDomainName: domainForEnv(ENVIRONMENT, APP_DOMAIN_PREFIX),
    instanceProfileName: iamStack.instanceProfileName,
    deploymentsBucketName: CTECH_DEPLOYMENTS_BUCKET,
    logsBucketName: CTECH_LOGS_BUCKET,
    interBaseUrl: INTER_BASE_URL,
    interPixKey: INTER_PIX_KEY,
    description: `CTech Wallet API (EC2 + ASG + ALB) - ${ENVIRONMENT}`,
});
// instanceProfileName is a plain string, not a CFN token — CDK cannot infer the
// dependency. Force it so the instance profile exists before the ASG validates
// the launch template.
apiStack.addDependency(iamStack);

// =====================
// Withdrawal reconciliation (Lambda + EventBridge Scheduler)
// =====================
const reconcileStack = new ReconcileStack(app, id('Reconcile'), {
    env,
    environment: ENVIRONMENT,
    dynamoDBTables: dynamodbStack.tables,
    interBaseUrl: INTER_BASE_URL,
    description: `CTech Wallet withdrawal reconciliation - ${ENVIRONMENT}`,
});
reconcileStack.addDependency(dynamodbStack);

// =====================
// Frontend (S3 + CloudFront)
// =====================
new FrontendStack(app, id('Frontend'), {
    env,
    environment: ENVIRONMENT,
    certificateArn: CERT_ARN,
    domainName: domainForEnv(ENVIRONMENT, APP_DOMAIN_PREFIX),
    apiDomainName: domainForEnv(ENVIRONMENT, API_DOMAIN_PREFIX),
    description: `CTech Wallet Frontend (S3 + CloudFront) - ${ENVIRONMENT}`,
});
