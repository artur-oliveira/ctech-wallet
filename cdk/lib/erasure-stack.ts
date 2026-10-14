import * as cdk from 'aws-cdk-lib';
import * as sns from 'aws-cdk-lib/aws-sns';
import * as subs from 'aws-cdk-lib/aws-sns-subscriptions';
import * as sqs from 'aws-cdk-lib/aws-sqs';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {ERASURE_SERVICE_ID, erasureQueueName, SSM_ACCOUNT_ERASURE_TOPIC_ARN} from './constants';
import {Environment} from './types';

interface ErasureStackProps extends cdk.StackProps {
  environment: Environment;
}

/**
 * The wallet's participant queue in the account-deletion saga
 * (ctech-account docs/specs/2026-10-06-account-deletion-saga-protocol.md §3, §7).
 */
export class ErasureStack extends cdk.Stack {
  public readonly queueArn: string;
  public readonly queueUrl: string;

  constructor(scope: Construct, id: string, props: ErasureStackProps) {
    super(scope, id, props);
    const {environment} = props;

    const dlq = new sqs.Queue(this, 'ErasureDLQ', {
      queueName: `${erasureQueueName(environment)}-dlq`,
      retentionPeriod: cdk.Duration.days(14),
      encryption: sqs.QueueEncryption.SQS_MANAGED,
    });
    const queue = new sqs.Queue(this, 'ErasureQueue', {
      queueName: erasureQueueName(environment),
      // ≥ 2× the slowest purge (the consumer has no visibility heartbeat).
      visibilityTimeout: cdk.Duration.minutes(5),
      retentionPeriod: cdk.Duration.days(14),
      encryption: sqs.QueueEncryption.SQS_MANAGED,
      deadLetterQueue: {queue: dlq, maxReceiveCount: 5},
    });

    const topic = sns.Topic.fromTopicArn(this, 'AccountErasureTopic',
      ssm.StringParameter.valueForStringParameter(this, SSM_ACCOUNT_ERASURE_TOPIC_ARN(environment)));
    // ctech-account sets MessageAttributes.services on every publish; the
    // default filter scope (MessageAttributes) is the one that matches.
    topic.addSubscription(new subs.SqsSubscription(queue, {
      rawMessageDelivery: true,
      filterPolicy: {services: sns.SubscriptionFilter.stringFilter({allowlist: [ERASURE_SERVICE_ID]})},
    }));

    this.queueArn = queue.queueArn;
    this.queueUrl = queue.queueUrl;
  }
}
