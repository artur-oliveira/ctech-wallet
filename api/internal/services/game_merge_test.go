package services

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// mergeRepo is an in-memory repository for the merge: several users, real
// transfers that move balances, idempotency by key, and retirement.
type mergeRepo struct {
	*stubRepo
	wallets    map[string]*wallet.Wallet // by wallet id
	byUser     map[string]map[string]*wallet.Wallet
	entries    []wallet.LedgerEntry
	idem       map[string]bool
	loseACent  string // user whose credit comes up one cent short
	transferTo []string
}

type mergeUser struct {
	ID         string
	Real, Game int64
	NoGame     bool
}

func newMergeRepo(users ...mergeUser) *mergeRepo {
	r := &mergeRepo{stubRepo: newStubRepo(), wallets: map[string]*wallet.Wallet{}, byUser: map[string]map[string]*wallet.Wallet{}, idem: map[string]bool{}}
	for _, u := range users {
		set := map[string]*wallet.Wallet{}
		add := func(typ string, bal int64) {
			w := &wallet.Wallet{WalletID: "w-" + typ + "-" + u.ID, UserID: u.ID, Type: typ, Balance: bal}
			r.wallets[w.WalletID] = w
			set[typ] = w
		}
		add(wallet.TypeReal, u.Real)
		if !u.NoGame {
			add(wallet.TypeGame, u.Game)
		}
		r.byUser[u.ID] = set
	}
	return r
}

func (r *mergeRepo) GetWallet(_ context.Context, id string) (*wallet.Wallet, error) {
	if w := r.wallets[id]; w != nil {
		c := *w
		return &c, nil
	}
	return nil, nil
}
func (r *mergeRepo) EnsureRealWallet(_ context.Context, userID string) (*wallet.Wallet, error) {
	return r.byUser[userID][wallet.TypeReal], nil
}
func (r *mergeRepo) LoadWallets(_ context.Context, userID string) (*wallet.Wallet, *wallet.Wallet, *wallet.Wallet, error) {
	s := r.byUser[userID]
	cp := func(w *wallet.Wallet) *wallet.Wallet {
		if w == nil {
			return nil
		}
		c := *w
		return &c
	}
	return cp(s[wallet.TypeReal]), cp(s[wallet.TypeGame]), cp(s[wallet.TypeSandbox]), nil
}
func (r *mergeRepo) Transfer(_ context.Context, from, to string, amount, creditAmount int64, dt, ct, _, idemKey, _ string, _ ...types.TransactWriteItem) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error) {
	if r.idem[idemKey] {
		return &wallet.LedgerEntry{IdempotencyKey: idemKey}, nil, true, nil
	}
	f, t := r.wallets[from], r.wallets[to]
	if f.Balance < amount {
		return nil, nil, false, errors.New("insufficient")
	}
	if r.loseACent == f.UserID {
		creditAmount--
	}
	f.Balance -= amount
	t.Balance += creditAmount
	r.idem[idemKey] = true
	r.transferTo = append(r.transferTo, f.UserID)
	d := wallet.LedgerEntry{WalletID: from, Amount: -amount, Type: dt, IdempotencyKey: idemKey}
	c := wallet.LedgerEntry{WalletID: to, Amount: creditAmount, Type: ct, IdempotencyKey: idemKey}
	r.entries = append(r.entries, d, c)
	return &d, &c, false, nil
}
func (r *mergeRepo) ListWalletsOfType(_ context.Context, typ string, _ map[string]types.AttributeValue, _ int) (*repositories.Page[wallet.Wallet], error) {
	var out []wallet.Wallet
	for _, w := range r.wallets {
		if w.Type == typ {
			out = append(out, *w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return &repositories.Page[wallet.Wallet]{Items: out}, nil
}
func (r *mergeRepo) RetireWallet(_ context.Context, id string) (bool, error) {
	w := r.wallets[id]
	if w == nil || w.Balance != 0 || w.RetiredAt != "" {
		return false, nil
	}
	w.RetiredAt = "now"
	return true, nil
}
func (r *mergeRepo) UnretireWallet(_ context.Context, id string) error {
	r.wallets[id].RetiredAt = ""
	return nil
}
func (r *mergeRepo) Statement(_ context.Context, walletID string, _ int, _ map[string]types.AttributeValue) (*repositories.QueryResult, error) {
	res := &repositories.QueryResult{}
	for i := len(r.entries) - 1; i >= 0; i-- {
		if r.entries[i].WalletID == walletID {
			item, err := repositories.Encode(r.entries[i])
			if err != nil {
				return nil, err
			}
			res.Items = append(res.Items, item)
		}
	}
	return res, nil
}
func (r *mergeRepo) ListOpenHoldsForWallet(ctx context.Context, id string, n int) ([]wallet.Hold, error) {
	return r.stubRepo.ListOpenHoldsForWallet(ctx, id, n)
}
func (r *mergeRepo) bal(user, typ string) int64 { return r.byUser[user][typ].Balance }
func (r *mergeRepo) entriesOf(typ string) int {
	n := 0
	for _, e := range r.entries {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func newMergeSvc(r *mergeRepo) (*WalletService, *recordingLocker) {
	lock := &recordingLocker{acquired: true, release: func() {}}
	return NewWalletService(r, &stubUserRepo{}, &stubAudit{}, lock, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}}), lock
}

func TestMergeMovesTheWholeGameBalanceToReal(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 2500, Game: 1000})
	s, lock := newMergeSvc(r)
	res, moved, err := s.MergeGameIntoReal(context.Background(), "u1")
	if err != nil || res != MergeMerged || moved != 1000 {
		t.Fatalf("res=%v moved=%d err=%v", res, moved, err)
	}
	if r.bal("u1", wallet.TypeReal) != 3500 || r.bal("u1", wallet.TypeGame) != 0 || r.byUser["u1"][wallet.TypeGame].RetiredAt == "" {
		t.Fatalf("real=%d game=%d retired=%q", r.bal("u1", wallet.TypeReal), r.bal("u1", wallet.TypeGame), r.byUser["u1"][wallet.TypeGame].RetiredAt)
	}
	if r.entriesOf(wallet.EntryGameMergeDebit) != 1 || r.entriesOf(wallet.EntryGameMergeCredit) != 1 {
		t.Fatalf("entries: %+v", r.entries)
	}
	// Both wallets are locked together, in the one ordered acquisition, so a deposit
	// landing on real waits for the merge and both amounts survive.
	if lock.orderedCalls != 1 || len(lock.orderedIDs) != 2 {
		t.Fatalf("ordered lock calls=%d ids=%v", lock.orderedCalls, lock.orderedIDs)
	}
}

func TestMergeTwiceIsANoopTheSecondTime(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 100, Game: 400})
	s, _ := newMergeSvc(r)
	_, _, _ = s.MergeGameIntoReal(context.Background(), "u1")
	res, moved, err := s.MergeGameIntoReal(context.Background(), "u1")
	if err != nil || res != MergeNoop || moved != 0 || r.bal("u1", wallet.TypeReal) != 500 || r.entriesOf(wallet.EntryGameMergeCredit) != 1 {
		t.Fatalf("res=%v moved=%d real=%d credits=%d err=%v", res, moved, r.bal("u1", wallet.TypeReal), r.entriesOf(wallet.EntryGameMergeCredit), err)
	}
}

