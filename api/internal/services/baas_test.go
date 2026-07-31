package services

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/asaas"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// fakeBaasRepo is a minimal in-memory BaasRepo for unit tests.
type fakeBaasRepo struct {
	accounts    map[string]*wallet.BaasAccount
	intents     map[string]*wallet.TransferIntent
	receivables map[string]*wallet.MedReceivable
}

func newFakeBaasRepo() *fakeBaasRepo {
	return &fakeBaasRepo{
		accounts: map[string]*wallet.BaasAccount{}, intents: map[string]*wallet.TransferIntent{},
		receivables: map[string]*wallet.MedReceivable{},
	}
}

func (f *fakeBaasRepo) GetBaasAccount(_ context.Context, userID string) (*wallet.BaasAccount, error) {
	return f.accounts[userID], nil
}
func (f *fakeBaasRepo) GetBaasAccountByProviderID(_ context.Context, providerAccountID string) (*wallet.BaasAccount, error) {
	for _, a := range f.accounts {
		if a.ProviderAccountID == providerAccountID {
			return a, nil
		}
	}
	return nil, nil
}
func (f *fakeBaasRepo) PutBaasAccount(_ context.Context, a *wallet.BaasAccount) error {
	if _, exists := f.accounts[a.UserID]; exists {
		return repositories.ErrBaasAccountExists
	}
	f.accounts[a.UserID] = a
	return nil
}
func (f *fakeBaasRepo) UpdateBaasAccount(_ context.Context, userID string, updates map[string]any) error {
	a := f.accounts[userID]
	if a == nil {
		return nil
	}
	if v, ok := updates["status"].(string); ok {
		a.Status = v
	}
	if v, ok := updates["conservation_drift"].(bool); ok {
		a.ConservationDrift = v
	}
	if v, ok := updates["evp_pix_key"].(string); ok {
		a.EVPPixKey = v
	}
	if v, ok := updates["provider_account_id"].(string); ok {
		a.ProviderAccountID = v
	}
	if v, ok := updates["provider_wallet_id"].(string); ok {
		a.ProviderWalletID = v
	}
	if v, ok := updates["api_key_ciphertext"].([]byte); ok {
		a.APIKeyCiphertext = v
	}
	if v, ok := updates["api_key_nonce"].([]byte); ok {
		a.APIKeyNonce = v
	}
	return nil
}
func (f *fakeBaasRepo) PutTransferIntentIfAbsent(_ context.Context, t *wallet.TransferIntent) error {
	if _, ok := f.intents[t.ExternalReference]; ok {
		return repositories.ErrTransferIntentExists
	}
	cp := *t
	f.intents[t.ExternalReference] = &cp
	return nil
}
func (f *fakeBaasRepo) GetTransferIntent(_ context.Context, externalReference string) (*wallet.TransferIntent, error) {
	return f.intents[externalReference], nil
}
func (f *fakeBaasRepo) UpdateTransferIntent(_ context.Context, externalReference string, updates map[string]any) error {
	t := f.intents[externalReference]
	if t == nil {
		return nil
	}
	if v, ok := updates["status"].(string); ok {
		t.Status = v
	}
	return nil
}
func (f *fakeBaasRepo) ListTransferIntentsByStatus(_ context.Context, status string, _ int) ([]wallet.TransferIntent, error) {
	var out []wallet.TransferIntent
	for _, t := range f.intents {
		if t.Status == status {
			out = append(out, *t)
		}
	}
	return out, nil
}
func (f *fakeBaasRepo) ListBaasAccountsByStatus(_ context.Context, status string, _ int) ([]wallet.BaasAccount, error) {
	var out []wallet.BaasAccount
	for _, a := range f.accounts {
		if a.Status == status {
			out = append(out, *a)
		}
	}
	return out, nil
}
func (f *fakeBaasRepo) PutMedReceivableIfAbsent(_ context.Context, m *wallet.MedReceivable) error {
	if _, ok := f.receivables[m.ReceivableID]; ok {
		return nil
	}
	cp := *m
	f.receivables[m.ReceivableID] = &cp
	return nil
}
func (f *fakeBaasRepo) ListOpenMedReceivablesForWallet(_ context.Context, walletID string, _ int) ([]wallet.MedReceivable, error) {
	var out []wallet.MedReceivable
	for _, m := range f.receivables {
		if m.WalletID == walletID && m.Status == wallet.MedReceivableOpen {
			out = append(out, *m)
		}
	}
	return out, nil
}

