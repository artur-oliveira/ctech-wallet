import * as cdk from 'aws-cdk-lib';
import {Duration} from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import * as acm from 'aws-cdk-lib/aws-certificatemanager';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import {IpAddressType} from 'aws-cdk-lib/aws-apigatewayv2';
import {HttpLambdaIntegration} from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import {Construct} from 'constructs';
import path from 'node:path';
import {Environment} from './types';
import {goLambdaCode} from './go-lambda';
import {
    domainForEnv,
    pixGatewayOutboundFunctionName,
    pixGatewayOutboundRoleName,
    pixGatewayWebhookFunctionName,
    pixGatewayWebhookRoleName,
    SERVICE,
    SSM_ACCOUNT,
    SSM_PIX_GATEWAY,
    SSM_WALLET,
} from './constants';

const PIX_GATEWAY_DIR = path.join(__dirname, '../../pix-gateway');

interface PixGatewayStackProps extends cdk.StackProps {
    environment: Environment;
    /** Reused from the existing *.aoctech.app wildcard cert — same region (us-east-1)
     * as this stack, so no separate ACM cert is needed for the regional custom domain. */
    certificateArn: string;
    interBaseUrl: string;
    interPixKey: string;
    /** api's public base URL — the webhook Lambda's confirm-deposit target. */
    walletApiUrl: string;
}

/**
 * pix-gateway: the only part of the system that talks to Inter directly.
 *
 * Outbound function: invoked synchronously by api's LambdaPixClient for every
 * PixClient call (CreateCharge, QueryCharge, DictLookup, Transfer,
 * QueryTransfer, Refund, Ping). Holds the Inter mTLS keypair + OAuth secret.
 *
 * Webhook function: sits behind an mTLS-verified HTTP API custom domain
 * (pix.wallet.aoctech.app) — API Gateway validates Inter's client certificate
 * against a Trust Store before the request ever reaches Lambda. Holds no Inter
 * credentials at all; it only forwards txids to api's confirm-deposit endpoint
 * using its own M2M client_credentials secret.
 *
 * Neither function is VPC-attached: both only need internet egress (Inter,
 * ctech-account, api's public domain) — same reasoning reconcile-stack.ts
 * documents for staying out of the VPC.
 */
export class PixGatewayStack extends cdk.Stack {
    public readonly outboundFunctionArn: string;
    public readonly outboundFunctionName: string;