func TestMergeSkipsAUserWithAnOpenHold(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 0, Game: 900})
	r.holds["h1"] = &wallet.Hold{HoldID: "h1", WalletID: "w-game-u1", UserID: "u1", Amount: 100, Status: wallet.HoldHeld}
	s, _ := newMergeSvc(r)
	res, moved, err := s.MergeGameIntoReal(context.Background(), "u1")
	if err != nil || res != MergeSkippedOpenHold || moved != 0 || r.bal("u1", wallet.TypeGame) != 900 || len(r.entries) != 0 {
		t.Fatalf("res=%v moved=%d game=%d err=%v", res, moved, r.bal("u1", wallet.TypeGame), err)
	}
}

func TestMergeZeroBalanceWritesNoEntriesAndRetires(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 50, Game: 0})
	s, _ := newMergeSvc(r)
	res, _, err := s.MergeGameIntoReal(context.Background(), "u1")
	if err != nil || res != MergeNoop || len(r.entries) != 0 || r.byUser["u1"][wallet.TypeGame].RetiredAt == "" {
		t.Fatalf("res=%v entries=%d err=%v", res, len(r.entries), err)
	}
}

func TestMergeUserWithoutAGameWalletIsANoop(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 50, NoGame: true})
	s, _ := newMergeSvc(r)
	res, _, err := s.MergeGameIntoReal(context.Background(), "u1")
	if err != nil || res != MergeNoop || len(r.entries) != 0 {
		t.Fatalf("res=%v entries=%d err=%v", res, len(r.entries), err)
	}
}

