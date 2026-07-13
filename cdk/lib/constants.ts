import {Environment} from './types';

/**
 * Every magic string used by more than one stack lives here (root CLAUDE.md:
 * "Constants — no magic variables"). Names that AWS resources are actually
 * created with must never be inlined at a call site.
 */

// ── Account / region ────────────────────────────────────────────────────────
export const AWS_ACCOUNT = '868899309401';
export const AWS_REGION = 'us-east-1';

// Wildcard *.aoctech.app cert — owned by ctech-cdk, referenced here for CloudFront.
export const CERT_ARN =
  'arn:aws:acm:us-east-1:868899309401:certificate/29678869-bfc3-4688-b81b-55aa5b1d7443';

// GitHub OIDC provider — owned by ctech-cdk (Ctech-Global stack). Imported by ARN.
export const OIDC_PROVIDER_ARN =
  `arn:aws:iam::${AWS_ACCOUNT}:oidc-provider/token.actions.githubusercontent.com`;

export const GITHUB_REPO_DEFAULT = 'artur-oliveira/ctech-wallet';

// ── Naming ──────────────────────────────────────────────────────────────────
export const SERVICE = 'ctech-wallet';
export const BASE_DOMAIN = 'aoctech.app';

/** ALB (api) and CloudFront (ui) host prefixes. */
export const API_DOMAIN_PREFIX = 'wallet-api';
export const APP_DOMAIN_PREFIX = 'wallet';

/**
 * Shared HTTPS listener rule priorities on the ctech-cdk ALB.
 * 10 = ctech-dfe api, 20 = ctech-account api, 30 = ctech-wallet api.
 * Must stay unique across every service that attaches to the shared listener.
 */
export const ALB_LISTENER_PRIORITY = 30;

/** Port the Go binary listens on (nginx proxies :8080 → :8000). */
export const APP_PORT = 8000;
/** Port nginx listens on — the ALB target port. */
export const NGINX_PORT = 8080;
/** Health check path served by the Go API. */
export const HEALTH_CHECK_PATH = '/v1.0/health-check';

/**
 * Request paths CloudFront forwards to the ALB instead of S3. The browser then
 * talks to the API same-origin (wallet.aoctech.app/v1.0/...) and never needs
 * CORS; wallet-api.aoctech.app stays reachable directly for API clients.
 */
export const API_PATH_PATTERNS = ['/v1.0/*'];

/** S3 key prefix inside the shared deployments/logs buckets. */
export const S3_PREFIX = SERVICE;
/** Key of the artifact new ASG instances bootstrap from. */
export const API_CURRENT_ARTIFACT_KEY = `${S3_PREFIX}/api/current.zip`;

// ── Per-environment names ───────────────────────────────────────────────────
export const asgName = (env: Environment) => `${env}-${SERVICE}-api`;
export const instanceRoleName = (env: Environment) => `${env}-${SERVICE}-api-role`;
export const instanceProfileName = (env: Environment) => `${env}-${SERVICE}-api-instance-profile`;
export const frontendBucketName = (env: Environment) => `${env}-${SERVICE}-frontend`;
export const routeStoreName = (env: Environment) => `${env}-${SERVICE}-routes`;
export const reconcileFunctionName = (env: Environment) => `${env}-${SERVICE}-reconcile`;
export const reconcileRoleName = (env: Environment) => `${env}-${SERVICE}-reconcile-role`;
export const pixGatewayOutboundFunctionName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-outbound`;
export const pixGatewayWebhookFunctionName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-webhook`;
export const pixGatewayOutboundRoleName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-outbound-role`;
export const pixGatewayWebhookRoleName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-webhook-role`;

/**
 * DynamoDB table prefix. repositories.NewBase joins prefix and table with "_",
 * so TABLE_PREFIX=dev yields the table `dev_wallets`.
 */
export const tablePrefix = (env: Environment) => env;

// ── GitHub Actions role names (global, not per-env) ─────────────────────────
export const GHA_API_ROLE = `${SERVICE}-gha-api`;
export const GHA_FRONTEND_ROLE = `${SERVICE}-gha-frontend`;
export const GHA_INFRA_ROLE = `${SERVICE}-gha-infra`;
export const GHA_RECONCILE_ROLE = `${SERVICE}-gha-reconcile`;
export const GHA_PIX_GATEWAY_ROLE = `${SERVICE}-gha-pix-gateway`;

