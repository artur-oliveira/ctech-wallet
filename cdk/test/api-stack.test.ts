import assert from 'node:assert/strict';
import {test} from 'node:test';
import * as cdk from 'aws-cdk-lib';
import {Template} from 'aws-cdk-lib/assertions';
import {ApiStack} from '../lib/api-stack';

/** EC2's hard cap on user data, which a deploy discovers and not a review. */
const USER_DATA_LIMIT_BYTES = 16384;

/** The rendered user data, with unresolved tokens standing in for their values. */
let cachedTemplate: Template | undefined;

function synth(): Template {
  if (cachedTemplate) return cachedTemplate;
  const app = new cdk.App();
  const stack = new ApiStack(app, 'TestApiStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
    vpcId: 'vpc-0adfd86727d17445b',
    instanceProfileName: 'prod-ctech-wallet-instance-profile',
    deploymentsBucketName: 'prod-ctech-deployments',
    logsBucketName: 'prod-ctech-application-logs',
    pixGatewayFunctionName: 'prod-ctech-wallet-pix-gateway-outbound',
    erasureQueueUrl: 'https://sqs.us-east-1.amazonaws.com/868899309401/prod-ctech-wallet-erasure',
  });
  cachedTemplate = Template.fromStack(stack);
  return cachedTemplate;
}

function userDataText(): string {
  const template = synth();
  const launchTemplate = Object.values(
    template.findResources('AWS::EC2::LaunchTemplate'),
  )[0] as any;
  const encoded = launchTemplate.Properties.LaunchTemplateData.UserData['Fn::Base64'];
  if (typeof encoded === 'string') return encoded;
  return (encoded['Fn::Join'][1] as unknown[])
    .map((part) => (typeof part === 'string' ? part : '<<token>>'))
    .join('');
}

test('wallet user data keeps the WebSocket location and the Valkey DB suffix', () => {
  const text = userDataText();
  assert.match(text, /location-ws\.conf/);
  assert.match(text, /proxy_set_header Upgrade \$http_upgrade/);
  assert.match(text, /service-env\.sh/);
  assert.match(text, /VALKEY_BASE%\//, 'the DB number must still be appended');
  assert.doesNotMatch(text, /limit_req_zone/, 'nginx.conf must no longer be inline');
});

test('user data stays well under the EC2 limit', () => {
  // Regression: this service was at the 16 KB ceiling with everything inline.
  assert.ok(Buffer.byteLength(userDataText(), 'utf8') < USER_DATA_LIMIT_BYTES);
});

test('no secret value is written into the launch template', () => {
  const rendered = userDataText();
  assert.match(rendered, /'WALLET_CLIENT_SECRET=\/ctech-wallet\/prod\//);
  assert.doesNotMatch(rendered, /WALLET_CLIENT_SECRET=(?!\/|\$)/);
});

test('the API uses only nano On-Demand capacity', () => {
  synth().hasResourceProperties('AWS::AutoScaling::AutoScalingGroup', {
    CapacityRebalance: false,
  });
  synth().hasResourceProperties('AWS::EC2::LaunchTemplate', {
    LaunchTemplateData: {InstanceType: 't4g.nano'},
  });
  assert.doesNotMatch(JSON.stringify(synth().toJSON()), /MixedInstancesPolicy|t4g.micro/);
});

test('user data wires the erasure queue and the revocation Valkey (DB 0 base URL)', () => {
  const text = userDataText();
  assert.match(text, /ERASURE_QUEUE_URL=https:\/\/sqs\./);
  assert.match(text, /VALKEY_REVOCATION_URL="\$VALKEY_BASE"/);
  assert.match(text, /export VALKEY_URL VALKEY_REVOCATION_URL CORS_ALLOWED_ORIGINS/);
  // The dedicated ack client comes from SSM at service start, never inlined.
  assert.match(text, /'ERASURE_CLIENT_SECRET=\/ctech-wallet\/prod\/erasure-client-secret'/);
  assert.match(text, /'ERASURE_CLIENT_ID=\/ctech-wallet\/prod\/erasure-client-id'/);
});

test('the Billing rail is off in every environment and asks for none of its SSM parameters', () => {
  const text = userDataText();
  assert.match(text, /BILLING_RAIL=off/);
  assert.doesNotMatch(text, /BILLING_URL|BILLING_CLIENT_SECRET|BILLING_EVENT_SECRET|billing-client-secret/,
    'an unseeded SSM path would stop the service from starting');
});