func TestPreflightCountsWhatAMergeWouldDo(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 10, Game: 1000}, mergeUser{ID: "u2", Real: 20, Game: 500}, mergeUser{ID: "u3", Real: 30, NoGame: true})
	r.holds["h1"] = &wallet.Hold{HoldID: "h1", WalletID: "w-game-u1", UserID: "u1", Amount: 1, Status: wallet.HoldHeld}
	s, _ := newMergeSvc(r)
	rep, err := s.PreflightGameMerge(context.Background())
	if err != nil || rep.Users != 2 || rep.WithGameBalance != 2 || rep.OpenHolds != 1 || rep.TotalGameCents != 1500 || rep.TotalRealCents != 30 {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
}

func TestRunMergesEveryoneAndCountsAddUp(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 100, Game: 1000}, mergeUser{ID: "u2", Real: 200, Game: 500},
		mergeUser{ID: "u3", Real: 300, Game: 0}, mergeUser{ID: "u4", Real: 400, Game: 700})
	r.holds["h4"] = &wallet.Hold{HoldID: "h4", WalletID: "w-game-u4", UserID: "u4", Amount: 1, Status: wallet.HoldHeld}
	s, _ := newMergeSvc(r)
	sum, err := s.RunGameMerge(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Merged != 2 || sum.Noop != 1 || sum.SkippedOpenHold != 1 || sum.Failed != 0 {
		t.Fatalf("summary=%+v", sum)
	}
	if sum.TotalRealBefore != 1000 || sum.TotalRealAfter != 2500 || sum.TotalGameBefore != 2200 {
		t.Fatalf("totals=%+v", sum)
	}
	var real int64
	for _, u := range []string{"u1", "u2", "u3", "u4"} {
		real += r.bal(u, wallet.TypeReal)
	}
	if real != sum.TotalRealAfter {
		t.Fatalf("real across users = %d, summary says %d", real, sum.TotalRealAfter)
	}
	// A second run finishes nothing twice: u4 is still held, the others are retired.
	again, err := s.RunGameMerge(context.Background(), 0)
	if err != nil || again.Merged != 0 || again.SkippedOpenHold != 1 || r.entriesOf(wallet.EntryGameMergeCredit) != 2 {
		t.Fatalf("again=%+v credits=%d err=%v", again, r.entriesOf(wallet.EntryGameMergeCredit), err)
	}
}

func TestRunStopsBeforeTheNextUserWhenATransferLosesACent(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 0, Game: 1000}, mergeUser{ID: "u2", Real: 0, Game: 500})
	r.loseACent = "u1"
	s, _ := newMergeSvc(r)
	_, err := s.RunGameMerge(context.Background(), 0)
	if !errors.Is(err, ErrMergeTotalsDiffer) {
		t.Fatalf("err=%v", err)
	}
	if len(r.transferTo) != 1 || r.bal("u2", wallet.TypeGame) != 500 {
		t.Fatalf("the run touched a second user: %v", r.transferTo)
	}
}

func TestUnmergeGivesBackWhatTheMergeMoved(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 2500, Game: 1000})
	s, _ := newMergeSvc(r)
	_, _, _ = s.MergeGameIntoReal(context.Background(), "u1")
	res, moved, err := s.UnmergeGameFromReal(context.Background(), "u1")
	if err != nil || res != MergeMerged || moved != 1000 {
		t.Fatalf("res=%v moved=%d err=%v", res, moved, err)
	}
	if r.bal("u1", wallet.TypeReal) != 2500 || r.bal("u1", wallet.TypeGame) != 1000 || r.byUser["u1"][wallet.TypeGame].RetiredAt != "" {
		t.Fatalf("real=%d game=%d retired=%q", r.bal("u1", wallet.TypeReal), r.bal("u1", wallet.TypeGame), r.byUser["u1"][wallet.TypeGame].RetiredAt)
	}
}

func TestUnmergeNeverForcesAUserWhoSpentTheMoney(t *testing.T) {
	r := newMergeRepo(mergeUser{ID: "u1", Real: 0, Game: 1000})
	s, _ := newMergeSvc(r)
	_, _, _ = s.MergeGameIntoReal(context.Background(), "u1")
	r.byUser["u1"][wallet.TypeReal].Balance = 300 // spent most of it
	res, moved, err := s.UnmergeGameFromReal(context.Background(), "u1")
	if err != nil || res != MergeSkippedSpent || moved != 0 || r.bal("u1", wallet.TypeReal) != 300 || r.bal("u1", wallet.TypeGame) != 0 {
		t.Fatalf("res=%v moved=%d real=%d game=%d err=%v", res, moved, r.bal("u1", wallet.TypeReal), r.bal("u1", wallet.TypeGame), err)
	}
}
