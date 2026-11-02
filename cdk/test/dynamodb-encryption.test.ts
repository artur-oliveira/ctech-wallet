import assert from 'node:assert/strict';
import {test} from 'node:test';
import * as cdk from 'aws-cdk-lib';
import {Template} from 'aws-cdk-lib/assertions';
import {DynamoDBStack} from '../lib/dynamodb-stack';

// KMS policy: tables use the AWS-owned key (free, no KMS requests). The AWS-managed
// key (`aws/dynamodb`) bills every table request as a KMS call.
test('every table uses the AWS-owned key, never a KMS key', () => {
  const t = Template.fromStack(new DynamoDBStack(new cdk.App(), 'D', {
    env: {account: '868899309401', region: 'us-east-1'}, environment: 'prod', tablePrefix: 'prod',
  }));
  const tables = Object.values(t.findResources('AWS::DynamoDB::GlobalTable')) as any[];
  assert.ok(tables.length > 0);
  for (const r of tables) {
    assert.equal(r.Properties.SSESpecification.SSEEnabled, false);
    assert.equal(r.Properties.SSESpecification.SSEType, undefined);
  }
});
