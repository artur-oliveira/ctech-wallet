// Command reconcile resolves withdrawals stuck in the processing state. It runs
// as a scheduled Lambda (EventBridge Scheduler) in deployed environments, and as
// a one-shot CLI locally.
//
// It asks the bank whether each processing payout actually went through, then
// completes or reverses it, alarming on any reversal whose credit-back fails.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/artur-oliveira/ctech-wallet/api/internal/awsclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/cache"
	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
	"github.com/artur-oliveira/ctech-wallet/api/internal/kycclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/lock"
	"github.com/artur-oliveira/ctech-wallet/api/internal/pix"
	"github.com/artur-oliveira/ctech-wallet/api/internal/repositories"
	"github.com/artur-oliveira/ctech-wallet/api/internal/secrets"
	"github.com/artur-oliveira/ctech-wallet/api/internal/services"
)

// Result is what the Lambda returns (and what the CLI logs).
type Result struct {
	Resolved int `json:"resolved"`
	Reversed int `json:"reversed"`
	Alarmed  int `json:"alarmed"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// AWS_LAMBDA_FUNCTION_NAME is set by the Lambda runtime; locally it is empty.
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(handler)
		return
	}

	res, err := run(context.Background())
	if err != nil {
		slog.Error("reconcile failed", "err", err)
		os.Exit(1)
	}
	slog.Info("reconcile complete", "resolved", res.Resolved, "reversed", res.Reversed, "alarmed", res.Alarmed)
	if res.Alarmed > 0 {
		os.Exit(3) // non-zero so the scheduler/alarm notices unresolved refunds
	}
}

func handler(ctx context.Context) (*Result, error) {
	res, err := run(ctx)
	if err != nil {
		return nil, err
	}
	if res.Alarmed > 0 {
		// Surface as a Lambda error so the schedule's failure alarm fires. The
		// affected withdrawals are already flagged refund_failed for manual work.
		return res, fmt.Errorf("reconcile: %d reversal(s) failed and need manual reconciliation", res.Alarmed)
	}
	return res, nil
}

func run(ctx context.Context) (*Result, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	clients, err := awsclient.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("aws: %w", err)
	}
	pixClient, err := newPix(ctx, cfg, clients)
	if err != nil {
		return nil, fmt.Errorf("pix: %w", err)
	}

	repo := repositories.NewWalletRepository(clients.DynamoDB, cfg)
	users := repositories.NewUserRepository(clients.DynamoDB, cfg)
	audit := repositories.NewAuditRepository(clients.DynamoDB, cfg)
	svc := services.NewWalletService(repo, users, audit, lock.NewLocker(cache.NewMemoryBackend(16)), pixClient, kycclient.New(cfg))

	resolved, reversed, alarmed, err := svc.ReconcileWithdrawals(ctx)
	if err != nil {
		return nil, err
	}
	return &Result{Resolved: resolved, Reversed: reversed, Alarmed: alarmed}, nil
}

// newPix builds the real Inter client, reading the mTLS keypair AND the OAuth
// client secret from SSM — the Lambda has no start.sh to export env vars.
//
// FAIL CLOSED: outside local dev a missing credential is fatal. Falling back to
// the fake client here would be catastrophic: its QueryTransfer reports every
// payout as NAO_ENCONTRADO, so the job would reverse every processing withdrawal
// and credit back money the bank had in fact already sent.
func newPix(ctx context.Context, cfg *config.Config, clients *awsclient.Clients) (pix.PixClient, error) {
	if cfg.InterClientID == "" {
		if cfg.Env != "dev" {
			return nil, fmt.Errorf("INTER_CLIENT_ID is required in %s — refusing to reconcile with the fake PIX client", cfg.Env)
		}
		slog.Warn("reconcile: Inter credentials not set — using fake PIX client (local dev only)")
		return pix.NewFake(), nil
	}

	store := secrets.NewStore(clients.SSM, cfg.Env)
	kp, err := store.LoadInterMTLS(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.InterClientSecret == "" {
		s, err := store.LoadInterClientSecret(ctx)
		if err != nil {
			return nil, err
		}
		cfg.InterClientSecret = s
	}
	return pix.NewInterClient(cfg, kp)
}
