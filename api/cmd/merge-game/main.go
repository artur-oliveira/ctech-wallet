// Command merge-game moves every user's `game` balance into `real` (P3 of the
// wallet-ledger migration), then retires the game wallets. Run by an operator
// with the AWS profile of the environment:
//
//	TABLE_PREFIX=prod ENVIRONMENT=prod AWS_PROFILE=ctech go run ./cmd/merge-game --dry-run
//	... go run ./cmd/merge-game --poker-real-money-off
//	... go run ./cmd/merge-game --reverse            (rollback; GAME_WALLET_MERGED must be off)
//
// It refuses to merge unless the operator states, with --poker-real-money-off,
// that Poker's REAL_MONEY_ENABLED is off: while the flag is on and Poker plays
// real money, personal limits are not enforced (the metered edge is gone).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
	"gopkg.aoctech.app/wallet/api/internal/services"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	dry := flag.Bool("dry-run", false, "print the report and change nothing")
	pokerOff := flag.Bool("poker-real-money-off", false, "I checked: Poker's REAL_MONEY_ENABLED is off in this environment")
	reverse := flag.Bool("reverse", false, "roll the merge back for users whose real still covers it")
	limit := flag.Int("limit", 0, "stop after this many users (0 is all)")
	flag.Parse()
	if err := run(context.Background(), *dry, *pokerOff, *reverse, *limit); err != nil {
		slog.Error("merge-game failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dry, pokerOff, reverse bool, limit int) error {
	cfg, err := config.LoadReconcile()
	if err != nil {
		return err
	}
	clients, err := awsclient.New(ctx, cfg)
	if err != nil {
		return err
	}
	repo := repositories.NewWalletRepository(clients.DynamoDB, cfg)
	users := repositories.NewUserRepository(clients.DynamoDB, cfg)
	audit := repositories.NewAuditRepository(clients.DynamoDB, cfg)
	// The lock is local to this process; the balance writes are version-conditional,
	// so a deposit landing mid-merge fails that user's transfer (counted Failed) and
	// the next run takes it.
	svc := services.NewWalletService(repo, users, audit, lock.NewLocker(cache.NewMemoryBackend(16)), nil, nil)

	switch {
	case dry:
		rep, err := svc.PreflightGameMerge(ctx)
		if err != nil {
			return err
		}
		rep.RealMoneyPokerEnabled = !pokerOff
		return printJSON(rep)
	case reverse:
		merged, spent, noop, err := svc.RunGameUnmerge(ctx)
		if err != nil {
			return err
		}
		return printJSON(map[string]int{"reversed": merged, "skipped_spent": spent, "noop": noop})
	}
	if !pokerOff {
		return errors.New("refusing to merge: check that Poker's REAL_MONEY_ENABLED is off, then pass --poker-real-money-off")
	}
	sum, err := svc.RunGameMerge(ctx, limit)
	if sum != nil {
		_ = printJSON(sum)
	}
	if err != nil {
		return fmt.Errorf("stopped: %w", err)
	}
	if sum.Failed > 0 {
		return fmt.Errorf("%d user(s) failed; run again", sum.Failed)
	}
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
