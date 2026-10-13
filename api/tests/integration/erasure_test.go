//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

func putRaw(t *testing.T, tableName string, v any) {
	t.Helper()
	av, err := attributevalue.MarshalMap(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := db.PutItem(context.Background(), &dynamodb.PutItemInput{TableName: aws.String(table(tableName)), Item: av}); err != nil {
		t.Fatalf("put %s: %v", tableName, err)
	}
}

func getRaw(t *testing.T, tableName, pk, sk string) map[string]dtypes.AttributeValue {
	t.Helper()
	key := map[string]dtypes.AttributeValue{"pk": &dtypes.AttributeValueMemberS{Value: pk}}
	if sk != "" {
		key["sk"] = &dtypes.AttributeValueMemberS{Value: sk}
	}
	out, err := db.GetItem(context.Background(), &dynamodb.GetItemInput{TableName: aws.String(table(tableName)), Key: key, ConsistentRead: aws.Bool(true)})
	if err != nil {
		t.Fatalf("get %s: %v", tableName, err)
	}
	return out.Item
}

type retainedDeposit struct {
	Item wallet.PixDeposit `dynamodbav:"item"`
	TTL  int64             `dynamodbav:"ttl"`
}

func erasureRepo() *repositories.ErasureRepository {
	return repositories.NewErasureRepository(db, &config.Config{TablePrefix: tablePrefix})
}

func TestErasureRepositoryFindsUserRowsAcrossStatusPages(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	user, other := "u-"+id.New(), "u-"+id.New()
	// 120 rows of someone else in the same status partition force a second
	// GSI page (the repository pages at 100).
	for range 120 {
		putRaw(t, wallet.TablePixDeposits, wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: other, Status: wallet.DepositConfirmed, CreatedAt: repositories.NowStr()})
	}
	mine := wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: user, Status: wallet.DepositConfirmed, PayerCPF: cpf, PayerName: "Fulano", AmountExpected: 500, CreatedAt: repositories.NowStr()}
	putRaw(t, wallet.TablePixDeposits, mine)
	putRaw(t, wallet.TableWithdrawals, wallet.Withdrawal{WithdrawalID: "wd-" + id.New(), UserID: user, Status: wallet.WithdrawCompleted, PixKey: cpf, Amount: 300, CreatedAt: repositories.NowStr()})
	putRaw(t, wallet.TableHolds, wallet.Hold{HoldID: "h-" + id.New(), UserID: user, WalletID: "WALLET#g-" + id.New(), Status: wallet.HoldHeld, Amount: 100, CreatedAt: repositories.NowStr()})
	putRaw(t, wallet.TableHolds, wallet.Hold{HoldID: "h-" + id.New(), UserID: user, WalletID: "WALLET#g-" + id.New(), Status: wallet.HoldSettled, Amount: 100, CreatedAt: repositories.NowStr()})

	deps, err := er.DepositsForUser(ctx, user)
	if err != nil || len(deps) != 1 || deps[0].Txid != mine.Txid {
		t.Fatalf("DepositsForUser = %v, %v; want exactly %s", deps, err, mine.Txid)
	}
	wds, err := er.WithdrawalsForUser(ctx, user)
	if err != nil || len(wds) != 1 {
		t.Fatalf("WithdrawalsForUser = %v, %v; want 1", wds, err)
	}
	holds, err := er.OpenHoldsForUser(ctx, user)
	if err != nil || len(holds) != 1 || holds[0].Status != wallet.HoldHeld {
		t.Fatalf("OpenHoldsForUser = %v, %v; want only the held one", holds, err)
	}
}

func TestErasureRetainIsWriteOnceAndStripRemovesPII(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	walletRepo := repositories.NewWalletRepository(db, &config.Config{TablePrefix: tablePrefix})
	user := "u-" + id.New()
	mine := wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: user, Status: wallet.DepositConfirmed, PayerCPF: cpf, PayerName: "Fulano", AmountExpected: 500, CreatedAt: repositories.NowStr()}
	putRaw(t, wallet.TablePixDeposits, mine)
	until := time.Now().AddDate(wallet.ErasureRetentionYears, 0, 0)

	if err := er.Retain(ctx, user, wallet.TablePixDeposits, mine.Txid, mine, until); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	// A rerun after the original was stripped must never overwrite the full copy.
	stripped := mine
	stripped.PayerCPF, stripped.PayerName = "", ""
	if err := er.Retain(ctx, user, wallet.TablePixDeposits, mine.Txid, stripped, until); err != nil {
		t.Fatalf("second Retain must be a no-op, got %v", err)
	}
	var rec retainedDeposit
	item := getRaw(t, wallet.TableErasureRetention, wallet.RetentionSubPrefix+user, repositories.RetentionSK(wallet.TablePixDeposits, mine.Txid))
	if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
		t.Fatalf("unmarshal retention: %v", err)
	}
	if rec.Item.PayerCPF != cpf || rec.Item.PayerName != "Fulano" || rec.TTL != until.Unix() {
		t.Fatalf("retention = %+v, want the full PII copy with ttl %d", rec, until.Unix())
	}

	if err := er.StripDepositPII(ctx, mine.Txid); err != nil {
		t.Fatalf("StripDepositPII: %v", err)
	}
	d, err := walletRepo.GetDeposit(ctx, mine.Txid)
	if err != nil || d.PayerCPF != "" || d.PayerName != "" || d.AmountExpected != 500 || d.Status != wallet.DepositConfirmed {
		t.Fatalf("stripped deposit = %+v, %v; want PII gone, money fields intact", d, err)
	}
}

