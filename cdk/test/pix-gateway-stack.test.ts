import assert from 'node:assert/strict';
import {test} from 'node:test';
import * as cdk from 'aws-cdk-lib';
import {Template} from 'aws-cdk-lib/assertions';
import {PixGatewayStack} from '../lib/pix-gateway-stack';

let cachedTemplate: Template | undefined;

function synth(): Template {
  if (cachedTemplate) return cachedTemplate;
  const app = new cdk.App();
  const stack = new PixGatewayStack(app, 'TestPixGatewayStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
    certificateArn: 'arn:aws:acm:us-east-1:868899309401:certificate/00000000-0000-0000-0000-000000000000',
    interBaseUrl: 'https://cdpj.partners.bancointer.com.br',
    interPixKey: 'test-key',
    walletApiUrl: 'https://wallet.aoctech.app',
  });
  cachedTemplate = Template.fromStack(stack);
  return cachedTemplate;
}

test('both Inter Lambdas receive the shared alerts topic ARN', () => {
  const fns = Object.values(synth().findResources('AWS::Lambda::Function')) as any[];
  const withTopic = fns.filter((f) => f.Properties.Environment?.Variables?.ALERTS_TOPIC_ARN !== undefined);
  assert.equal(withTopic.length, 2);
});

test('both roles may publish to the alerts topic and nothing broader', () => {
  const policies = Object.values(synth().findResources('AWS::IAM::Policy')) as any[];
  const publish = policies
    .flatMap((p) => p.Properties.PolicyDocument.Statement as any[])
    .filter((s) => ([] as string[]).concat(s.Action).includes('sns:Publish'));
  assert.equal(publish.length, 2);
  for (const s of publish) {
    assert.notEqual(s.Resource, '*');
  }
});
