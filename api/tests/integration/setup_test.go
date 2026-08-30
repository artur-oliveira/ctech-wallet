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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/asaas"
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

func gsiWithSort(name, key, sort string) dtypes.GlobalSecondaryIndex {
	return dtypes.GlobalSecondaryIndex{
		IndexName:  aws.String(name),
		KeySchema:  []dtypes.KeySchemaElement{hashKey(key), rangeKey(sort)},
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
			TableName:              aws.String(table(wallet.TablePixDeposits)),
			AttributeDefinitions:   []dtypes.AttributeDefinition{s("pk"), s("status")},
			KeySchema:              []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{gsi(wallet.GSIStatus, "status")},
			BillingMode:            dtypes.BillingModePayPerRequest,
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
		{
			TableName:              aws.String(table(wallet.TableHolds)),
			AttributeDefinitions:   []dtypes.AttributeDefinition{s("pk"), s("status")},
			KeySchema:              []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{gsi(wallet.GSIHoldStatus, "status")},
			BillingMode:            dtypes.BillingModePayPerRequest,
		},
		{
			TableName: aws.String(table(wallet.TableSandboxPurchases)),
			AttributeDefinitions: []dtypes.AttributeDefinition{
				s("pk"), s("status"), s("webhook_status"), s("user_id"), s("created_at"),
			},
			KeySchema: []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{
				gsi(wallet.GSISandboxPurchaseStatus, "status"),
				gsi(wallet.GSISandboxPurchaseWebhookStatus, "webhook_status"),
				gsiWithSort(wallet.GSIUser, "user_id", "created_at"),
			},
			BillingMode: dtypes.BillingModePayPerRequest,
		},
		{
			TableName:            aws.String(table(wallet.TableProductPurchases)),
			AttributeDefinitions: []dtypes.AttributeDefinition{s("pk"), s("status"), s("webhook_status"), s("user_id"), s("created_at")},
			KeySchema:            []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{
				gsi(wallet.GSIProductPurchaseStatus, "status"),
				gsi(wallet.GSIProductPurchaseWebhookStatus, "webhook_status"),
				gsiWithSort(wallet.GSIUser, "user_id", "created_at"),
			},
			BillingMode: dtypes.BillingModePayPerRequest,
		},
		{
			TableName:            aws.String(table(wallet.TableBaasAccounts)),
			AttributeDefinitions: []dtypes.AttributeDefinition{s("pk"), s("status"), s("provider_account_id")},
			KeySchema:            []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{
				gsi(wallet.GSIBaasStatus, "status"),
				gsi(wallet.GSIBaasAccountID, "provider_account_id"),
			},
			BillingMode: dtypes.BillingModePayPerRequest,
		},
		{
			TableName:              aws.String(table(wallet.TableTransferIntents)),
			AttributeDefinitions:   []dtypes.AttributeDefinition{s("pk"), s("status")},
			KeySchema:              []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{gsi(wallet.GSIIntentStatus, "status")},
			BillingMode:            dtypes.BillingModePayPerRequest,
		},
		{
			TableName:              aws.String(table(wallet.TableMedReceivables)),
			AttributeDefinitions:   []dtypes.AttributeDefinition{s("pk"), s("status")},
			KeySchema:              []dtypes.KeySchemaElement{hashKey("pk")},
			GlobalSecondaryIndexes: []dtypes.GlobalSecondaryIndex{gsi(wallet.GSIMedStatus, "status")},
			BillingMode:            dtypes.BillingModePayPerRequest,
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
		wallet.TableAudit, wallet.TableHolds, wallet.TableSandboxPurchases, wallet.TableProductPurchases,
		wallet.TableBaasAccounts, wallet.TableTransferIntents, wallet.TableMedReceivables,
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
	baas     *services.BaasService
	baasRepo *repositories.BaasRepository
	asaas    *asaas.FakeAsaasClient
}

// custodyMasterKey is the AES-256 key subaccount API keys are encrypted under.
// Zeroed on purpose: an integration harness must never carry a real one.
var custodyMasterKey = make([]byte, 32)

const (
	custodyMasterPixKey = "master-evp-key"
	custodyFeeCents     = 1290
)

func newHarness(kyc *kycclient.KYC) *harness {
	cfg := &config.Config{TablePrefix: tablePrefix}
	repo := repositories.NewWalletRepository(db, cfg)
	userRepo := repositories.NewUserRepository(db, cfg)
	audit := repositories.NewAuditRepository(db, cfg)
	baasRepo := repositories.NewBaasRepository(db, cfg)
	productRepo := repositories.NewProductPurchaseRepository(db, cfg)
	fake := pix.NewFake()
	fakeAsaas := asaas.NewFake()
	locker := lock.NewLocker(cache.NewMemoryBackend(64))
	svc := services.NewWalletService(repo, userRepo, audit, locker, fake, &stubKYC{rec: kyc})
	baas := services.NewBaasService(baasRepo, repo, fakeAsaas, audit, &stubKYC{rec: kyc}, custodyMasterKey, "wallet_parent", "parent-apikey")
	baas.SetCustodyFee(services.CustodyFeeConfig{
		MasterAccountID: "acc_master", MasterPixKey: custodyMasterPixKey, AmountCents: custodyFeeCents,
	}, productRepo)
	baas.SetWithdrawalReverser(svc.ReverseWithdrawal)
	svc.SetBaas(baas)
	svc.SetProductPurchases(productRepo)
	return &harness{
		repo: repo, userRepo: userRepo, audit: audit, svc: svc, pix: fake,
		locker: locker, baas: baas, baasRepo: baasRepo, asaas: fakeAsaas,
	}
}

// stagePaidDeposit makes a pending deposit look received at the provider. The
// confirm path re-queries by payment id and reads the payer's CPF off the
// linked customer, so both have to exist — staging only the deposit row would
// test a path production never takes.
func (h *harness) stagePaidDeposit(dep *wallet.PixDeposit, amount int64, payerCPF string) {
	paymentID := dep.ProviderQRCodeID
	h.asaas.StagePayment(paymentID, dep.ProviderQRCodeID, amount, asaas.PaymentReceived, dep.Txid)
	h.asaas.StagePayer(paymentID, "cus_"+dep.Txid, payerCPF, "Fake Payer")
}

// onboardCustody walks a user through the real onboarding sequence — fee
// charge, fee payment, provider approval — because a deposit has no other way
// to exist now: there is exactly one deposit rail and it needs an approved
// subaccount behind it.
func (h *harness) onboardCustody(t *testing.T, user string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.repo.EnsureRealWallet(ctx, user); err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}
	_, charge, err := h.baas.RequestCustodyAccount(ctx, user, wallet.KYCVerified, 500000)
	if err != nil {
		t.Fatalf("RequestCustodyAccount: %v", err)
	}
	ref := "vfee#" + charge.Txid
	paymentID := "pay_fee_" + user
	h.asaas.StagePayment(paymentID, "", custodyFeeCents, asaas.PaymentReceived, ref)
	if err := h.baas.ConfirmCustodyFee(ctx, paymentID, ref); err != nil {
		t.Fatalf("ConfirmCustodyFee: %v", err)
	}

	acc, err := h.baasRepo.GetBaasAccount(ctx, user)
	if err != nil || acc == nil {
		t.Fatalf("GetBaasAccount: %v (%+v)", err, acc)
	}
	apiKey, err := h.baas.DecryptAPIKey(acc)
	if err != nil {
		t.Fatalf("DecryptAPIKey: %v", err)
	}
	h.asaas.StageAccountStatus(apiKey, &asaas.AccountStatus{General: asaas.RegistrationApproved})

	// The webhook resolves the account through a GSI, and DynamoDB-local
	// populates one asynchronously: an unknown id is an idempotent no-op in
	// production (a webhook for someone else's account), so a stale index reads
	// here as "nothing happened" rather than an error. Retry until the index
	// catches up instead of asserting on a race.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := h.baas.ProcessAccountStatusWebhook(ctx, acc.ProviderAccountID); err != nil {
			t.Fatalf("ProcessAccountStatusWebhook: %v", err)
		}
		got, err := h.baasRepo.GetBaasAccount(ctx, user)
		if err != nil {
			t.Fatalf("GetBaasAccount: %v", err)
		}
		if got.Status == wallet.BaasApproved {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("custody never reached approved; status=%q", got.Status)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