// ── SSM parameter paths ─────────────────────────────────────────────────────
/** Shared infra owned by ctech-cdk. */
export const SSM_SHARED = (env: Environment) => ({
  vpcId: `/ctech/${env}/network/vpc-id`,
  albSgId: `/ctech/${env}/network/alb-sg-id`,
  httpsListenerArn: `/ctech/${env}/alb/https-listener-arn`,
  // Base URL with no DB number; consumers append their own (see VALKEY_DB).
  valkeyUrl: `/ctech/${env}/valkey/url`,
  deploymentsBucket: `/ctech/${env}/s3/deployments-bucket`,
  logsBucket: `/ctech/${env}/s3/logs-bucket`,
});

/**
 * Valkey logical DB owned by the wallet.
 * ctech-cdk convention: /0 = ctech-dfe cache, /1 = ws pub/sub (dfe/account),
 * /2+ = other services. The wallet owns /2 — its per-wallet SETNX locks must
 * not collide with another service's keyspace.
 */
export const VALKEY_DB = 2;

/**
 * Wallet-owned SSM namespace (seeded operationally; never written by CDK).
 * The `inter/*` leaves are read only by pix-gateway's own IAM role now — api
 * no longer talks to Inter directly (see
 * docs/specs/2026-07-13-pix-gateway-lambda-design.md), but the paths stay
 * here since they're still under the wallet's shared namespace.
 */
export const SSM_WALLET = (env: Environment) => ({
  namespace: `/${SERVICE}/${env}`,
  walletClientId: `/${SERVICE}/${env}/wallet-client-id`,
  walletClientSecret: `/${SERVICE}/${env}/wallet-client-secret`, // SecureString
  interClientId: `/${SERVICE}/${env}/inter/client-id`,
  interClientSecret: `/${SERVICE}/${env}/inter/client-secret`, // SecureString
  interWebhookSecret: `/${SERVICE}/${env}/inter/webhook-secret`, // SecureString
  // Read by pix-gateway's outbound Lambda at cold start, never exported to env.
  interMtlsCert: `/${SERVICE}/${env}/inter/mtls-cert`, // SecureString
  interMtlsKey: `/${SERVICE}/${env}/inter/mtls-key`, // SecureString
});

/** ctech-account namespace — read-only for the wallet. */
export const SSM_ACCOUNT = (env: Environment) => ({
  namespace: `/ctech-account/${env}`,
  baseUrl: `/ctech-account/${env}/base-url`,
  jwksUrl: `/ctech-account/${env}/jwks-url`,
});

/** pix-gateway-owned SSM namespace (seeded operationally; never written by CDK). */
export const SSM_PIX_GATEWAY = (env: Environment) => ({
  namespace: `/${SERVICE}/${env}/pix-gateway`,
  clientId: `/${SERVICE}/${env}/pix-gateway/client-id`,
  clientSecret: `/${SERVICE}/${env}/pix-gateway/client-secret`, // SecureString
});

// ── Domain helper (identical to ctech-dfe's) ────────────────────────────────
export const domainForEnv = (environment: Environment, prefix: string) => {
  switch (environment) {
    case 'prod':
      return `${prefix}.${BASE_DOMAIN}`;
    case 'dev':
    case 'stage':
      return `${prefix}-${environment}.${BASE_DOMAIN}`;
  }
};

/**
 * The append-only ledger table. Named here because IAM grants it a strictly
 * narrower action set than every other table (no UpdateItem, no DeleteItem) —
 * Financial Safety Invariant 2 enforced at the permission layer.
 */
export const TABLE_LEDGER = 'ledger_entries';

/**
 * The append-only audit table: consent, gambling activation, and every change to
 * a personal gambling limit. The ledger proves what happened to the money; this
 * proves what the user agreed to.
 */
export const TABLE_AUDIT = 'wallet_audit';

/**
 * Tables that are append-only. IAM grants these create+read and explicitly DENIES
 * UpdateItem/DeleteItem, so a bug — or a compromised instance — can add to the
 * record but never rewrite or erase it. That denial is what makes "append-only"
 * a property of the system rather than a promise made by the code.
 */
export const APPEND_ONLY_TABLES = [TABLE_LEDGER, TABLE_AUDIT] as const;