    constructor(scope: Construct, id: string, props: PixGatewayStackProps) {
        super(scope, id, props);

        const {environment, certificateArn, interBaseUrl, interPixKey, walletApiUrl} = props;
        const walletSsm = SSM_WALLET(environment);
        const accountSsm = SSM_ACCOUNT(environment);
        const pixGatewaySsm = SSM_PIX_GATEWAY(environment);

        // ── Outbound function ───────────────────────────────────────────────────
        const outboundRole = new iam.Role(this, 'OutboundRole', {
            roleName: pixGatewayOutboundRoleName(environment),
            assumedBy: new iam.ServicePrincipal('lambda.amazonaws.com'),
            managedPolicies: [
                iam.ManagedPolicy.fromAwsManagedPolicyName('service-role/AWSLambdaBasicExecutionRole'),
            ],
        });
        // Inter mTLS keypair + OAuth client secret only — this role does not need
        // pix-gateway/client-secret (that belongs to the webhook function) or any
        // DynamoDB access (pix-gateway never touches the ledger).
        outboundRole.addToPolicy(new iam.PolicyStatement({
            actions: ['ssm:GetParameter'],
            resources: [
                `arn:aws:ssm:*:*:parameter${walletSsm.interMtlsCert}`,
                `arn:aws:ssm:*:*:parameter${walletSsm.interMtlsKey}`,
                `arn:aws:ssm:*:*:parameter${walletSsm.interClientSecret}`,
            ],
        }));

        const outboundFn = new lambda.Function(this, 'OutboundFunction', {
            functionName: pixGatewayOutboundFunctionName(environment),
            runtime: lambda.Runtime.PROVIDED_AL2023,
            handler: 'bootstrap',
            code: goLambdaCode(PIX_GATEWAY_DIR, 'outbound'),
            role: outboundRole,
            architecture: lambda.Architecture.ARM_64,
            timeout: Duration.seconds(20),
            memorySize: 256,
            environment: {
                ENVIRONMENT: environment,
                AWS_USE_DUALSTACK_ENDPOINT: 'true',
                INTER_BASE_URL: interBaseUrl,
                INTER_PIX_KEY: interPixKey,
                INTER_CLIENT_ID: ssm.StringParameter.valueForStringParameter(this, walletSsm.interClientId),
            },
        });
        this.outboundFunctionArn = outboundFn.functionArn;
        this.outboundFunctionName = outboundFn.functionName;

        // ── Webhook function ─────────────────────────────────────────────────────
        const webhookRole = new iam.Role(this, 'WebhookRole', {
            roleName: pixGatewayWebhookRoleName(environment),
            assumedBy: new iam.ServicePrincipal('lambda.amazonaws.com'),
            managedPolicies: [
                iam.ManagedPolicy.fromAwsManagedPolicyName('service-role/AWSLambdaBasicExecutionRole'),
            ],
        });
        // Its own M2M client secret, plus the Inter webhook hmac secret this
        // function checks on every callback — no Inter mTLS credentials (see
        // Task 7's design note: ConfirmDeposit re-queries Inter through the
        // outbound function via api's LambdaPixClient, not directly from this
        // function).
        webhookRole.addToPolicy(new iam.PolicyStatement({
            actions: ['ssm:GetParameter'],
            resources: [
                `arn:aws:ssm:*:*:parameter${pixGatewaySsm.clientSecret}`,
                `arn:aws:ssm:*:*:parameter${walletSsm.interWebhookSecret}`,
            ],
        }));

        const webhookFn = new lambda.Function(this, 'WebhookFunction', {
            functionName: pixGatewayWebhookFunctionName(environment),
            runtime: lambda.Runtime.PROVIDED_AL2023,
            handler: 'bootstrap',
            code: goLambdaCode(PIX_GATEWAY_DIR, 'webhook'),
            role: webhookRole,
            architecture: lambda.Architecture.ARM_64,
            timeout: Duration.seconds(10),
            memorySize: 256,
            environment: {
                ENVIRONMENT: environment,
                AWS_USE_DUALSTACK_ENDPOINT: 'true',
                CTECH_URL: ssm.StringParameter.valueForStringParameter(this, accountSsm.baseUrl),
                PIX_GATEWAY_CLIENT_ID: ssm.StringParameter.valueForStringParameter(this, pixGatewaySsm.clientId),
                WALLET_API_URL: walletApiUrl,
            },
        });

        // ── mTLS Trust Store ─────────────────────────────────────────────────────
        // Holds Inter's webhook CA/certificate — seeded operationally (the .crt
        // downloaded at Inter webhook registration is uploaded here as
        // `inter-webhook-ca.pem`, NOT committed to this repo; see root CLAUDE.md
        // secrets section). Versioned so a certificate rotation can be rolled back.
        const trustStoreBucket = new s3.Bucket(this, 'TrustStoreBucket', {
            bucketName: `${environment}-${SERVICE}-pix-gateway-truststore`,
            versioned: true,
            blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
            removalPolicy: cdk.RemovalPolicy.RETAIN,
        });

        // ── mTLS custom domain ───────────────────────────────────────────────────
        // Regional only — mTLS custom domains cannot be edge-optimized. Reuses the
        // existing *.aoctech.app wildcard cert (same region as this stack).
        const domainName = domainForEnv(environment, 'pix.wallet');
        const domain = new apigwv2.DomainName(this, 'WebhookDomain', {
            domainName,
            ipAddressType: IpAddressType.DUAL_STACK,
            certificate: acm.Certificate.fromCertificateArn(this, 'WebhookDomainCert', certificateArn),
            mtls: {
                bucket: trustStoreBucket,
                key: 'inter-webhook-ca.crt',
            },
        });

        const httpApi = new apigwv2.HttpApi(this, 'WebhookHttpApi', {
            apiName: `${environment}-${SERVICE}-pix-gateway-webhook`,
            defaultDomainMapping: {domainName: domain},
            ipAddressType: IpAddressType.DUAL_STACK,
            // The mTLS client-certificate check happens only at the custom domain
            // (pix.wallet.aoctech.app). HttpApi's auto-generated execute-api.amazonaws.com
            // endpoint is enabled by default and would let anyone POST fake txids
            // straight to the webhook Lambda, bypassing that check entirely. Disabling
            // it makes the custom domain the only way in.
            disableExecuteApiEndpoint: true,
        });
        httpApi.addRoutes({
            path: '/pix/webhook',
            methods: [apigwv2.HttpMethod.POST],
            integration: new HttpLambdaIntegration('WebhookIntegration', webhookFn),
        });

        new cdk.CfnOutput(this, 'OutboundFunctionName', {
            value: this.outboundFunctionName,
            exportName: `${id}-outbound-function-name`,
        });
        new cdk.CfnOutput(this, 'WebhookDomainTarget', {
            // Cloudflare's CNAME for pix.wallet.aoctech.app must point here, DNS-only
            // (unproxied) — a proxied record terminates TLS at Cloudflare and the
            // mTLS handshake never reaches API Gateway (see the design spec).
            value: domain.regionalDomainName,
            exportName: `${id}-webhook-domain-target`,
        });
    }
}
