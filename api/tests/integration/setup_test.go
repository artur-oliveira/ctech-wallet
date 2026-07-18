//go:build integration

// Package integration_test exercises the wallet repository and service against a
// real DynamoDB (DynamoDB-local). Run: make test-integration (needs
// `docker compose -f docker-compose.test.yml up -d`). Skips if DYNAMODB_ENDPOINT
// is unset.
package integration_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/cache"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
	"gopkg.aoctech.app/wallet/api/internal/services"
)

const tablePrefix = "test"

var db *dynamodb.Client

func TestMain(m *testing.M) {
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		// Not an environment with DynamoDB-local — skip the whole package.
		os.Exit(0)
	}
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		panic(err)
	}
	db = dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(endpoint) })

	if err := createTables(context.Background()); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = dropTables(context.Background())
	os.Exit(code)
}

func table(name string) string { return tablePrefix + "_" + name }

func s(name string) dtypes.AttributeDefinition {
	return dtypes.AttributeDefinition{AttributeName: aws.String(name), AttributeType: dtypes.ScalarAttributeTypeS}
}
func hashKey(name string) dtypes.KeySchemaElement {
	return dtypes.KeySchemaElement{AttributeName: aws.String(name), KeyType: dtypes.KeyTypeHash}
}
func rangeKey(name string) dtypes.KeySchemaElement {
	return dtypes.KeySchemaElement{AttributeName: aws.String(name), KeyType: dtypes.KeyTypeRange}
}
func gsi(name, key string) dtypes.GlobalSecondaryIndex {
	return dtypes.GlobalSecondaryIndex{
		IndexName:  aws.String(name),
		KeySchema:  []dtypes.KeySchemaElement{hashKey(key)},
		Projection: &dtypes.Projection{ProjectionType: dtypes.ProjectionTypeAll},
	}
}

func createTables(ctx context.Context) error {
	defs := []*dynamodb.CreateTableInput{
		{
			TableName:              aws.String(table(wallet.TableWallets)),
			AttributeDefinitions:   []dtypes.AttributeDefinition{s("pk"), s("user_id")},
			KeySchema:              []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{gsi(wallet.GSIUser, "user_id")},
			BillingMode:            dtypes.BillingModePayPerRequest,
		},
		{
			TableName:              aws.String(table(wallet.TableLedger)),
			AttributeDefinitions:   []dtypes.AttributeDefinition{s("pk"), s("sk"), s("idempotency_key")},
			KeySchema:              []dtypes.KeySchemaElement{hashKey("pk"), rangeKey("sk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{gsi(wallet.GSIIdem, "idempotency_key")},
			BillingMode:            dtypes.BillingModePayPerRequest,
		},
		{
			TableName:            aws.String(table(wallet.TableIdempotency)),
			AttributeDefinitions: []dtypes.AttributeDefinition{s("pk")},
			KeySchema:            []dtypes.KeySchemaElement{hashKey("pk")},
			BillingMode:          dtypes.BillingModePayPerRequest,
		},
		{
			TableName:            aws.String(table(wallet.TablePixDeposits)),
			AttributeDefinitions: []dtypes.AttributeDefinition{s("pk")},
			KeySchema:            []dtypes.KeySchemaElement{hashKey("pk")},
			BillingMode:          dtypes.BillingModePayPerRequest,
		},
		{
			TableName:              aws.String(table(wallet.TableWithdrawals)),
			AttributeDefinitions:   []dtypes.AttributeDefinition{s("pk"), s("status")},
			KeySchema:              []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{gsi(wallet.GSIStatus, "status")},
			BillingMode:            dtypes.BillingModePayPerRequest,
		},
		{
			TableName:            aws.String(table(wallet.TableUsers)),
			AttributeDefinitions: []dtypes.AttributeDefinition{s("pk")},
			KeySchema:            []dtypes.KeySchemaElement{hashKey("pk")},
			BillingMode:          dtypes.BillingModePayPerRequest,
		},
		{
			TableName:            aws.String(table(wallet.TableAudit)),
			AttributeDefinitions: []dtypes.AttributeDefinition{s("pk"), s("sk")},
			KeySchema:            []dtypes.KeySchemaElement{hashKey("pk"), rangeKey("sk")},
			BillingMode:          dtypes.BillingModePayPerRequest,
		},
	}
	for _, in := range defs {
		if _, err := db.CreateTable(ctx, in); err != nil {
			// Ignore "already exists" so repeated local runs work.
			var exists *dtypes.ResourceInUseException
			if !errors.As(err, &exists) {
				return err
			}
		}
	}
	return nil
}

func dropTables(ctx context.Context) error {
	for _, t := range []string{
		wallet.TableWallets, wallet.TableLedger, wallet.TableIdempotency,
		wallet.TablePixDeposits, wallet.TableWithdrawals, wallet.TableUsers,
		wallet.TableAudit,
	} {
		_, _ = db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table(t))})
	}
	return nil
}

// --- harness ---

type stubKYC struct{ rec *kycclient.KYC }

func (k *stubKYC) Get(_ context.Context, _ string) (*kycclient.KYC, error) { return k.rec, nil }

type harness struct {
	repo     *repositories.WalletRepository
	userRepo *repositories.UserRepository
	audit    *repositories.AuditRepository
	svc      *services.WalletService
	pix      *pix.FakePixClient
	locker   *lock.Locker
}

func newHarness(kyc *kycclient.KYC) *harness {
	cfg := &config.Config{TablePrefix: tablePrefix}
	repo := repositories.NewWalletRepository(db, cfg)
	userRepo := repositories.NewUserRepository(db, cfg)
	audit := repositories.NewAuditRepository(db, cfg)
	fake := pix.NewFake()
	locker := lock.NewLocker(cache.NewMemoryBackend(64))
	svc := services.NewWalletService(repo, userRepo, audit, locker, fake, &stubKYC{rec: kyc})
	return &harness{repo: repo, userRepo: userRepo, audit: audit, svc: svc, pix: fake, locker: locker}
}
