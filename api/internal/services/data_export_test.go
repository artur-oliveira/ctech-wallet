package services

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// Every wallet table the CDK declares must be exported or excluded on purpose.
func TestEveryTableIsClassifiedForTheExport(t *testing.T) {
	re := regexp.MustCompile(`'(wallets|wallet_[a-z_]+)'`)
	for _, file := range []string{"../../../cdk/lib/dynamodb-stack.ts", "../../../cdk/lib/constants.ts"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			if m[1] == "wallet_locale" { // the locale cookie's name, not a table
				continue
			}
			v, ok := exportInventory[m[1]]
			if !ok {
				t.Errorf("%s declares %s, which the export does not classify: export it (add it to DataExporter.Part) or exclude it with a reason", file, m[1])
				continue
			}
			if !strings.HasPrefix(v, "exported: ") && !strings.HasPrefix(v, "excluded: ") {
				t.Errorf("%s: classification %q must start with exported: or excluded:", m[1], v)
			}
		}
	}
}

type fakeSource struct{ ledgerPages int }

func (fakeSource) Wallets(context.Context, string) ([]wallet.Wallet, error) {
	return []wallet.Wallet{{WalletID: "w1", UserID: "alice", Type: wallet.TypeReal, Balance: 500}}, nil
}

func (f *fakeSource) Statement(_ context.Context, walletID string, _ int, start map[string]types.AttributeValue) (*repositories.QueryResult, error) {
	f.ledgerPages++
	entry := func(id string) map[string]types.AttributeValue {
		return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: walletID}, "entry_id": &types.AttributeValueMemberS{Value: id},
			"type": &types.AttributeValueMemberS{Value: "deposit"}, "ref": &types.AttributeValueMemberS{Value: "wallet-of-bob"}}
	}
	if start == nil {
		return &repositories.QueryResult{Items: []map[string]types.AttributeValue{entry("e2")}, LastEvaluatedKey: map[string]types.AttributeValue{"k": &types.AttributeValueMemberS{Value: "x"}}}, nil
	}
	return &repositories.QueryResult{Items: []map[string]types.AttributeValue{entry("e1")}}, nil
}

func (fakeSource) Deposits(context.Context, string) ([]wallet.PixDeposit, error) {
	return []wallet.PixDeposit{
		{Txid: "t1", Status: wallet.DepositConfirmed, PayerCPF: "111.222.333-44", PayerName: "Alice"},
		{Txid: "t2", Status: wallet.DepositRejectedCPF, PayerCPF: "999.888.777-66", PayerName: "Mallory"},
	}, nil
}

func (fakeSource) Withdrawals(context.Context, string) ([]wallet.Withdrawal, error) {
	return []wallet.Withdrawal{{WithdrawalID: "wd1", PixKey: "alice@example.com", IdempotencyKey: "IDEM-SECRET"}}, nil
}
func (fakeSource) Holds(context.Context, string) ([]wallet.Hold, error) { return nil, nil }
func (fakeSource) SandboxPurchases(context.Context, string, int, map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error) {
	return &repositories.Page[wallet.SandboxPurchase]{}, nil
}
func (fakeSource) ProductPurchases(context.Context, string, int, map[string]types.AttributeValue) (*repositories.Page[wallet.ProductPurchase], error) {
	return &repositories.Page[wallet.ProductPurchase]{Items: []wallet.ProductPurchase{{PurchaseID: "p1", RequestHash: "HASH-SECRET"}}}, nil
}
func (fakeSource) User(context.Context, string) (*wallet.User, error) {
	return &wallet.User{UserID: "alice", SelfExclusion: &wallet.SelfExclusion{Period: "30d"}}, nil
}
func (fakeSource) Audit(context.Context, string, int) ([]wallet.AuditEvent, error) {
	return []wallet.AuditEvent{{EventID: "a1", EventType: "limits.set", IP: "1.2.3.4"}}, nil
}

func TestDataExporterPart(t *testing.T) {
	src := &fakeSource{}
	e := &DataExporter{src: src, now: time.Now}
	data, err := e.Part(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	files := map[string]string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		files[f.Name] = string(b)
	}
	for _, want := range []string{"wallets.json", "ledger-real.json", "deposits.json", "withdrawals.json", "purchases-product.json", "settings.json", "audit.json", "README.txt"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	if _, ok := files["holds.json"]; ok {
		t.Error("no holds: no file")
	}
	if src.ledgerPages != 2 || !strings.Contains(files["ledger-real.json"], `"e1"`) || !strings.Contains(files["ledger-real.json"], `"e2"`) {
		t.Errorf("the ledger must be paged to the end: pages=%d", src.ledgerPages)
	}
	all := strings.Join([]string{files["deposits.json"], files["withdrawals.json"], files["purchases-product.json"]}, "")
	if strings.Contains(all, "Mallory") || strings.Contains(all, "999.888") {
		t.Errorf("a rejected deposit's third-party payer leaked: %s", files["deposits.json"])
	}
	if !strings.Contains(files["deposits.json"], "111.222.333-44") || !strings.Contains(files["withdrawals.json"], "alice@example.com") {
		t.Errorf("own PIX data missing")
	}
	if strings.Contains(all, "IDEM-SECRET") || strings.Contains(all, "HASH-SECRET") {
		t.Errorf("internal keys leaked: %s", all)
	}
	if !strings.Contains(files["settings.json"], "30d") {
		t.Errorf("self-exclusion missing: %s", files["settings.json"])
	}
}