func TestErasureProfileAndAuditAreAnonymizedIdempotently(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	cfg := &config.Config{TablePrefix: tablePrefix}
	users := repositories.NewUserRepository(db, cfg)
	audit := repositories.NewAuditRepository(db, cfg)
	user := "u-" + id.New()

	if ok, err := er.EraseUserProfile(ctx, "ghost-"+id.New()); err != nil || ok {
		t.Fatalf("EraseUserProfile(missing) = %v, %v; want false, nil", ok, err)
	}

	_ = users.AcceptTerms(ctx, user)
	_ = users.SetSelfExclusion(ctx, user, &wallet.SelfExclusion{Period: "indefinite", RequestedAt: repositories.NowStr()})
	_ = users.SetGameLimits(ctx, user, &wallet.GameLimits{Daily: 1, Weekly: 2, Monthly: 3})
	_ = audit.Append(ctx, &wallet.AuditEvent{UserID: user, EventType: wallet.EventSelfExcluded, Actor: user, IP: "203.0.113.9", UserAgent: "UA/1"})
	_ = audit.Append(ctx, &wallet.AuditEvent{UserID: user, EventType: wallet.EventTermsAddendumAccepted, Actor: user})

	if ok, err := er.EraseUserProfile(ctx, user); err != nil || !ok {
		t.Fatalf("EraseUserProfile = %v, %v", ok, err)
	}
	u, err := users.Get(ctx, user)
	if err != nil || u.SelfExclusion != nil || u.GameLimits != nil || !u.TermsAccepted() {
		t.Fatalf("profile = %+v, %v; want limits/exclusion gone, consent kept", u, err)
	}

	if n, err := er.AnonymizeAudit(ctx, user); err != nil || n != 1 {
		t.Fatalf("AnonymizeAudit = %d, %v; want 1", n, err)
	}
	if n, err := er.AnonymizeAudit(ctx, user); err != nil || n != 0 {
		t.Fatalf("second AnonymizeAudit = %d, %v; want 0", n, err)
	}
	evs, err := audit.List(ctx, user, 10)
	if err != nil || len(evs) != 2 {
		t.Fatalf("audit rows = %v, %v; want both events kept", evs, err)
	}
	for _, e := range evs {
		if e.IP != "" || e.UserAgent != "" || e.EventType == "" {
			t.Fatalf("audit row %+v still carries ip/ua or lost its event", e)
		}
	}
}

func TestErasureCarriedSelfExclusionKeepsTheLongerOne(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	now := time.Now().Truncate(time.Second)
	hmac := "hmac-" + id.New()
	long := wallet.SelfExclusion{Period: "indefinite", RequestedAt: now.Format(time.RFC3339)}
	short := wallet.SelfExclusion{Period: "30d", RequestedAt: now.Format(time.RFC3339), Until: now.Add(24 * time.Hour).Format(time.RFC3339)}

	if err := er.PutCarriedSelfExclusion(ctx, hmac, long, now.Add(10*24*time.Hour)); err != nil {
		t.Fatalf("put long: %v", err)
	}
	if err := er.PutCarriedSelfExclusion(ctx, hmac, short, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("a shorter exclusion must be a no-op, got %v", err)
	}
	got, err := er.GetCarriedSelfExclusion(ctx, hmac, now)
	if err != nil || got == nil || *got != long {
		t.Fatalf("carried = %+v, %v; want the longer one", got, err)
	}
	if got, err := er.GetCarriedSelfExclusion(ctx, hmac, now.Add(11*24*time.Hour)); err != nil || got != nil {
		t.Fatalf("after ttl = %+v, %v; want nil (TTL deletes lazily)", got, err)
	}
	if got, err := er.GetCarriedSelfExclusion(ctx, "hmac-unknown-"+id.New(), now); err != nil || got != nil {
		t.Fatalf("unknown = %+v, %v; want nil", got, err)
	}
}