// fakeWallets is a no-op RealWalletEnsurer for tests that don't exercise the
// account-approval path (which is what actually calls EnsureRealWallet).
type fakeWallets struct{}

func (fakeWallets) EnsureRealWallet(context.Context, string) (*wallet.Wallet, error) {
	return &wallet.Wallet{WalletID: "WALLET#fake", Type: wallet.TypeReal}, nil
}
func (fakeWallets) LoadWallets(context.Context, string) (*wallet.Wallet, *wallet.Wallet, *wallet.Wallet, error) {
	return nil, nil, nil, nil
}
func (fakeWallets) ListOpenHoldsForWallet(context.Context, string, int) ([]wallet.Hold, error) {
	return nil, nil
}
func (fakeWallets) UpdateWithdrawal(context.Context, string, map[string]any) error {
	return nil
}

func TestGetIfApprovedOnlyReturnsApprovedAccounts(t *testing.T) {
	repo := newFakeBaasRepo()
	svc := NewBaasService(repo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	if acc, err := svc.GetIfApproved(context.Background(), "u1"); err != nil || acc != nil {
		t.Fatalf("absent account: got %v, %v", acc, err)
	}

	repo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasOnboarding}
	if acc, err := svc.GetIfApproved(context.Background(), "u1"); err != nil || acc != nil {
		t.Fatalf("onboarding account: got %v, %v", acc, err)
	}

	repo.accounts["u1"].Status = wallet.BaasApproved
	acc, err := svc.GetIfApproved(context.Background(), "u1")
	if err != nil || acc == nil {
		t.Fatalf("approved account: got %v, %v", acc, err)
	}
}

