package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// GameMergeStore is what the one-off game→real merge needs beyond Repo. A
// separate interface: only the merge command and its tests reach for it.
type GameMergeStore interface {
	ListWalletsOfType(ctx context.Context, walletType string, start map[string]types.AttributeValue, limit int) (*repositories.Page[wallet.Wallet], error)
	RetireWallet(ctx context.Context, walletID string) (bool, error)
	UnretireWallet(ctx context.Context, walletID string) error
}

// MergeResult is what the merge did for one user.
type MergeResult string

const (
	MergeMerged          MergeResult = "merged"
	MergeNoop            MergeResult = "noop"
	MergeSkippedOpenHold MergeResult = "skipped_open_hold"
	MergeSkippedSpent    MergeResult = "skipped_spent" // rollback only: real no longer covers what was moved
)

// ErrMergeTotalsDiffer stops a run the moment a transfer did not move exactly
// what it debited: nothing further is touched until a person looks.
var ErrMergeTotalsDiffer = errors.New("game merge: the money moved is not the money debited")

const (
	mergePageSize    = 100
	mergeOpenHoldCap = 1000
)

func (s *WalletService) mergeStore() (GameMergeStore, error) {
	m, ok := s.repo.(GameMergeStore)
	if !ok {
		return nil, errors.New("game merge: the repository cannot list wallets by type")
	}
	return m, nil
}

// MergeReport is the read-only picture before a merge.
type MergeReport struct {
	Users, WithGameBalance, ZeroBalance, OpenHolds int
	TotalGameCents, TotalRealCents                 int64 // before
	RealMoneyPokerEnabled                          bool  // set by the caller, who read Poker's config
}

// PreflightGameMerge counts what a merge would do without changing anything.
func (s *WalletService) PreflightGameMerge(ctx context.Context) (*MergeReport, error) {
	store, err := s.mergeStore()
	if err != nil {
		return nil, err
	}
	rep := &MergeReport{}
	err = forEachGameWallet(ctx, store, func(g wallet.Wallet) error {
		if g.RetiredAt != "" {
			return nil
		}
		rep.Users++
		if g.Balance > 0 {
			rep.WithGameBalance++
			rep.TotalGameCents += g.Balance
		} else {
			rep.ZeroBalance++
		}
		real, _, _, err := s.repo.LoadWallets(ctx, g.UserID)
		if err != nil {
			return err
		}
		if real != nil {
			rep.TotalRealCents += real.Balance
		}
		open, err := s.repo.ListOpenHoldsForWallet(ctx, g.WalletID, mergeOpenHoldCap)
		if err != nil {
			return err
		}
		if len(open) > 0 {
			rep.OpenHolds++
		}
		return nil
	})
	return rep, err
}

func forEachGameWallet(ctx context.Context, store GameMergeStore, fn func(wallet.Wallet) error) error {
	var start map[string]types.AttributeValue
	for {
		page, err := store.ListWalletsOfType(ctx, wallet.TypeGame, start, mergePageSize)
		if err != nil {
			return err
		}
		for _, w := range page.Items {
			if err := fn(w); err != nil {
				return err
			}
		}
		if len(page.LastEvaluatedKey) == 0 {
			return nil
		}
		start = page.LastEvaluatedKey
	}
}

func gameMergeKey(userID string) string { return "game-merge#" + userID }

// MergeGameIntoReal moves a user's whole game balance to their real wallet, with a
// ledger entry on each side, then retires the game wallet. Idempotent: a replay
// moves nothing. A user with an open hold is skipped, never merged: the hold
// settles against the game wallet and the merge is run again afterwards.
func (s *WalletService) MergeGameIntoReal(ctx context.Context, userID string) (MergeResult, int64, error) {
	store, err := s.mergeStore()
	if err != nil {
		return "", 0, err
	}
	real, game, _, err := s.repo.LoadWallets(ctx, userID)
	if err != nil {
		return "", 0, err
	}
	if game == nil || game.RetiredAt != "" {
		return MergeNoop, 0, nil
	}
	if real == nil {
		if real, err = s.repo.EnsureRealWallet(ctx, userID); err != nil {
			return "", 0, err
		}
	}
	if open, err := s.repo.ListOpenHoldsForWallet(ctx, game.WalletID, mergeOpenHoldCap); err != nil {
		return "", 0, err
	} else if len(open) > 0 {
		return MergeSkippedOpenHold, 0, nil
	}

	release, err := acquireWallets(ctx, s.lock, real.WalletID, game.WalletID)
	if err != nil {
		return "", 0, err
	}
	defer release()

	// Under the lock: no hold can open and no balance can move on the game wallet.
	if game, err = s.repo.GetWallet(ctx, game.WalletID); err != nil || game == nil {
		return "", 0, fmt.Errorf("game merge: reading the game wallet: %w", err)
	}
	if game.RetiredAt != "" {
		return MergeNoop, 0, nil
	}
	if open, err := s.repo.ListOpenHoldsForWallet(ctx, game.WalletID, mergeOpenHoldCap); err != nil {
		return "", 0, err
	} else if len(open) > 0 {
		return MergeSkippedOpenHold, 0, nil
	}

	var moved int64
	if game.Balance > 0 {
		key := gameMergeKey(userID)
		d, c, _, err := s.repo.Transfer(ctx, game.WalletID, real.WalletID, game.Balance, game.Balance,
			wallet.EntryGameMergeDebit, wallet.EntryGameMergeCredit, "game-merge", key, reqHash("game-merge", game.Balance))
		if err != nil {
			return "", 0, err
		}
		// A replay answers with the first entry only; nothing to verify then.
		if c != nil && (d.Amount != -game.Balance || c.Amount != game.Balance) {
			return "", 0, fmt.Errorf("%w: user %s debited %d, credited %d", ErrMergeTotalsDiffer, userID, -d.Amount, c.Amount)
		}
		moved = game.Balance
	}
	if _, err := store.RetireWallet(ctx, game.WalletID); err != nil {
		return "", 0, err
	}
	if moved == 0 {
		return MergeNoop, 0, nil
	}
	return MergeMerged, moved, nil
}

