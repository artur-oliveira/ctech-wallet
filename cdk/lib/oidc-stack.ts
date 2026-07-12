import * as cdk from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import {Construct} from 'constructs';
import {
  GHA_API_ROLE,
  GHA_FRONTEND_ROLE,
  GHA_INFRA_ROLE,
  GHA_RECONCILE_ROLE,
  OIDC_PROVIDER_ARN,
  S3_PREFIX,
  SERVICE,
} from './constants';

interface OidcStackProps extends cdk.StackProps {
  // e.g. "artur-oliveira/ctech-wallet"
  githubRepo: string;
  deploymentsBucket: string;
}

/**
 * One-time global stack (not per-environment).
 * Creates the GitHub Actions deployment roles. Auth is pure OIDC — there are no
 * long-lived access keys and no `secrets.*` anywhere in the workflows.
 */
export class OidcStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: OidcStackProps) {
    super(scope, id, props);

    const {githubRepo, deploymentsBucket} = props;

    // GitHub OIDC provider is owned by ctech-cdk (Ctech-Global stack).
    // Import by well-known ARN — do not create it here.
    const provider = iam.OpenIdConnectProvider.fromOpenIdConnectProviderArn(
      this, 'GitHubOidc', OIDC_PROVIDER_ARN,
    );

    const subject = `repo:${githubRepo}:*`;

    const trust = new iam.FederatedPrincipal(
      provider.openIdConnectProviderArn,
      {StringLike: {'token.actions.githubusercontent.com:sub': subject}},
      'sts:AssumeRoleWithWebIdentity',
    );

    const deploymentsPrefixArns = [
      `arn:aws:s3:::${deploymentsBucket}/${S3_PREFIX}`,
      `arn:aws:s3:::${deploymentsBucket}/${S3_PREFIX}/*`,
    ];

    // ── Frontend deploy role ────────────────────────────────────────────────
    const frontendRole = new iam.Role(this, 'FrontendDeployRole', {
      roleName: GHA_FRONTEND_ROLE,
      assumedBy: trust,
    });
    frontendRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject', 's3:DeleteObject', 's3:GetObject', 's3:ListBucket'],
      resources: [
        `arn:aws:s3:::*-${SERVICE}-frontend`,
        `arn:aws:s3:::*-${SERVICE}-frontend/*`,
      ],
    }));
    frontendRole.addToPolicy(new iam.PolicyStatement({
      actions: ['cloudfront:CreateInvalidation'],
      resources: ['*'],
    }));
    // Route manifest for the URL-rewrite CloudFront Function. Published after
    // the S3 sync so the key set matches the objects in the bucket.
    frontendRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'cloudfront-keyvaluestore:DescribeKeyValueStore',
        'cloudfront-keyvaluestore:ListKeys',
        'cloudfront-keyvaluestore:UpdateKeys',
      ],
      resources: [`arn:aws:cloudfront::${this.account}:key-value-store/*`],
    }));
    // Reads the DistributionId output of CtechWallet-{Env}-Frontend.
    frontendRole.addToPolicy(new iam.PolicyStatement({
      actions: ['cloudformation:DescribeStacks'],
      resources: ['*'],
    }));

    // ── API deploy role ─────────────────────────────────────────────────────
    const apiRole = new iam.Role(this, 'ApiDeployRole', {
      roleName: GHA_API_ROLE,
      assumedBy: trust,
    });
    apiRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:ListBucket'],
      resources: [`arn:aws:s3:::${deploymentsBucket}`],
      conditions: {StringLike: {'s3:prefix': `${S3_PREFIX}/*`}},
    }));
    apiRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject', 's3:GetObject'],
      resources: deploymentsPrefixArns,
    }));
    // The workflow reads /ctech/{env}/s3/deployments-bucket before uploading.
    apiRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:GetParameter'],
      resources: ['arn:aws:ssm:*:*:parameter/ctech/*'],
    }));
    // Trigger the rolling deploy on running instances via SSM RunCommand.
    apiRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'ssm:SendCommand',
        'ssm:GetCommandInvocation',
        'ssm:ListCommands',
        'ssm:ListCommandInvocations',
      ],
      resources: ['*'],
    }));
    // Discover the InService instances of the ASG.
    apiRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'autoscaling:DescribeAutoScalingGroups',
        'autoscaling:DescribeInstanceRefreshes',
        'autoscaling:StartInstanceRefresh',
        'ec2:DescribeInstances',
      ],
      resources: ['*'],
    }));

    // ── Reconcile Lambda deploy role ────────────────────────────────────────
    // Kept separate from the API role so a compromised API workflow cannot
    // rewrite the code of the job that guards money in limbo.
    const reconcileRole = new iam.Role(this, 'ReconcileDeployRole', {
      roleName: GHA_RECONCILE_ROLE,
      assumedBy: trust,
    });
    reconcileRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject', 's3:GetObject'],
      resources: deploymentsPrefixArns,
    }));
    reconcileRole.addToPolicy(new iam.PolicyStatement({
      actions: ['lambda:UpdateFunctionCode', 'lambda:GetFunction', 'lambda:GetFunctionConfiguration'],
      resources: [`arn:aws:lambda:*:*:function:*-${SERVICE}-reconcile`],
    }));

    // ── Infra deploy role ───────────────────────────────────────────────────
    const infraRole = new iam.Role(this, 'InfraDeployRole', {
      roleName: GHA_INFRA_ROLE,
      assumedBy: trust,
    });
    // CDK requires broad permissions to manage CloudFormation stacks.
    infraRole.addManagedPolicy(
      iam.ManagedPolicy.fromAwsManagedPolicyName('AdministratorAccess'),
    );

    new cdk.CfnOutput(this, 'FrontendRoleArn', {value: frontendRole.roleArn});
    new cdk.CfnOutput(this, 'ApiRoleArn', {value: apiRole.roleArn});
    new cdk.CfnOutput(this, 'ReconcileRoleArn', {value: reconcileRole.roleArn});
    new cdk.CfnOutput(this, 'InfraRoleArn', {value: infraRole.roleArn});
  }
}
