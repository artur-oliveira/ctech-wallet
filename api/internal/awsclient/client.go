// Package awsclient bundles the AWS SDK v2 service clients the wallet API uses.
// The wallet only touches DynamoDB; there is no S3/SQS/SNS/Lambda here.
package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/api-commons/awsconfig"
	"gopkg.aoctech.app/wallet/api/internal/config"
)

// Clients holds the initialized AWS service clients. SSM serves the Inter
// credentials (SecureString); DynamoDB is the only data store.
type Clients struct {
	DynamoDB *dynamodb.Client
	SSM      *ssm.Client
}

// New builds the AWS client bundle. A non-empty DynamoDBEndpoint overrides the
// resolved endpoint for local development (DynamoDB-local).
func New(ctx context.Context, cfg *config.Config) (*Clients, error) {
	awsCfg, err := awsconfig.Load(ctx, cfg.AWSRegion)
	if err != nil {
		return nil, err
	}
	return &Clients{
		DynamoDB: awsconfig.NewDynamoDBClient(awsCfg, cfg.DynamoDBEndpoint),
		SSM:      ssm.NewFromConfig(awsCfg),
	}, nil
}