func TestAuthorizeTransferApprovesOnExactMatch(t *testing.T) {
	repo := newFakeBaasRepo()
	svc := NewBaasService(repo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	repo.intents["ref1"] = &wallet.TransferIntent{
		ExternalReference: "ref1", Status: wallet.IntentAwaitingAuthorization, Amount: 1000, Destination: "wallet_parent",
	}

	approved, reason := svc.AuthorizeTransfer(context.Background(), "ref1", 1000, "wallet_parent")
	if !approved || reason != "" {
		t.Fatalf("expected approved, got approved=%v reason=%q", approved, reason)
	}
	if repo.intents["ref1"].Status != wallet.IntentProcessing {
		t.Fatalf("expected status processing, got %q", repo.intents["ref1"].Status)
	}
}

func TestAuthorizeTransferRefusesUnknownReference(t *testing.T) {
	repo := newFakeBaasRepo()
	svc := NewBaasService(repo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	approved, reason := svc.AuthorizeTransfer(context.Background(), "missing", 1000, "wallet_parent")
	if approved || reason != "unknown_reference" {
		t.Fatalf("expected refused/unknown_reference, got approved=%v reason=%q", approved, reason)
	}
}

func TestAuthorizeTransferRefusesAmountMismatch(t *testing.T) {
	repo := newFakeBaasRepo()
	svc := NewBaasService(repo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	repo.intents["ref1"] = &wallet.TransferIntent{
		ExternalReference: "ref1", Status: wallet.IntentAwaitingAuthorization, Amount: 1000, Destination: "wallet_parent",
	}

	approved, reason := svc.AuthorizeTransfer(context.Background(), "ref1", 999, "wallet_parent")
	if approved || reason != "mismatch" {
		t.Fatalf("expected refused/mismatch, got approved=%v reason=%q", approved, reason)
	}
}

func TestAuthorizeTransferRefusesDestinationMismatch(t *testing.T) {
	repo := newFakeBaasRepo()
	svc := NewBaasService(repo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	repo.intents["ref1"] = &wallet.TransferIntent{
		ExternalReference: "ref1", Status: wallet.IntentAwaitingAuthorization, Amount: 1000, Destination: "wallet_parent",
	}

	approved, reason := svc.AuthorizeTransfer(context.Background(), "ref1", 1000, "wallet_someone_else")
	if approved || reason != "mismatch" {
		t.Fatalf("expected refused/mismatch, got approved=%v reason=%q", approved, reason)
	}
}

func TestAuthorizeTransferRefusesMissingDestination(t *testing.T) {
	repo := newFakeBaasRepo()
	svc := NewBaasService(repo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	repo.intents["ref1"] = &wallet.TransferIntent{
		ExternalReference: "ref1", Status: wallet.IntentAwaitingAuthorization, Amount: 1000, Destination: "wallet_parent",
	}

	approved, reason := svc.AuthorizeTransfer(context.Background(), "ref1", 1000, "")
	if approved || reason != "mismatch" {
		t.Fatalf("expected refused/mismatch, got approved=%v reason=%q", approved, reason)
	}
}

func TestSubmitTransferWritesIntentAndCallsCreateTransfer(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	svc := NewBaasService(repo, fakeWallets{}, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	req := asaas.TransferRequest{Value: 500, WalletID: "wallet_parent", ExternalReference: "sbxg#abc"}
	if err := svc.SubmitTransfer(context.Background(), wallet.IntentKindSandboxPurchaseSettle, "u1", "apikey", req, "credit-sk-1"); err != nil {
		t.Fatalf("SubmitTransfer: %v", err)
	}
	if len(fake.CreatedTransfers) != 1 {
		t.Fatalf("expected 1 CreateTransfer call, got %d", len(fake.CreatedTransfers))
	}
	intent := repo.intents["sbxg#abc"]
	if intent == nil {
		t.Fatal("expected transfer intent to be written")
	}
	if intent.Amount != 500 || intent.Destination != "wallet_parent" || intent.Ref != "credit-sk-1" {
		t.Fatalf("unexpected intent: %+v", intent)
	}

	// Replay with the same ExternalReference must never re-submit (Invariant #3).
	if err := svc.SubmitTransfer(context.Background(), wallet.IntentKindSandboxPurchaseSettle, "u1", "apikey", req, "credit-sk-1"); err != nil {
		t.Fatalf("SubmitTransfer replay: %v", err)
	}
	if len(fake.CreatedTransfers) != 1 {
		t.Fatalf("expected replay to skip CreateTransfer, got %d total calls", len(fake.CreatedTransfers))
	}
}

// walletsWithBalances is a WalletReader stub reporting fixed
// real/game balances and open holds — for conservation-check tests.
type walletsWithBalances struct {
	real, game *wallet.Wallet
	holds      []wallet.Hold
	updated    []string
}

func (w *walletsWithBalances) EnsureRealWallet(context.Context, string) (*wallet.Wallet, error) {
	return w.real, nil
}
func (w *walletsWithBalances) LoadWallets(context.Context, string) (*wallet.Wallet, *wallet.Wallet, *wallet.Wallet, error) {
	return w.real, w.game, nil, nil
}
func (w *walletsWithBalances) ListOpenHoldsForWallet(context.Context, string, int) ([]wallet.Hold, error) {
	return w.holds, nil
}
func (w *walletsWithBalances) UpdateWithdrawal(_ context.Context, withdrawalID string, _ map[string]any) error {
	w.updated = append(w.updated, withdrawalID)
	return nil
}

func TestCheckConservationZeroDriftWhenBalancesMatch(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	repo.accounts["u1"] = &wallet.BaasAccount{
		UserID: "u1", Status: wallet.BaasApproved,
	}
	repo.accounts["u1"].APIKeyCiphertext, repo.accounts["u1"].APIKeyNonce, _ = asaas.EncryptAPIKey(make([]byte, 32), "apikey1")
	fake.StageBalance("apikey1", 15000)
	wallets := &walletsWithBalances{
		real:  &wallet.Wallet{WalletID: "w-real", Balance: 10000},
		game:  &wallet.Wallet{WalletID: "w-game", Balance: 4000},
		holds: []wallet.Hold{{WalletID: "w-game", Amount: 1000}},
	}
	svc := NewBaasService(repo, wallets, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	drift, err := svc.CheckConservation(context.Background(), "u1")
	if err != nil {
		t.Fatalf("CheckConservation: %v", err)
	}
	if drift != 0 {
		t.Fatalf("expected zero drift (10000+4000+1000 == 15000), got %d", drift)
	}
}

func TestInitiateOnboardingReservesBeforeProviderCall(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	fake.CreateAccountErr = errors.New("ambiguous provider timeout")
	svc := NewBaasService(repo, &walletsWithBalances{}, fake, nil,
		&stubKYC{rec: &kycclient.KYC{CPF: "12345678901", LegalName: "User"}},
		make([]byte, 32), "wallet_parent", "parent-apikey")

	if _, err := svc.InitiateOnboarding(context.Background(), "u1", wallet.KYCVerified, 1000); err == nil {
		t.Fatal("expected provider failure")
	}
	row := repo.accounts["u1"]
	if row == nil || row.Status != wallet.BaasOnboarding {
		t.Fatalf("durable reservation missing: %+v", row)
	}
	if _, err := svc.InitiateOnboarding(context.Background(), "u1", wallet.KYCVerified, 1000); err != nil {
		t.Fatalf("replay should return reservation: %v", err)
	}
	if len(fake.CreatedAccounts) != 1 {
		t.Fatalf("ambiguous retry created %d provider accounts", len(fake.CreatedAccounts))
	}
}

func TestCheckConservationNonZeroDriftWhenMismatched(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	repo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved}
	repo.accounts["u1"].APIKeyCiphertext, repo.accounts["u1"].APIKeyNonce, _ = asaas.EncryptAPIKey(make([]byte, 32), "apikey1")
	fake.StageBalance("apikey1", 5000) // Asaas reports far less than the ledger
	wallets := &walletsWithBalances{real: &wallet.Wallet{Balance: 10000}}
	svc := NewBaasService(repo, wallets, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	drift, err := svc.CheckConservation(context.Background(), "u1")
	if err != nil {
		t.Fatalf("CheckConservation: %v", err)
	}
	if drift != 5000 {
		t.Fatalf("expected drift 5000 (10000 ledger - 5000 asaas), got %d", drift)
	}
}

func TestCheckConservationTriviallyZeroForNonApprovedUser(t *testing.T) {
	repo := newFakeBaasRepo()
	svc := NewBaasService(repo, &walletsWithBalances{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	drift, err := svc.CheckConservation(context.Background(), "no-such-user")
	if err != nil || drift != 0 {
		t.Fatalf("expected zero drift / no error for a non-custodied user, got drift=%d err=%v", drift, err)
	}
}

func TestRunConservationCheckSetsAndClearsDriftFlag(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	repo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved}
	repo.accounts["u1"].APIKeyCiphertext, repo.accounts["u1"].APIKeyNonce, _ = asaas.EncryptAPIKey(make([]byte, 32), "apikey1")
	fake.StageBalance("apikey1", 5000)
	wallets := &walletsWithBalances{real: &wallet.Wallet{Balance: 10000}}
	svc := NewBaasService(repo, wallets, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	checked, drifted, err := svc.RunConservationCheck(context.Background())
	if err != nil {
		t.Fatalf("RunConservationCheck: %v", err)
	}
	if checked != 1 || drifted != 1 {
		t.Fatalf("expected checked=1 drifted=1, got checked=%d drifted=%d", checked, drifted)
	}
	if !repo.accounts["u1"].ConservationDrift {
		t.Fatal("expected ConservationDrift to be set")
	}

	// Balances converge — the flag must clear on the next run.
	fake.StageBalance("apikey1", 10000)
	checked, drifted, err = svc.RunConservationCheck(context.Background())
	if err != nil {
		t.Fatalf("RunConservationCheck (2nd run): %v", err)
	}
	if checked != 1 || drifted != 0 {
		t.Fatalf("expected checked=1 drifted=0 after convergence, got checked=%d drifted=%d", checked, drifted)
	}
	if repo.accounts["u1"].ConservationDrift {
		t.Fatal("expected ConservationDrift to be cleared after convergence")
	}
}

func TestHoldGameBlockedByConservationDrift(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	baas := &fakeDepositBaas{approved: true, conservationDrift: true}
	svc.SetBaas(baas)
	svc.SetCustodyEnabled(true)

	_, err := svc.HoldGame(context.Background(), "u1", 1000, "table-1", "idem-hold-drift")
	p, ok := errors.AsType[*problem.Problem](err)
	if !ok || p.Type != problem.TypeAccountBlocked {
		t.Fatalf("expected account-blocked problem, got %v", err)
	}
}

func TestMoneyOutPathsBlockedWhenFrozen(t *testing.T) {
	baas := &fakeDepositBaas{approved: false, status: wallet.BaasFrozen}

	t.Run("Withdraw", func(t *testing.T) {
		repo := newStubRepo()
		svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "12345678900"}})
		svc.SetBaas(baas)
		svc.SetCustodyEnabled(true)
		_, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 5000, "idem-frozen-w")
		p, ok := errors.AsType[*problem.Problem](err)
		if !ok || p.Type != problem.TypeAccountBlocked {
			t.Fatalf("expected account-blocked, got %v", err)
		}
	})

	t.Run("CashoutGame", func(t *testing.T) {
		repo := newStubRepo()
		svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
		svc.SetBaas(baas)
		svc.SetCustodyEnabled(true)
		_, err := svc.CashoutGame(context.Background(), "u1", 1000, "table-1", nil, "idem-frozen-c")
		p, ok := errors.AsType[*problem.Problem](err)
		if !ok || p.Type != problem.TypeAccountBlocked {
			t.Fatalf("expected account-blocked, got %v", err)
		}
	})

	t.Run("ringTransfer via ReturnFromGame", func(t *testing.T) {
		repo := newStubRepo()
		svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
		svc.SetBaas(baas)
		svc.SetCustodyEnabled(true)
		_, _, err := svc.ReturnFromGame(context.Background(), "u1", 1000, "idem-frozen-r")
		p, ok := errors.AsType[*problem.Problem](err)
		if !ok || p.Type != problem.TypeAccountBlocked {
			t.Fatalf("expected account-blocked, got %v", err)
		}
	})
}

func TestReconcileTransferIntentsMarksDoneAndCompletesWithdrawal(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	repo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved}
	repo.accounts["u1"].APIKeyCiphertext, repo.accounts["u1"].APIKeyNonce, _ = asaas.EncryptAPIKey(make([]byte, 32), "apikey1")
	repo.intents["withdraw#u1#idem1#payout"] = &wallet.TransferIntent{
		ExternalReference: "withdraw#u1#idem1#payout", Kind: wallet.IntentKindWithdrawalPayout,
		Status: wallet.IntentAwaitingAuthorization, UserID: "u1", Amount: 5000, Ref: "withdraw#u1#idem1",
	}
	fake.Transfers["withdraw#u1#idem1#payout"] = &asaas.Transfer{ID: "t1", Status: asaas.TransferDone, ExternalReference: "withdraw#u1#idem1#payout"}
	wallets := &walletsWithBalances{}
	svc := NewBaasService(repo, wallets, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	resolved, retried, alarmed, err := svc.ReconcileTransferIntents(context.Background())
	if err != nil {
		t.Fatalf("ReconcileTransferIntents: %v", err)
	}
	if resolved != 1 || retried != 0 || alarmed != 0 {
		t.Fatalf("expected resolved=1 retried=0 alarmed=0, got resolved=%d retried=%d alarmed=%d", resolved, retried, alarmed)
	}
	if repo.intents["withdraw#u1#idem1#payout"].Status != wallet.IntentDone {
		t.Fatalf("expected intent status done, got %q", repo.intents["withdraw#u1#idem1#payout"].Status)
	}
	if len(wallets.updated) != 1 || wallets.updated[0] != "withdraw#u1#idem1" {
		t.Fatalf("expected withdrawal to be marked completed, got %v", wallets.updated)
	}
}

func TestReconcileTransferIntentsResubmitsUnknownTransfer(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	repo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved}
	repo.accounts["u1"].APIKeyCiphertext, repo.accounts["u1"].APIKeyNonce, _ = asaas.EncryptAPIKey(make([]byte, 32), "apikey1")
	repo.intents["sbxg#idem1"] = &wallet.TransferIntent{
		ExternalReference: "sbxg#idem1", Kind: wallet.IntentKindSandboxPurchaseSettle,
		Status: wallet.IntentAwaitingAuthorization, UserID: "u1", Amount: 500, Destination: "wallet_parent", DestinationType: wallet.TransferDestinationWallet,
	}
	// Never staged in fake.Transfers — QueryTransfer will report "not found".
	svc := NewBaasService(repo, &walletsWithBalances{}, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	resolved, retried, alarmed, err := svc.ReconcileTransferIntents(context.Background())
	if err != nil {
		t.Fatalf("ReconcileTransferIntents: %v", err)
	}
	if resolved != 0 || retried != 1 || alarmed != 0 {
		t.Fatalf("expected resolved=0 retried=1 alarmed=0, got resolved=%d retried=%d alarmed=%d", resolved, retried, alarmed)
	}
	if len(fake.CreatedTransfers) != 1 {
		t.Fatalf("expected 1 resubmission CreateTransfer call, got %d", len(fake.CreatedTransfers))
	}
}

func TestReconcileTransferIntentsNeverResubmitsAfterAmbiguousQueryFailure(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	fake.QueryTransferErr = errors.New("provider timeout")
	repo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved}
	repo.accounts["u1"].APIKeyCiphertext, repo.accounts["u1"].APIKeyNonce, _ = asaas.EncryptAPIKey(make([]byte, 32), "apikey1")
	repo.intents["withdraw#u1#idem1#payout"] = &wallet.TransferIntent{
		ExternalReference: "withdraw#u1#idem1#payout", Kind: wallet.IntentKindWithdrawalPayout,
		Status: wallet.IntentAwaitingAuthorization, UserID: "u1", Amount: 5000,
		Destination: "12345678901", DestinationType: wallet.TransferDestinationPIX,
	}
	svc := NewBaasService(repo, &walletsWithBalances{}, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")

	resolved, retried, alarmed, err := svc.ReconcileTransferIntents(context.Background())
	if err != nil {
		t.Fatalf("ReconcileTransferIntents: %v", err)
	}
	if resolved != 0 || retried != 0 || alarmed != 1 {
		t.Fatalf("expected fail-closed alarm, got resolved=%d retried=%d alarmed=%d", resolved, retried, alarmed)
	}
	if len(fake.CreatedTransfers) != 0 {
		t.Fatalf("ambiguous query failure resubmitted %d transfer(s)", len(fake.CreatedTransfers))
	}
}

func TestReconcileFailedAsaasWithdrawalReversesLedgerDebit(t *testing.T) {
	repo := newFakeBaasRepo()
	fake := asaas.NewFake()
	repo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved}
	repo.accounts["u1"].APIKeyCiphertext, repo.accounts["u1"].APIKeyNonce, _ = asaas.EncryptAPIKey(make([]byte, 32), "apikey1")
	repo.intents["withdraw#u1#idem1#payout"] = &wallet.TransferIntent{
		ExternalReference: "withdraw#u1#idem1#payout", Kind: wallet.IntentKindWithdrawalPayout,
		Status: wallet.IntentProcessing, UserID: "u1", Amount: 5000, Ref: "withdraw#u1#idem1",
		Destination: "12345678901", DestinationType: wallet.TransferDestinationPIX,
	}
	fake.StageTransferStatus("withdraw#u1#idem1#payout", asaas.TransferFailed)
	svc := NewBaasService(repo, &walletsWithBalances{}, fake, nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	var reversed string
	svc.SetWithdrawalReverser(func(_ context.Context, id string) error { reversed = id; return nil })

	_, _, alarmed, err := svc.ReconcileTransferIntents(context.Background())
	if err != nil || alarmed != 0 {
		t.Fatalf("reconcile failed: alarmed=%d err=%v", alarmed, err)
	}
	if reversed != "withdraw#u1#idem1" {
		t.Fatalf("reversed %q", reversed)
	}
	if repo.intents["withdraw#u1#idem1#payout"].Status != wallet.IntentFailed {
		t.Fatalf("intent not terminal")
	}
}

func TestProcessMedClawbackCleanDebitWhenSufficientBalance(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	baasRepo := newFakeBaasRepo()
	baasRepo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved, ProviderAccountID: "acc_1"}
	baasSvc := NewBaasService(baasRepo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	svc.SetBaas(baasSvc)
	svc.SetCustodyEnabled(true)
	repo.real.Balance = 10000

	if err := svc.ProcessMedClawback(context.Background(), "acc_1", 3000, "med-evt-1"); err != nil {
		t.Fatalf("ProcessMedClawback: %v", err)
	}
	if len(repo.debitCalls) != 1 || repo.debitCalls[0].Amount != 3000 {
		t.Fatalf("expected a single 3000 debit, got %+v", repo.debitCalls)
	}
	if len(baasRepo.receivables) != 0 {
		t.Fatalf("expected no receivable when balance covers the clawback, got %d", len(baasRepo.receivables))
	}
}

func TestProcessMedClawbackCreatesReceivableForShortfall(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	baasRepo := newFakeBaasRepo()
	baasRepo.accounts["u1"] = &wallet.BaasAccount{UserID: "u1", Status: wallet.BaasApproved, ProviderAccountID: "acc_1"}
	baasSvc := NewBaasService(baasRepo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	svc.SetBaas(baasSvc)
	svc.SetCustodyEnabled(true)
	repo.real.Balance = 1000 // less than the 3000 clawback

	if err := svc.ProcessMedClawback(context.Background(), "acc_1", 3000, "med-evt-2"); err != nil {
		t.Fatalf("ProcessMedClawback: %v", err)
	}
	if len(repo.debitCalls) != 1 || repo.debitCalls[0].Amount != 1000 {
		t.Fatalf("expected a debit of exactly the available 1000, got %+v", repo.debitCalls)
	}
	recv := baasRepo.receivables["med-recv#med-evt-2"]
	if recv == nil {
		t.Fatal("expected a receivable to be created")
	}
	if recv.Amount != 2000 || recv.Status != wallet.MedReceivableOpen {
		t.Fatalf("unexpected receivable: %+v", recv)
	}
}

func TestProcessMedClawbackUnknownAccountIsNoOp(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	baasRepo := newFakeBaasRepo()
	baasSvc := NewBaasService(baasRepo, fakeWallets{}, asaas.NewFake(), nil, nil, make([]byte, 32), "wallet_parent", "parent-apikey")
	svc.SetBaas(baasSvc)
	svc.SetCustodyEnabled(true)

	if err := svc.ProcessMedClawback(context.Background(), "unknown-acc", 3000, "med-evt-3"); err != nil {
		t.Fatalf("expected idempotent no-op, got %v", err)
	}
	if len(repo.debitCalls) != 0 {
		t.Fatalf("expected no debit for an unknown account, got %+v", repo.debitCalls)
	}
}

func TestInitiateDepositBlockedByOpenMedReceivable(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	baas := &fakeDepositBaas{approved: true, openMedReceivable: true}
	svc.SetBaas(baas)
	svc.SetCustodyEnabled(true)

	_, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 1000, "idem-med-dep")
	p, ok := errors.AsType[*problem.Problem](err)
	if !ok || p.Type != problem.TypeMedReceivableOpen {
		t.Fatalf("expected med-receivable-open problem, got %v", err)
	}
}

func TestWithdrawBlockedByOpenMedReceivable(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "12345678900"}})
	baas := &fakeDepositBaas{approved: true, openMedReceivable: true}
	svc.SetBaas(baas)
	svc.SetCustodyEnabled(true)

	_, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 5000, "idem-med-w")
	p, ok := errors.AsType[*problem.Problem](err)
	if !ok || p.Type != problem.TypeMedReceivableOpen {
		t.Fatalf("expected med-receivable-open problem, got %v", err)
	}
}