// RunSummary is the outcome of a whole run.
type RunSummary struct {
	Merged, Noop, SkippedOpenHold, Failed            int
	TotalGameBefore, TotalRealBefore, TotalRealAfter int64
}

// RunGameMerge merges every game wallet, one user at a time. limit > 0 stops
// after that many users (0 is all). It stops at once, with ErrMergeTotalsDiffer,
// when a transfer's two sides disagree; any other error is counted as Failed and
// the run goes on. Re-running finishes the rest and double-credits nobody.
func (s *WalletService) RunGameMerge(ctx context.Context, limit int) (*RunSummary, error) {
	store, err := s.mergeStore()
	if err != nil {
		return nil, err
	}
	sum := &RunSummary{}
	var movedSum int64
	seen := 0
	errStop := errors.New("limit reached")
	err = forEachGameWallet(ctx, store, func(g wallet.Wallet) error {
		if g.RetiredAt != "" {
			return nil
		}
		if limit > 0 && seen >= limit {
			return errStop
		}
		seen++
		real, _, _, err := s.repo.LoadWallets(ctx, g.UserID)
		if err != nil {
			sum.Failed++
			return nil
		}
		if real != nil {
			sum.TotalRealBefore += real.Balance
		}
		sum.TotalGameBefore += g.Balance
		res, moved, err := s.MergeGameIntoReal(ctx, g.UserID)
		switch {
		case errors.Is(err, ErrMergeTotalsDiffer):
			return err
		case err != nil:
			sum.Failed++
		case res == MergeMerged:
			sum.Merged++
			movedSum += moved
		case res == MergeSkippedOpenHold:
			sum.SkippedOpenHold++
		default:
			sum.Noop++
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return sum, err
	}
	sum.TotalRealAfter = sum.TotalRealBefore + movedSum
	return sum, nil
}

// UnmergeGameFromReal rolls one user's merge back: the amount that arrived on
// `real` goes back to a reopened `game` wallet, by counter-entries. A user whose
// `real` no longer covers it (they spent it) is reported, never forced. Run it
// with GAME_WALLET_MERGED off.
func (s *WalletService) UnmergeGameFromReal(ctx context.Context, userID string) (MergeResult, int64, error) {
	store, err := s.mergeStore()
	if err != nil {
		return "", 0, err
	}
	real, game, _, err := s.repo.LoadWallets(ctx, userID)
	if err != nil {
		return "", 0, err
	}
	if real == nil || game == nil {
		return MergeNoop, 0, nil
	}
	moved, err := s.mergedAmount(ctx, real.WalletID, userID)
	if err != nil {
		return "", 0, err
	}
	if moved == 0 {
		return MergeNoop, 0, nil
	}
	release, err := acquireWallets(ctx, s.lock, real.WalletID, game.WalletID)
	if err != nil {
		return "", 0, err
	}
	defer release()
	if real, err = s.repo.GetWallet(ctx, real.WalletID); err != nil || real == nil {
		return "", 0, fmt.Errorf("game unmerge: reading the real wallet: %w", err)
	}
	if real.Balance < moved {
		return MergeSkippedSpent, 0, nil
	}
	if _, _, _, err := s.repo.Transfer(ctx, real.WalletID, game.WalletID, moved, moved,
		wallet.EntryGameUnmergeDebit, wallet.EntryGameUnmergeCredit, "game-unmerge", "game-unmerge#"+userID, reqHash("game-unmerge", moved)); err != nil {
		return "", 0, err
	}
	if err := store.UnretireWallet(ctx, game.WalletID); err != nil {
		return "", 0, err
	}
	return MergeMerged, moved, nil
}

// mergedAmount reads what the merge credited to this user's real wallet, from
// its ledger (newest first, bounded pages). Zero when it never merged anything.
func (s *WalletService) mergedAmount(ctx context.Context, realWalletID, userID string) (int64, error) {
	var start map[string]types.AttributeValue
	for pages := 0; pages < 50; pages++ {
		res, err := s.repo.Statement(ctx, realWalletID, 100, start)
		if err != nil {
			return 0, err
		}
		for _, it := range res.Items {
			e, err := repositories.Decode[wallet.LedgerEntry](it)
			if err != nil {
				return 0, err
			}
			if e.IdempotencyKey == gameMergeKey(userID) && e.Type == wallet.EntryGameMergeCredit {
				return e.Amount, nil
			}
		}
		if len(res.LastEvaluatedKey) == 0 {
			return 0, nil
		}
		start = res.LastEvaluatedKey
	}
	return 0, nil
}

// RunGameUnmerge rolls every retired game wallet back; users who spent the money
// are counted as SkippedOpenHold-free "Failed"-free noops of their own kind.
func (s *WalletService) RunGameUnmerge(ctx context.Context) (merged, skippedSpent, noop int, err error) {
	store, err := s.mergeStore()
	if err != nil {
		return 0, 0, 0, err
	}
	err = forEachGameWallet(ctx, store, func(g wallet.Wallet) error {
		if g.RetiredAt == "" {
			return nil
		}
		res, _, uerr := s.UnmergeGameFromReal(ctx, g.UserID)
		if uerr != nil {
			return uerr
		}
		switch res {
		case MergeMerged:
			merged++
		case MergeSkippedSpent:
			skippedSpent++
		default:
			noop++
		}
		return nil
	})
	return merged, skippedSpent, noop, err
}
