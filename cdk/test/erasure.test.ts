import assert from 'node:assert/strict';
import {test} from 'node:test';
import * as cdk from 'aws-cdk-lib';
import {Match, Template} from 'aws-cdk-lib/assertions';
import {ErasureStack} from '../lib/erasure-stack';
import {DynamoDBStack} from '../lib/dynamodb-stack';
import {IAMStack} from '../lib/iam-stack';

const env = {account: '868899309401', region: 'us-east-1'};

test('erasure queue: subscribed to the account topic, filtered to wallet on the message attribute, raw delivery, no alarm', () => {
  const t = Template.fromStack(new ErasureStack(new cdk.App(), 'E', {env, environment: 'prod'}));
  t.hasResourceProperties('AWS::SNS::Subscription', {
    Protocol: 'sqs',
    RawMessageDelivery: true,
    FilterPolicy: {services: ['wallet']},
  });
  for (const s of Object.values(t.findResources('AWS::SNS::Subscription')) as any[]) {
    // Absent = MessageAttributes, where ctech-account publishes `services`.
    assert.notEqual(s.Properties.FilterPolicyScope, 'MessageBody');
  }
  t.hasResourceProperties('AWS::SQS::Queue', {
    QueueName: 'prod-ctech-wallet-erasure',
    VisibilityTimeout: 300,
    RedrivePolicy: {maxReceiveCount: 5},
  });
  t.hasResourceProperties('AWS::SQS::Queue', {QueueName: 'prod-ctech-wallet-erasure-dlq'});
  // CloudWatch alarms cost money and were declined: the DLQ is checked by hand (OPERATIONS.md §7).
  t.resourceCountIs('AWS::CloudWatch::Alarm', 0);
});

test('erasure tables: state with TTL, retention kept out of the generic grants, wallet_users TTL', () => {
  const db = new DynamoDBStack(new cdk.App(), 'D', {env, environment: 'prod', tablePrefix: 'prod'});
  const t = Template.fromStack(db);
  t.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_wallet_erasure_state', TimeToLiveSpecification: {AttributeName: 'ttl', Enabled: true},
  });
  t.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_wallet_erasure_retention',
    KeySchema: [{AttributeName: 'pk', KeyType: 'HASH'}, {AttributeName: 'sk', KeyType: 'RANGE'}],
    TimeToLiveSpecification: {AttributeName: 'ttl', Enabled: true},
  });
  t.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_wallet_users', TimeToLiveSpecification: {AttributeName: 'ttl', Enabled: true},
  });
  assert.ok(db.tables.has('wallet_erasure_state'));
  assert.ok(![...db.tables.keys()].includes('wallet_erasure_retention' as any), 'retention must never get the generic table grants');
});

test('api role: retention write-only, audit updatable only on ip/user_agent, ledger never updated, queue consumable', () => {
  const app = new cdk.App();
  const db = new DynamoDBStack(app, 'D', {env, environment: 'prod', tablePrefix: 'prod'});
  const iamStack = new IAMStack(app, 'I', {
    env, environment: 'prod',
    deploymentsBucketArn: 'arn:aws:s3:::d', logsBucketArn: 'arn:aws:s3:::l',
    dynamoDBTables: db.tables,
    pixGatewayOutboundFunctionArn: 'arn:aws:lambda:us-east-1:868899309401:function:x',
    erasureQueueArn: 'arn:aws:sqs:us-east-1:868899309401:prod-ctech-wallet-erasure',
    retentionTableArn: db.retentionTable.tableArn,
  });
  const statements = Object.values(Template.fromStack(iamStack).findResources('AWS::IAM::ManagedPolicy'))
    .flatMap((p: any) => p.Properties.PolicyDocument.Statement);
  const bySid = (sid: string) => {
    const s = statements.find((x: any) => x.Sid === sid);
    assert.ok(s, `missing statement ${sid}`);
    return s;
  };
  const actions = (s: any): string[] => [].concat(s.Action);

  assert.deepEqual(actions(bySid('RetentionWriteOnly')), ['dynamodb:PutItem']);
  const noRead = bySid('RetentionNoRead');
  assert.equal(noRead.Effect, 'Deny');
  for (const a of ['dynamodb:GetItem', 'dynamodb:Query', 'dynamodb:Scan', 'dynamodb:BatchGetItem']) {
    assert.ok(actions(noRead).includes(a), `retention must deny ${a}`);
  }
  const auditAllow = bySid('AuditAnonymizeIpUaOnly');
  assert.deepEqual(auditAllow.Condition['ForAllValues:StringEquals']['dynamodb:Attributes'], ['pk', 'sk', 'ip', 'user_agent']);
  assert.equal(bySid('AuditNoOtherUpdate').Effect, 'Deny');
  assert.equal(bySid('AuditNoUnscopedUpdate').Effect, 'Deny');
  const ledger = bySid('LedgerNeverUpdated');
  assert.equal(ledger.Effect, 'Deny');
  assert.equal(ledger.Condition, undefined, 'the ledger deny must stay unconditional (Invariant #2)');
  assert.ok(actions(bySid('ErasureQueueConsume')).includes('sqs:ReceiveMessage'));
  const ssmResources = JSON.stringify(statements.filter((s: any) => actions(s).includes('ssm:GetParameter')));
  assert.match(ssmResources, /\/ctech-wallet\/prod\/erasure-client-secret/, 'the API must read the dedicated ack client secret');
});

test('user lookups (R21): deposits, withdrawals and holds carry gsi_user on user_id/created_at', () => {
  const t = Template.fromStack(new DynamoDBStack(new cdk.App(), 'D', {env, environment: 'prod', tablePrefix: 'prod'}));
  for (const name of ['prod_wallet_pix_deposits', 'prod_wallet_withdrawals', 'prod_wallet_holds']) {
    t.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
      TableName: name,
      GlobalSecondaryIndexes: Match.arrayWith([Match.objectLike({
        IndexName: 'gsi_user',
        KeySchema: [{AttributeName: 'user_id', KeyType: 'HASH'}, {AttributeName: 'created_at', KeyType: 'RANGE'}],
      })]),
    });
  }
});
