# Generic PIX Product Purchase Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an M2M caller (first consumer: ctech-poker) sell a fixed-price digital good for real
PIX money through a new `ProductPurchase` primitive that never touches the sandbox ledger, wallet
lock, or credit path — generalizing the existing sandbox-purchase flow by dropping its one
sandbox-specific step.

**Architecture:** New domain type (`ProductSKU`, `ProductPurchase`), new repository/table
(`wallet_product_purchases`), new service methods on `WalletService` that mirror
`PurchaseSandboxDirect`/`ConfirmSandboxPurchase`/`RefundSandboxPurchase` structurally but skip every
credit/ledger/wallet-lock step, new M2M HTTP routes gated by a new scope, new CDK table, and Fx
wiring in `internal/app/app.go` + `cmd/reconcile/main.go`.

**Tech Stack:** Go 1.26, Fiber v3, AWS SDK v2 DynamoDB, `uber-go/fx`, AWS CDK v2 (TypeScript).

**Spec:** `docs/specs/2026-08-12-product-purchase-skus.md`

## Global Constraints

- No KYC gate; charged to CTech's pooled account via the existing Inter PIX integration
  (`pix.PixClient`) — never a wallet-to-wallet transfer, never Asaas custody.
- No ledger effect whatsoever: `ConfirmProductPurchase` must never call `s.repo.Credit`/`Debit` or
  touch `WalletRepository`.
- No refund-eligibility/usage check in wallet — that is the caller's domain fact, not wallet's
  (unlike `RefundSandboxPurchase`'s `AnyDebitSince` check).
- No admin UI/write path for SKUs — `productSKUCatalog` is a fixed Go table, edited by a deploy.
- `purchaseID` is a deterministic SHA-256 digest, prefix `"prdp"` (never `"sbxp"`), so pix-gateway's
  webhook and poker's own dispatch can route it distinctly.
- New scope `internal:wallet:product-purchase`, deliberately not reused from
  `internal:wallet:sandbox-purchase`.

---

### Task 1: `ProductSKU` catalog + `ProductPurchase` domain model

**Files:**
- Create: `api/internal/domain/wallet/product_sku.go`
- Modify: `api/internal/domain/wallet/model.go` (add `TableProductPurchases` const, `ProductPurchase`
  struct, status consts, GSI name consts)
- Test: `api/internal/domain/wallet/product_sku_test.go`

**Interfaces:**
- Produces: `wallet.ProductSKU{ID string, PriceCents int64}`, `wallet.ListProductSKUs() []ProductSKU`,
  `wallet.ProductSKUByID(id string) (ProductSKU, bool)`, `wallet.ProductPurchase` struct (below),
  `wallet.TableProductPurchases = "wallet_product_purchases"`,
  `wallet.ProductPurchasePending/Confirmed/Refunded = "pending"/"confirmed"/"refunded"`,
  `wallet.GSIProductPurchaseStatus = "gsi_product_purchase_status"`,
  `wallet.GSIProductPurchaseWebhookStatus = "gsi_product_purchase_webhook_status"`.

- [ ] **Step 1: Write the failing test**

```go
// api/internal/domain/wallet/product_sku_test.go
package wallet

import "testing"

func TestListProductSKUsIncludesEveryCatalogEntry(t *testing.T) {
	skus := ListProductSKUs()
	if len(skus) != len(productSKUCatalog) {
		t.Fatalf("got %d skus, want %d", len(skus), len(productSKUCatalog))
	}
	for _, s := range skus {
		if s.PriceCents <= 0 {
			t.Fatalf("sku %q has non-positive price %d", s.ID, s.PriceCents)
		}
	}
}

func TestProductSKUByIDUnknown(t *testing.T) {
	if _, ok := ProductSKUByID("does-not-exist"); ok {
		t.Fatal("expected ok=false for unknown sku")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/wallet/... -run TestListProductSKUs -v`
Expected: FAIL with "undefined: ListProductSKUs" (package doesn't compile yet)

- [ ] **Step 3: Write minimal implementation**

```go
// api/internal/domain/wallet/product_sku.go
package wallet

// ProductSKU is one purchasable fixed-price digital good, sold for real PIX
// money with no ledger effect (docs/specs/2026-08-12-product-purchase-skus.md).
// Price is fixed, server-side, never client- or M2M-caller-supplied — same
// "never trust a money-shaped number from outside this table" posture as
// SandboxSKU.
type ProductSKU struct {
	ID         string `json:"id"`
	PriceCents int64  `json:"price_cents"`
}

// First consumer: poker's 6 premium reactions
// (ctech-poker/docs/specs/2026-08-12-premium-reactions.md), 2 emoji + 4
// targeted objects — SKU ID is poker-chosen, price is wallet-owned.
var productSKUCatalog = map[string]ProductSKU{
	"poker_reaction_cold":   {ID: "poker_reaction_cold", PriceCents: 100},
	"poker_reaction_fire":   {ID: "poker_reaction_fire", PriceCents: 100},
	"poker_reaction_poop":   {ID: "poker_reaction_poop", PriceCents: 500},
	"poker_reaction_rofl":   {ID: "poker_reaction_rofl", PriceCents: 500},
	"poker_reaction_knife":  {ID: "poker_reaction_knife", PriceCents: 500},
	"poker_reaction_turtle": {ID: "poker_reaction_turtle", PriceCents: 500},
}

func ListProductSKUs() []ProductSKU {
	skus := make([]ProductSKU, 0, len(productSKUCatalog))
	for _, s := range productSKUCatalog {
		skus = append(skus, s)
	}
	return skus
}

// ProductSKUByID looks up a SKU by its ID, or ok=false if unknown.
func ProductSKUByID(id string) (ProductSKU, bool) {
	sku, ok := productSKUCatalog[id]
	return sku, ok
}
```

Add to `model.go`, near the `SandboxPurchase` block:

```go
const TableProductPurchases = "wallet_product_purchases"

const (
	GSIProductPurchaseStatus        = "gsi_product_purchase_status"
	GSIProductPurchaseWebhookStatus = "gsi_product_purchase_webhook_status"
)

const (
	ProductPurchasePending   = "pending"
	ProductPurchaseConfirmed = "confirmed"
	ProductPurchaseRefunded  = "refunded"
)

// ProductPurchase mirrors SandboxPurchase's shape minus everything about
// credits: no CreditSK, no CreditsGranted, no ledger entry type. There is no
// refund_pending status — a refund has nothing to resume except the PIX
// provider call itself, which is idempotent on E2EID
// (docs/specs/2026-08-12-product-purchase-skus.md).
type ProductPurchase struct {
	PurchaseID       string `dynamodbav:"pk" json:"purchase_id"`
	UserID           string `dynamodbav:"user_id" json:"user_id"`
	SKU              string `dynamodbav:"sku" json:"sku"`
	AmountExpected   int64  `dynamodbav:"amount_expected" json:"amount_expected"`
	RequestHash      string `dynamodbav:"request_hash" json:"-"`
	Status           string `dynamodbav:"status" json:"status"`
	E2EID            string `dynamodbav:"e2e_id,omitempty" json:"e2e_id,omitempty"`
	RequestingClient string `dynamodbav:"requesting_client,omitempty" json:"-"`
	WebhookStatus    string `dynamodbav:"webhook_status,omitempty" json:"-"`
	CreatedAt        string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at" json:"updated_at"`
	TTL              int64  `dynamodbav:"expires_at,omitempty" json:"-"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/domain/wallet/... -run 'TestListProductSKUs|TestProductSKUByID' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/wallet/product_sku.go api/internal/domain/wallet/product_sku_test.go api/internal/domain/wallet/model.go
git commit -m "feat(wallet): add ProductSKU catalog and ProductPurchase domain model"
```

---

### Task 2: `ProductPurchaseRepository`

**Files:**
- Create: `api/internal/repositories/product_purchases.go`
- Test: `api/internal/repositories/product_purchases_test.go` (mirror
  `sandbox_purchases_test.go`'s DynamoDB-Local integration style)

**Interfaces:**
- Consumes: `wallet.ProductPurchase` (Task 1), `Base`, `NewBase`, `Encode`/`Decode`,
  `BuildPutTxItemIfAbsent`, `TransactWrite`, `IsConditionFailed`, `UpdateItemRaw`, `QueryGSI`,
  `NowStr` (all pre-existing in `internal/repositories`, same as used by `sandbox_purchases.go`).
- Produces: `ProductPurchaseRepository{}`, `NewProductPurchaseRepository(db *dynamodb.Client, cfg
  *config.Config) *ProductPurchaseRepository`, `.PutIfAbsent(ctx, *wallet.ProductPurchase) error`,
  `.Get(ctx, purchaseID string) (*wallet.ProductPurchase, error)`, `.TransitionStatus(ctx,
  purchaseID, fromStatus, toStatus string) (bool, error)`, `.Update(ctx, purchaseID string, updates
  map[string]any) error`, `.ListPendingOlderThan(ctx, cutoff time.Time, limit int)
  ([]wallet.ProductPurchase, error)`, `.ListWebhookFailedOlderThan(ctx, cutoff time.Time, limit int)
  ([]wallet.ProductPurchase, error)`, `ErrProductPurchaseExists`.

- [ ] **Step 1: Write the failing test**

```go
// api/internal/repositories/product_purchases_test.go
package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func TestProductPurchaseRepositoryPutIfAbsentIsIdempotent(t *testing.T) {
	db, cfg := newTestDynamo(t) // existing DynamoDB-Local test helper, see sandbox_purchases_test.go
	repo := NewProductPurchaseRepository(db, cfg)
	ctx := context.Background()
	p := &wallet.ProductPurchase{
		PurchaseID: "prdp-test-1", UserID: "user-1", SKU: "poker_reaction_cold",
		AmountExpected: 100, Status: wallet.ProductPurchasePending,
		CreatedAt: NowStr(), UpdatedAt: NowStr(),
	}
	if err := repo.PutIfAbsent(ctx, p); err != nil {
		t.Fatalf("first PutIfAbsent: %v", err)
	}
	if err := repo.PutIfAbsent(ctx, p); !errors.Is(err, ErrProductPurchaseExists) {
		t.Fatalf("second PutIfAbsent: got %v, want ErrProductPurchaseExists", err)
	}
	got, err := repo.Get(ctx, "prdp-test-1")
	if err != nil || got == nil || got.Status != wallet.ProductPurchasePending {
		t.Fatalf("Get: %v, %+v", err, got)
	}
}

func TestProductPurchaseRepositoryTransitionStatus(t *testing.T) {
	db, cfg := newTestDynamo(t)
	repo := NewProductPurchaseRepository(db, cfg)
	ctx := context.Background()
	p := &wallet.ProductPurchase{
		PurchaseID: "prdp-test-2", UserID: "user-1", SKU: "poker_reaction_cold",
		AmountExpected: 100, Status: wallet.ProductPurchasePending,
		CreatedAt: NowStr(), UpdatedAt: NowStr(),
	}
	if err := repo.PutIfAbsent(ctx, p); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	ok, err := repo.TransitionStatus(ctx, "prdp-test-2", wallet.ProductPurchasePending, wallet.ProductPurchaseConfirmed)
	if err != nil || !ok {
		t.Fatalf("TransitionStatus: ok=%v err=%v", ok, err)
	}
	// Wrong `from` fails the condition and reports ok=false, not an error.
	ok, err = repo.TransitionStatus(ctx, "prdp-test-2", wallet.ProductPurchasePending, wallet.ProductPurchaseConfirmed)
	if err != nil || ok {
		t.Fatalf("expected ok=false on stale from-status, got ok=%v err=%v", ok, err)
	}
	cutoff := time.Now().Add(time.Hour)
	rows, err := repo.ListPendingOlderThan(ctx, cutoff, 10)
	if err != nil {
		t.Fatalf("ListPendingOlderThan: %v", err)
	}
	for _, r := range rows {
		if r.PurchaseID == "prdp-test-2" {
			t.Fatal("confirmed purchase must not appear in the pending sweep list")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/repositories/... -run TestProductPurchaseRepository -v`
Expected: FAIL with "undefined: NewProductPurchaseRepository"

- [ ] **Step 3: Write minimal implementation**

```go
// api/internal/repositories/product_purchases.go
package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// ProductPurchaseRepository owns persistence for the generic PIX product-sale
// flow (docs/specs/2026-08-12-product-purchase-skus.md) — its own
// table/repository, decoupled from WalletRepository and from
// SandboxPurchaseRepository: this is a sale with no ledger effect at all.
type ProductPurchaseRepository struct {
	purchases Base
}

func NewProductPurchaseRepository(db *dynamodb.Client, cfg *config.Config) *ProductPurchaseRepository {
	return &ProductPurchaseRepository{purchases: NewBase(db, cfg, wallet.TableProductPurchases)}
}

var ErrProductPurchaseExists = errors.New("repositories: product purchase already exists")

func (r *ProductPurchaseRepository) PutIfAbsent(ctx context.Context, p *wallet.ProductPurchase) error {
	av, err := Encode(p)
	if err != nil {
		return err
	}
	item := r.purchases.BuildPutTxItemIfAbsent(av)
	if err := r.purchases.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
		if IsConditionFailed(err) {
			return ErrProductPurchaseExists
		}
		return err
	}
	return nil
}

func (r *ProductPurchaseRepository) Get(ctx context.Context, purchaseID string) (*wallet.ProductPurchase, error) {
	item, err := r.purchases.GetItem(ctx, purchaseID)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.ProductPurchase](item)
}

const (
	productStatusConditionExpression = "#status = :from"
	productStatusUpdateExpression    = "SET #status = :to, " + attributeUpdatedAt + " = :now"
)

func productStatusValues(fromStatus, toStatus string) (map[string]string, map[string]types.AttributeValue) {
	return map[string]string{"#status": "status"}, map[string]types.AttributeValue{
		":from": &types.AttributeValueMemberS{Value: fromStatus},
		":to":   &types.AttributeValueMemberS{Value: toStatus},
		":now":  &types.AttributeValueMemberS{Value: NowStr()},
	}
}

// TransitionStatus is a generic conditional transition — used for both
// pending→confirmed and confirmed→refunded, since neither has a companion
// ledger transaction to combine with (that's the whole simplification versus
// SandboxPurchaseRepository's BuildConfirmTx/BuildRefundClaimTx).
func (r *ProductPurchaseRepository) TransitionStatus(ctx context.Context, purchaseID, fromStatus, toStatus string) (bool, error) {
	names, values := productStatusValues(fromStatus, toStatus)
	_, err := r.purchases.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: purchaseID}},
		UpdateExpression:          aws.String(productStatusUpdateExpression),
		ConditionExpression:       aws.String(productStatusConditionExpression),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	return err == nil, err
}

func (r *ProductPurchaseRepository) Update(ctx context.Context, purchaseID string, updates map[string]any) error {
	return UpdateItemWithTimestamp(ctx, r.purchases, purchaseID, updates)
}

func (r *ProductPurchaseRepository) ListPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error) {
	return r.listByGSIOlderThan(ctx, wallet.GSIProductPurchaseStatus, "status", wallet.ProductPurchasePending, cutoff, limit)
}

func (r *ProductPurchaseRepository) ListWebhookFailedOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error) {
	return r.listByGSIOlderThan(ctx, wallet.GSIProductPurchaseWebhookStatus, "webhook_status", wallet.WebhookFailed, cutoff, limit)
}

func (r *ProductPurchaseRepository) listByGSIOlderThan(ctx context.Context, gsiName, attr, value string, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error) {
	res, err := r.purchases.QueryGSI(ctx, gsiName, attr, value, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.ProductPurchase, 0, len(res.Items))
	for _, it := range res.Items {
		p, err := Decode[wallet.ProductPurchase](it)
		if err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, p.CreatedAt)
		if err != nil {
			continue
		}
		if createdAt.Before(cutoff) {
			out = append(out, *p)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/repositories/... -run TestProductPurchaseRepository -v`
Expected: PASS (requires DynamoDB Local — same `docker-compose.test.yml` the existing repository
tests already use)

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories/product_purchases.go api/internal/repositories/product_purchases_test.go
git commit -m "feat(wallet): add ProductPurchaseRepository"
```

---

### Task 3: `PurchaseProductDirect` service method

**Files:**
- Create: `api/internal/services/product_purchase.go`
- Test: `api/internal/services/product_purchase_test.go`

**Interfaces:**
- Consumes: `wallet.ProductSKUByID`, `wallet.ProductPurchase`, `ProductPurchaseRepo` (new interface,
  below), `pix.PixClient.CreateCharge`/`QueryCharge` (existing, `pix.FakePixClient` for tests),
  `problem.BadRequest`, `problem.IdempotencyConflict`, `problem.InternalServer` (existing), `reqHash`
  (existing package-level helper in `internal/services`).
- Produces: `productPurchaseTxID(userID, idemKey, requestingClient string) string` (prefix
  `"prdp"`), `WalletService.PurchaseProductDirect(ctx, userID, sku, idemKey, requestingClient string)
  (*wallet.ProductPurchase, *pix.Charge, error)`, a new field `productPurchases ProductPurchaseRepo`
  on `WalletService` plus `SetProductPurchases(r ProductPurchaseRepo)` setter (mirrors
  `SetSandboxPurchases`'s doc comment: unset, this method panics on first use).

- [ ] **Step 1: Write the failing test**

```go
// api/internal/services/product_purchase_test.go
package services

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

func newTestWalletServiceForProduct(t *testing.T) (*WalletService, *repositories.ProductPurchaseRepository, *pix.FakePixClient) {
	db, cfg := newTestDynamo(t) // existing helper used by sandbox_purchase_test.go
	repo := repositories.NewProductPurchaseRepository(db, cfg)
	fakePix := pix.NewFakePixClient()
	svc := NewWalletService(nil, nil, nil, nil, fakePix, nil)
	svc.SetProductPurchases(repo)
	return svc, repo, fakePix
}

func TestPurchaseProductDirectIsIdempotent(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct(t)
	ctx := context.Background()

	p1, charge1, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-1", "poker")
	if err != nil {
		t.Fatalf("first purchase: %v", err)
	}
	if p1.Status != wallet.ProductPurchasePending || p1.AmountExpected != 100 {
		t.Fatalf("unexpected purchase: %+v", p1)
	}

	p2, charge2, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-1", "poker")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if p2.PurchaseID != p1.PurchaseID {
		t.Fatalf("replay must return the same purchase_id: %s vs %s", p2.PurchaseID, p1.PurchaseID)
	}
	if charge1.QRCode != charge2.QRCode {
		t.Fatal("replay must return the (re-queried) same charge")
	}
}

func TestPurchaseProductDirectUnknownSKU(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct(t)
	_, _, err := svc.PurchaseProductDirect(context.Background(), "user-1", "no-such-sku", "idem-1", "poker")
	if err == nil {
		t.Fatal("expected an error for unknown sku")
	}
	var target interface{ Error() string }
	if !errors.As(err, &target) {
		t.Fatalf("expected a wrapped error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/services/... -run TestPurchaseProductDirect -v`
Expected: FAIL with "undefined: SetProductPurchases" / "undefined: PurchaseProductDirect"

- [ ] **Step 3: Write minimal implementation**

```go
// api/internal/services/product_purchase.go
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// productPurchaseTTLMinutes mirrors sandboxPurchaseTTLMinutes's reasoning
// exactly — must outlast both the sweep interval and the charge's real
// validity.
const productPurchaseTTLMinutes = sandboxPurchaseTTLMinutes

const (
	productPurchaseTxIDPrefix       = "prdp"
	productPurchaseTxIDDigestLength = sandboxPurchaseTxIDDigestLength
)

// productPurchaseTxID mirrors sandboxPurchaseTxID exactly, but with the
// "prdp" prefix so pix-gateway's webhook and poker's own dispatch can route
// it distinctly from a sandbox-credits purchase ("sbxp").
func productPurchaseTxID(userID, idemKey, requestingClient string) string {
	identity := requestingClient + sandboxPurchaseIDSeparator + userID + sandboxPurchaseIDSeparator + idemKey
	digest := sha256.Sum256([]byte(identity))
	return productPurchaseTxIDPrefix + hex.EncodeToString(digest[:])[:productPurchaseTxIDDigestLength]
}

// ProductPurchaseRepo is the persistence seam PurchaseProductDirect/
// ConfirmProductPurchase/RefundProductPurchase depend on — satisfied by
// *repositories.ProductPurchaseRepository in production, a fake in tests.
type ProductPurchaseRepo interface {
	PutIfAbsent(ctx context.Context, p *wallet.ProductPurchase) error
	Get(ctx context.Context, purchaseID string) (*wallet.ProductPurchase, error)
	TransitionStatus(ctx context.Context, purchaseID, fromStatus, toStatus string) (bool, error)
	Update(ctx context.Context, purchaseID string, updates map[string]any) error
	ListPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error)
	ListWebhookFailedOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error)
}

// PurchaseProductDirect sells a fixed-price digital good for real PIX money —
// no KYC gate (product sale, not custody), no ledger effect whatsoever
// (docs/specs/2026-08-12-product-purchase-skus.md). Mirrors
// PurchaseSandboxDirect's idempotent-reservation-before-charge shape exactly.
func (s *WalletService) PurchaseProductDirect(ctx context.Context, userID, sku, idemKey, requestingClient string) (*wallet.ProductPurchase, *pix.Charge, error) {
	skuDef, ok := wallet.ProductSKUByID(sku)
	if !ok {
		return nil, nil, problem.BadRequest("sku inválido")
	}
	purchaseID := productPurchaseTxID(userID, idemKey, requestingClient)
	now := repositories.NowStr()
	p := &wallet.ProductPurchase{
		PurchaseID:       purchaseID,
		UserID:           userID,
		SKU:              sku,
		AmountExpected:   skuDef.PriceCents,
		RequestHash:      reqHash(requestingClient+"#"+userID+"#"+sku, skuDef.PriceCents),
		Status:           wallet.ProductPurchasePending,
		RequestingClient: requestingClient,
		CreatedAt:        now,
		UpdatedAt:        now,
		TTL:              time.Now().Add(productPurchaseTTLMinutes * time.Minute).Unix(),
	}
	if err := s.productPurchases.PutIfAbsent(ctx, p); err != nil {
		if !errors.Is(err, repositories.ErrProductPurchaseExists) {
			return nil, nil, err
		}
		existing, gerr := s.productPurchases.Get(ctx, purchaseID)
		if gerr != nil {
			return nil, nil, gerr
		}
		if (existing.RequestHash != "" && existing.RequestHash != p.RequestHash) ||
			existing.UserID != userID || existing.SKU != sku || existing.RequestingClient != requestingClient {
			return nil, nil, problem.IdempotencyConflict()
		}
		charge, qerr := s.pix.QueryCharge(ctx, purchaseID)
		if qerr != nil {
			charge, qerr = s.pix.CreateCharge(ctx, purchaseID, existing.AmountExpected, "")
			if qerr != nil {
				return nil, nil, qerr
			}
		}
		return existing, charge, nil
	}

	charge, err := s.pix.CreateCharge(ctx, purchaseID, skuDef.PriceCents, "")
	if err != nil {
		slog.Error("product purchase charge creation failed", "purchase_id", purchaseID, "err", err)
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX")
	}
	return p, charge, nil
}

// SetProductPurchases wires the generic product-purchase repository after
// construction — same setter pattern as SetSandboxPurchases. Unset,
// PurchaseProductDirect/ConfirmProductPurchase/RefundProductPurchase panic on
// first use — cmd/server and cmd/reconcile must always call this.
func (s *WalletService) SetProductPurchases(r ProductPurchaseRepo) {
	s.productPurchases = r
}
```

Add the `productPurchases ProductPurchaseRepo` field to `WalletService`'s struct in
`api/internal/services/wallet.go`, next to `sandboxPurchases SandboxPurchaseRepo`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/services/... -run TestPurchaseProductDirect -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/product_purchase.go api/internal/services/product_purchase_test.go api/internal/services/wallet.go
git commit -m "feat(wallet): add PurchaseProductDirect"
```

---

### Task 4: `ConfirmProductPurchase` + product webhook notify-back

**Files:**
- Modify: `api/internal/services/product_purchase.go` (add `ConfirmProductPurchase`)
- Modify: `api/internal/services/m2m_webhook.go` (add `Kind` field, `dispatchM2MWebhookProduct`, set
  `Kind: "sandbox"` explicitly on the existing `dispatchM2MWebhook`)
- Test: append to `api/internal/services/product_purchase_test.go`

**Interfaces:**
- Consumes: `pix.PixClient.QueryCharge`, `pix.ChargeCompleted` (existing).
- Produces: `WalletService.ConfirmProductPurchase(ctx, txid string, sweep bool) error`.

- [ ] **Step 1: Write the failing test**

```go
func TestConfirmProductPurchaseCreditsNothing(t *testing.T) {
	svc, repo, fakePix := newTestWalletServiceForProduct(t)
	ctx := context.Background()

	p, _, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-2", "poker")
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	fakePix.StageChargeCompleted(p.PurchaseID, p.AmountExpected) // existing FakePixClient helper, mirrors StageChargeRefund

	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	confirmed, err := repo.Get(ctx, p.PurchaseID)
	if err != nil || confirmed.Status != wallet.ProductPurchaseConfirmed {
		t.Fatalf("expected confirmed status, got %+v (err=%v)", confirmed, err)
	}

	// Idempotent replay: already-confirmed is a no-op, not an error.
	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err != nil {
		t.Fatalf("replay confirm: %v", err)
	}
}

func TestConfirmProductPurchaseAmountMismatchStaysPending(t *testing.T) {
	svc, repo, fakePix := newTestWalletServiceForProduct(t)
	ctx := context.Background()

	p, _, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-3", "poker")
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	fakePix.StageChargeCompleted(p.PurchaseID, p.AmountExpected+1) // wrong amount

	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err == nil {
		t.Fatal("expected an error on amount mismatch")
	}
	stillPending, err := repo.Get(ctx, p.PurchaseID)
	if err != nil || stillPending.Status != wallet.ProductPurchasePending {
		t.Fatalf("expected purchase to stay pending for manual reconciliation, got %+v (err=%v)", stillPending, err)
	}
}
```

If `pix.FakePixClient` has no `StageChargeCompleted` helper yet, add it (mirrors the existing
`StageChargeRefund` in `api/internal/pix/fake.go`):

```go
// api/internal/pix/fake.go — add alongside StageChargeRefund
func (f *FakePixClient) StageChargeCompleted(txid string, amount int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.charges[txid] = &Charge{Txid: txid, Amount: amount, Status: ChargeCompleted, E2EID: "e2e-" + txid}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/services/... -run TestConfirmProductPurchase -v`
Expected: FAIL with "undefined: ConfirmProductPurchase"

- [ ] **Step 3: Write minimal implementation**

Append to `product_purchase.go`:

```go
// ConfirmProductPurchase re-queries the PIX charge (never trusts the webhook
// body — Invariant #11), and on success just transitions pending→confirmed
// and dispatches the M2M webhook. No wallet lock, no Credit, no ledger entry
// — this is the entire generalization versus ConfirmSandboxPurchase
// (docs/specs/2026-08-12-product-purchase-skus.md).
func (s *WalletService) ConfirmProductPurchase(ctx context.Context, txid string, sweep bool) error {
	p, err := s.productPurchases.Get(ctx, txid)
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	if p.Status != wallet.ProductPurchasePending {
		return nil
	}

	charge, err := s.pix.QueryCharge(ctx, txid)
	if err != nil {
		return err
	}
	if charge.Status != pix.ChargeCompleted {
		return nil
	}
	if charge.Amount != p.AmountExpected {
		slog.Error("ALARM product purchase amount mismatch", "purchase_id", txid, "expected", p.AmountExpected, "paid", charge.Amount)
		return problem.InternalServer("valor pago não corresponde ao esperado; reconciliação manual necessária")
	}

	changed, err := s.productPurchases.TransitionStatus(ctx, txid, wallet.ProductPurchasePending, wallet.ProductPurchaseConfirmed)
	if err != nil {
		return err
	}
	if !changed {
		return nil // lost a race with a concurrent confirm — already handled
	}
	p.Status = wallet.ProductPurchaseConfirmed
	p.E2EID = charge.E2EID
	s.dispatchM2MWebhookProduct(ctx, p)
	return nil
}
```

Edit `m2m_webhook.go`:

```go
type m2mWebhookPayload struct {
	PurchaseID     string `json:"purchase_id"`
	UserID         string `json:"user_id"`
	SKU            string `json:"sku"`
	Status         string `json:"status"`
	AmountExpected int64  `json:"amount_expected"`
	CreditsGranted int64  `json:"credits_granted,omitempty"`
	Kind           string `json:"kind,omitempty"`
}
```

In the existing `dispatchM2MWebhook`, set `Kind: "sandbox"` explicitly inside the
`json.Marshal(m2mWebhookPayload{...})` call. Then add:

```go
// dispatchM2MWebhookProduct mirrors dispatchM2MWebhook exactly but for a
// *wallet.ProductPurchase — same delivery/retry machinery
// (markM2MWebhook/HeaderM2MWebhookSignature), Kind: "product" lets a
// receiver registered for both flows route without inspecting the SKU
// namespace.
func (s *WalletService) dispatchM2MWebhookProduct(ctx context.Context, p *wallet.ProductPurchase) {
	if p.RequestingClient == "" {
		return
	}
	client, ok := s.m2mClients[p.RequestingClient]
	if !ok || client.WebhookURL == "" {
		slog.ErrorContext(ctx, "m2m webhook: no registered webhook for client", "client", p.RequestingClient, "purchase_id", p.PurchaseID)
		s.markM2MWebhookProduct(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}
	body, err := json.Marshal(m2mWebhookPayload{
		PurchaseID: p.PurchaseID, UserID: p.UserID, SKU: p.SKU, Status: p.Status,
		AmountExpected: p.AmountExpected, Kind: "product",
	})
	if err != nil {
		slog.ErrorContext(ctx, "m2m webhook: marshal failed", "purchase_id", p.PurchaseID, "err", err)
		s.markM2MWebhookProduct(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}
	mac := hmac.New(sha256.New, []byte(client.HMACSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	reqCtx, cancel := context.WithTimeout(ctx, m2mWebhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, client.WebhookURL, bytes.NewReader(body))
	if err != nil {
		slog.ErrorContext(ctx, "m2m webhook: build request failed", "purchase_id", p.PurchaseID, "err", err)
		s.markM2MWebhookProduct(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderM2MWebhookSignature, sig)

	resp, err := m2mWebhookHTTPClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "m2m webhook: delivery failed, will retry via reconcile sweep", "purchase_id", p.PurchaseID, "client", p.RequestingClient, "err", err)
		s.markM2MWebhookProduct(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	s.markM2MWebhookProduct(ctx, p.PurchaseID, wallet.WebhookDelivered)
}

func (s *WalletService) markM2MWebhookProduct(ctx context.Context, purchaseID, status string) {
	if err := s.productPurchases.Update(ctx, purchaseID, map[string]any{"webhook_status": status}); err != nil {
		slog.ErrorContext(ctx, "m2m webhook: mark status failed", "purchase_id", purchaseID, "status", status, "err", err)
	}
}
```

(Check the exact body of the existing `markM2MWebhook` before writing `markM2MWebhookProduct` —
mirror its real update call/fields verbatim rather than the sketch above if they differ; this task's
review must diff the two side by side.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/services/... -run TestConfirmProductPurchase -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/product_purchase.go api/internal/services/m2m_webhook.go api/internal/pix/fake.go api/internal/services/product_purchase_test.go
git commit -m "feat(wallet): add ConfirmProductPurchase and product webhook notify-back"
```

---

### Task 5: `RefundProductPurchase` + `GetProductPurchase`

**Files:**
- Modify: `api/internal/services/product_purchase.go`
- Test: append to `api/internal/services/product_purchase_test.go`

**Interfaces:**
- Consumes: `pix.PixClient.Refund` (existing), `problem.NotFound`, `problem.Conflict` (existing).
- Produces: `WalletService.RefundProductPurchase(ctx, userID, purchaseID, idemKey,
  requestingClient string) (*wallet.ProductPurchase, error)`, `WalletService.GetProductPurchase(ctx,
  purchaseID, requestingClient string) (*wallet.ProductPurchase, error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestRefundProductPurchaseHappyPath(t *testing.T) {
	svc, _, fakePix := newTestWalletServiceForProduct(t)
	ctx := context.Background()

	p, _, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-4", "poker")
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	fakePix.StageChargeCompleted(p.PurchaseID, p.AmountExpected)
	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	refunded, err := svc.RefundProductPurchase(ctx, "user-1", p.PurchaseID, "idem-refund-1", "poker")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refunded.Status != wallet.ProductPurchaseRefunded {
		t.Fatalf("expected refunded status, got %+v", refunded)
	}

	// Idempotent replay.
	again, err := svc.RefundProductPurchase(ctx, "user-1", p.PurchaseID, "idem-refund-2", "poker")
	if err != nil || again.Status != wallet.ProductPurchaseRefunded {
		t.Fatalf("replay refund: %v, %+v", err, again)
	}
}

func TestRefundProductPurchaseCrossClientNotFound(t *testing.T) {
	svc, _, fakePix := newTestWalletServiceForProduct(t)
	ctx := context.Background()

	p, _, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-5", "poker")
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	fakePix.StageChargeCompleted(p.PurchaseID, p.AmountExpected)
	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if _, err := svc.RefundProductPurchase(ctx, "user-1", p.PurchaseID, "idem-x", "some-other-client"); err == nil {
		t.Fatal("expected an error for a purchase opened by a different client")
	}
	if _, err := svc.GetProductPurchase(ctx, p.PurchaseID, "some-other-client"); err == nil {
		t.Fatal("expected an error (not-found) for a purchase opened by a different client")
	}
}

func TestRefundProductPurchaseNotYetConfirmed(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct(t)
	ctx := context.Background()
	p, _, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-6", "poker")
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	if _, err := svc.RefundProductPurchase(ctx, "user-1", p.PurchaseID, "idem-refund-x", "poker"); err == nil {
		t.Fatal("expected a conflict refunding a pending (never-confirmed) purchase")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/services/... -run TestRefundProductPurchase -v`
Expected: FAIL with "undefined: RefundProductPurchase"

- [ ] **Step 3: Write minimal implementation**

```go
// RefundProductPurchase reverses a confirmed purchase. No usage-eligibility
// check here — that is the caller's domain fact (docs/specs/2026-08-12-
// product-purchase-skus.md Non-goals), unlike RefundSandboxPurchase's
// AnyDebitSince check. requestingClient isolates cross-client access: a
// purchase opened by a different client (or the user-direct route) is
// reported as not-found, never leaked.
func (s *WalletService) RefundProductPurchase(ctx context.Context, userID, purchaseID, idemKey, requestingClient string) (*wallet.ProductPurchase, error) {
	p, err := s.productPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.UserID != userID || (requestingClient != "" && p.RequestingClient != requestingClient) {
		return nil, problem.NotFound("compra não encontrada")
	}
	if p.Status == wallet.ProductPurchaseRefunded {
		return p, nil
	}
	if p.Status != wallet.ProductPurchaseConfirmed {
		return nil, problem.Conflict("compra ainda não confirmada")
	}

	if _, err := s.pix.Refund(ctx, p.E2EID, p.AmountExpected, "product_refund#"+purchaseID); err != nil {
		slog.Error("ALARM product purchase refund failed", "purchase_id", purchaseID, "e2e_id", p.E2EID, "err", err)
		return nil, problem.InternalServer("estorno da compra falhou; nova tentativa agendada")
	}
	changed, err := s.productPurchases.TransitionStatus(ctx, purchaseID, wallet.ProductPurchaseConfirmed, wallet.ProductPurchaseRefunded)
	if err != nil {
		return nil, err
	}
	if !changed {
		current, err := s.productPurchases.Get(ctx, purchaseID)
		if err != nil {
			return nil, err
		}
		return current, nil
	}
	p.Status = wallet.ProductPurchaseRefunded
	s.dispatchM2MWebhookProduct(ctx, p)
	return p, nil
}

// GetProductPurchase is the M2M poll endpoint's read path — never the
// webhook body alone. Ownership enforced identically to
// RefundProductPurchase.
func (s *WalletService) GetProductPurchase(ctx context.Context, purchaseID, requestingClient string) (*wallet.ProductPurchase, error) {
	p, err := s.productPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.RequestingClient != requestingClient {
		return nil, problem.NotFound("compra não encontrada")
	}
	return p, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/services/... -run TestRefundProductPurchase -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/product_purchase.go api/internal/services/product_purchase_test.go
git commit -m "feat(wallet): add RefundProductPurchase and GetProductPurchase"
```

---

### Task 6: `SweepPendingProductPurchases` + reconcile wiring

**Files:**
- Modify: `api/internal/services/product_purchase.go`
- Modify: `api/cmd/reconcile/main.go`
- Test: append to `api/internal/services/product_purchase_test.go`

**Interfaces:**
- Produces: `WalletService.SweepPendingProductPurchases(ctx) (swept int, err error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestSweepPendingProductPurchasesConfirmsAgedOnes(t *testing.T) {
	svc, repo, fakePix := newTestWalletServiceForProduct(t)
	ctx := context.Background()

	p, _, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-7", "poker")
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	fakePix.StageChargeCompleted(p.PurchaseID, p.AmountExpected)
	// Backdate CreatedAt past the sweep's age threshold so it's picked up.
	if err := repo.Update(ctx, p.PurchaseID, map[string]any{"created_at": "2020-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	swept, err := svc.SweepPendingProductPurchases(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if swept != 1 {
		t.Fatalf("expected 1 swept purchase, got %d", swept)
	}
	confirmed, err := repo.Get(ctx, p.PurchaseID)
	if err != nil || confirmed.Status != wallet.ProductPurchaseConfirmed {
		t.Fatalf("expected confirmed after sweep, got %+v (err=%v)", confirmed, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/services/... -run TestSweepPendingProductPurchases -v`
Expected: FAIL with "undefined: SweepPendingProductPurchases"

- [ ] **Step 3: Write minimal implementation**

```go
// SweepPendingProductPurchases re-queries the PIX provider once for every
// pending purchase approaching its TTL — mirrors SweepPendingSandboxPurchases,
// reusing ConfirmProductPurchase's own idempotent confirm logic. No
// SweepRefundPendingProductPurchases counterpart exists: a refund has no
// pending stage to resume (docs/specs/2026-08-12-product-purchase-skus.md).
func (s *WalletService) SweepPendingProductPurchases(ctx context.Context) (swept int, err error) {
	cutoff := time.Now().Add(-sweepAgeThreshold)
	purchases, err := s.productPurchases.ListPendingOlderThan(ctx, cutoff, reconcileBatch)
	if err != nil {
		return 0, err
	}
	for i := range purchases {
		p := purchases[i]
		if err := s.ConfirmProductPurchase(ctx, p.PurchaseID, true); err != nil {
			slog.Warn("sweep: confirm-product-purchase failed, will retry next run", "purchase_id", p.PurchaseID, "err", err)
			continue
		}
		swept++
	}
	return swept, nil
}
```

In `api/cmd/reconcile/main.go`, next to the existing sandbox wiring (around line 111-134):

```go
svc.SetProductPurchases(repositories.NewProductPurchaseRepository(clients.DynamoDB, cfg))
// ... (SetM2MClients(m2mClients) already covers both flows — same map)
sweptProducts, err := svc.SweepPendingProductPurchases(ctx)
if err != nil {
	slog.Error("sweep pending product purchases failed", "err", err)
} else {
	slog.Info("swept pending product purchases", "count", sweptProducts)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/services/... -run TestSweepPendingProductPurchases -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/product_purchase.go api/cmd/reconcile/main.go api/internal/services/product_purchase_test.go
git commit -m "feat(wallet): add SweepPendingProductPurchases and reconcile wiring"
```

---

### Task 7: New scope + M2M HTTP handlers + router registration

**Files:**
- Modify: `api/internal/middleware/scope.go` (add `ScopeWalletProductPurchase`)
- Create: `api/internal/api/v1/m2m_product_purchase.go`
- Modify: `api/internal/api/v1/router.go`
- Test: `api/internal/api/v1/m2m_product_purchase_test.go`

**Interfaces:**
- Consumes: `middleware.GetClaims(c).AZP`, `bindJSON`, `sendProblem` (existing helpers used by
  `m2m_sandbox_purchase.go`).
- Produces: HTTP routes under `/v1.0/internal/wallet/product-purchase/*`.

- [ ] **Step 1: Write the failing test**

```go
// api/internal/api/v1/m2m_product_purchase_test.go
package v1

// Mirrors m2m_sandbox_purchase_test.go's shape: spin up the router with a
// fake WalletService double, assert scope-gating (missing scope → 403) and
// cross-client ownership (wrong AZP on GET/refund → 404). Read
// m2m_sandbox_purchase_test.go in full before writing this file and copy its
// test harness (router setup, fake claims injection) verbatim, swapping the
// sandbox routes/handlers for the product ones below.
```

(This step is a structural copy of an existing test file, not new test design — the actual test
functions are written in Step 3 alongside the handlers, following
`m2m_sandbox_purchase_test.go`'s exact harness.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/api/v1/... -run TestM2MProductPurchase -v`
Expected: FAIL (handlers/routes don't exist yet)

- [ ] **Step 3: Write minimal implementation**

Add to `scope.go`:

```go
// ScopeWalletProductPurchase lets an M2M client sell a generic digital
// product for real PIX money with no ledger effect — deliberately its own
// scope, not reused from ScopeWalletSandboxPurchase: materially different
// blast radius (docs/specs/2026-08-12-product-purchase-skus.md).
ScopeWalletProductPurchase = "internal:wallet:product-purchase"
```

```go
// api/internal/api/v1/m2m_product_purchase.go
package v1

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

func productExpiresAtRFC3339(ttl int64) string {
	return time.Unix(ttl, 0).UTC().Format(time.RFC3339)
}

type productPurchaseWithExpiry struct {
	*wallet.ProductPurchase
	ExpiresAt string `json:"expires_at"`
}

func withProductExpiry(p *wallet.ProductPurchase) productPurchaseWithExpiry {
	return productPurchaseWithExpiry{ProductPurchase: p, ExpiresAt: productExpiresAtRFC3339(p.TTL)}
}

func (h *handlers) m2mListProductSKUs(c fiber.Ctx) error {
	skus := wallet.ListProductSKUs()
	out := make([]fiber.Map, len(skus))
	for i, s := range skus {
		out[i] = fiber.Map{"id": s.ID, "price_cents": s.PriceCents}
	}
	return c.JSON(out)
}

func (h *handlers) m2mPurchaseProduct(c fiber.Ctx) error {
	var body M2MSandboxPurchaseRequest // same {user_id, sku, idempotency_key} shape — reused, not duplicated
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	client := middleware.GetClaims(c).AZP
	purchase, charge, err := h.svc.PurchaseProductDirect(c.Context(), body.UserID, body.SKU, body.IdempotencyKey, client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"purchase_id":      purchase.PurchaseID,
		"sku":              purchase.SKU,
		"amount":           purchase.AmountExpected,
		"status":           purchase.Status,
		"pix_copia_e_cola": charge.QRCode,
		"qr_code_base64":   charge.QRCodeB64,
		"expires_at":       productExpiresAtRFC3339(purchase.TTL),
	})
}

func (h *handlers) m2mGetProductPurchase(c fiber.Ctx) error {
	client := middleware.GetClaims(c).AZP
	purchase, err := h.svc.GetProductPurchase(c.Context(), c.Params("id"), client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(withProductExpiry(purchase))
}

func (h *handlers) m2mRefundProductPurchase(c fiber.Ctx) error {
	var body M2MRefundSandboxPurchaseRequest // same {user_id, idempotency_key} shape — reused
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	client := middleware.GetClaims(c).AZP
	purchase, err := h.svc.RefundProductPurchase(c.Context(), body.UserID, c.Params("id"), body.IdempotencyKey, client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(withProductExpiry(purchase))
}
```

In `router.go`, next to the existing `sp := internal.Group("/wallet/sandbox-purchase", ...)` block
(around line 125):

```go
pp := internal.Group("/wallet/product-purchase", middleware.RequireScope(middleware.ScopeWalletProductPurchase))
pp.Get("/skus", h.m2mListProductSKUs)
pp.Post("/", h.m2mPurchaseProduct)
pp.Get("/:id", h.m2mGetProductPurchase)
pp.Post("/:id/refund", h.m2mRefundProductPurchase)
```

Now write the actual test functions in `m2m_product_purchase_test.go`, copying
`m2m_sandbox_purchase_test.go`'s harness and asserting: 201 on create, scope-gated 403 with no/wrong
scope, and 404 (not 403) on `GET`/`refund` with a mismatched AZP claim.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/api/v1/... -run TestM2MProductPurchase -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/internal/middleware/scope.go api/internal/api/v1/m2m_product_purchase.go api/internal/api/v1/m2m_product_purchase_test.go api/internal/api/v1/router.go
git commit -m "feat(wallet): add M2M product-purchase HTTP routes"
```

---

### Task 8: Fx wiring (`cmd/server` via `internal/app/app.go`)

**Files:**
- Modify: `api/internal/app/app.go`

**Interfaces:**
- Consumes: everything from Tasks 1-7.

- [ ] **Step 1: Write the failing test**

No new unit test — this task is pure dependency wiring. Verification is Step 2's build/boot check.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go build ./...`
Expected: builds fine today (nothing references `NewProductPurchaseRepository`/
`SetProductPurchases` from `app.go` yet) — this task's "fail" is functional, not a compile error;
skip straight to Step 3.

- [ ] **Step 3: Write minimal implementation**

In `app.go`'s `fx.Provide(...)` list (around line 53, next to
`repositories.NewSandboxPurchaseRepository`), add:

```go
repositories.NewProductPurchaseRepository,
```

In `newWalletService` (around line 216), add a `productPurchases
*repositories.ProductPurchaseRepository` parameter and wire it:

```go
func newWalletService(repo *repositories.WalletRepository, users *repositories.UserRepository, audit *repositories.AuditRepository, l *lock.Locker, p pix.PixClient, k services.KYCClient, baas *services.BaasService, sandboxPurchases *repositories.SandboxPurchaseRepository, productPurchases *repositories.ProductPurchaseRepository, m2mClients map[string]services.M2MClient, cfg *config.Config) *services.WalletService {
	svc := services.NewWalletService(repo, users, audit, l, p, k)
	svc.SetBaas(baas)
	svc.SetCustodyEnabled(cfg.AsaasCustodyEnabled)
	svc.SetSandboxPurchases(sandboxPurchases)
	svc.SetProductPurchases(productPurchases)
	svc.SetM2MClients(m2mClients)
	baas.SetWithdrawalReverser(svc.ReverseWithdrawal)
	return svc
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go build ./... && go run ./cmd/server --help` (or however this repo already smoke-
tests Fx wiring — check `README.md`/`Makefile` for the exact boot-check command used elsewhere)
Expected: builds and boots without an Fx "missing dependency" panic.

- [ ] **Step 5: Commit**

```bash
git add api/internal/app/app.go
git commit -m "feat(wallet): wire ProductPurchaseRepository into cmd/server"
```

---

### Task 9: CDK — `wallet_product_purchases` table

**Files:**
- Modify: `cdk/lib/dynamodb-stack.ts`
- Test: `cdk/test/dynamodb-stack.test.ts`

**Interfaces:**
- Produces: a new `TableName` union member `'wallet_product_purchases'`, a new table with 2 GSIs
  mirroring `wallet_sandbox_purchases`'s.

- [ ] **Step 1: Write the failing test**

```typescript
// cdk/test/dynamodb-stack.test.ts — add near the existing sandbox-purchases assertions
test('wallet_product_purchases table has status and webhook-status GSIs', () => {
  const template = Template.fromStack(stack); // however the existing tests in this file build `stack`
  template.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: Match.stringLikeRegexp('wallet_product_purchases'),
  });
});
```

(Match this file's existing assertion style exactly — read the surrounding `wallet_sandbox_purchases`
test in this same file before writing, and copy its structure rather than the sketch above.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd cdk && npx jest dynamodb-stack.test.ts`
Expected: FAIL — no `wallet_product_purchases` table exists yet

- [ ] **Step 3: Write minimal implementation**

In `dynamodb-stack.ts`:

```typescript
export type TableName = (
    // ...existing entries...
    'wallet_sandbox_purchases' |
    'wallet_product_purchases'
);
```

```typescript
const GSI_PRODUCT_PURCHASE_STATUS = 'gsi_product_purchase_status';
const GSI_PRODUCT_PURCHASE_WEBHOOK_STATUS = 'gsi_product_purchase_webhook_status';
```

Next to the existing `wallet_sandbox_purchases` block (around line 204):

```typescript
// wallet_product_purchases: pk = purchase_id — generic PIX product sale,
// deliberately decoupled from every ledger table: no credit ever lands on
// this purchase (docs/specs/2026-08-12-product-purchase-skus.md).
// gsi_product_purchase_status backs the pending-purchase sweep.
const productPurchasesTable = table('wallet_product_purchases');
gsi(productPurchasesTable, GSI_PRODUCT_PURCHASE_STATUS, ATTR_STATUS);
gsi(productPurchasesTable, GSI_PRODUCT_PURCHASE_WEBHOOK_STATUS, ATTR_WEBHOOK_STATUS);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd cdk && npx jest dynamodb-stack.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cdk/lib/dynamodb-stack.ts cdk/test/dynamodb-stack.test.ts
git commit -m "feat(cdk): add wallet_product_purchases table"
```

---

## Self-Review Notes

- **Spec coverage:** Domain model (Task 1), repository (Task 2), all 5 service methods + txID
  helper (Tasks 3-6), endpoints table (Task 7), notify-back `kind` field (Task 4), new scope (Task
  7), CDK (Task 9) — every spec section maps to a task. Manual provisioning (granting the scope in
  `ctech-account`) is explicitly out of engineering scope per the spec and is not a task here.
- **Placeholder scan:** Task 7's Step 1 and Task 9's Step 1 point at an existing sibling test file to
  copy rather than inventing new test scaffolding from scratch — this is a deliberate "go read the
  real harness first" instruction, not a TBD; the actual assertions are written in each task's Step
  3/4, same as every other task.
- **Type consistency:** `ProductPurchaseRepo` (Task 3) matches `*repositories.ProductPurchaseRepository`'s
  method set exactly as built in Task 2. `productPurchaseTxID` (Task 3) reuses
  `sandboxPurchaseIDSeparator`/`sandboxPurchaseTxIDDigestLength` from the existing
  `sandbox_purchase.go` rather than redefining them.
