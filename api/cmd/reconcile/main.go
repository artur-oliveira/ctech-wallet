// Command reconcile resolves withdrawals stuck in the processing state and deposits whose
// payment notice never arrived. It runs as a scheduled Lambda (EventBridge Scheduler) in
// deployed environments, and as a one-shot CLI locally.
//
// It asks Billing whether each processing payout actually went through, then completes or
// reverses it, alarming on any reversal whose credit-back fails.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	_ "time/tzdata" // responsible-gambling windows need America/Sao_Paulo everywhere

	awslambda "github.com/aws/aws-lambda-go/lambda"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ws"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/billingclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	"gopkg.aoctech.app/wallet/api/internal/rail"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
	"gopkg.aoctech.app/wallet/api/internal/secrets"
	"gopkg.aoctech.app/wallet/api/internal/services"
)

// Result is what the Lambda returns (and what the CLI logs).
type Result struct {
	Resolved              int `json:"resolved"`
	Reversed              int `json:"reversed"`
	Alarmed               int `json:"alarmed"`
	SweptDeposits         int `json:"swept_deposits"`
	RetriedDepositRefunds int `json:"retried_deposit_refunds"`
	StaleHolds            int `json:"stale_holds_alarmed"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// AWS_LAMBDA_FUNCTION_NAME is set by the Lambda runtime; locally it is empty.
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		awslambda.Start(handler)
		return
	}

	res, err := run(context.Background())
	if err != nil {
		slog.Error("reconcile failed", "err", err)
		os.Exit(1)
	}
	slog.Info("reconcile complete", "resolved", res.Resolved, "reversed", res.Reversed, "alarmed", res.Alarmed,
		"swept_deposits", res.SweptDeposits, "stale_holds_alarmed", res.StaleHolds)
	if res.Alarmed > 0 || res.StaleHolds > 0 {
		os.Exit(3) // non-zero so the scheduler/alarm notices unresolved refunds/stale holds
	}
}

func handler(ctx context.Context) (*Result, error) {
	res, err := run(ctx)
	if err != nil {
		return nil, err
	}
	if res.Alarmed > 0 || res.StaleHolds > 0 {
		// Surface as a Lambda error so the schedule's failure alarm fires. The
		// affected withdrawals are already flagged refund_failed; stale holds are
		// never auto-resolved (Invariant #14) — both need manual reconciliation.
		return res, fmt.Errorf("reconcile: %d reversal(s), %d stale hold(s) need manual reconciliation",
			res.Alarmed, res.StaleHolds)
	}
	return res, nil
}

func run(ctx context.Context) (*Result, error) {
	cfg, err := config.LoadReconcile()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	clients, err := awsclient.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("aws: %w", err)
	}
	// A Lambda's environment never holds a SecureString, so the client secrets it
	// needs are read from SSM once per run (the role already allows these paths).
	store := secrets.NewStore(clients.SSM, cfg.Env)
	if cfg.WalletClientSecret == "" {
		// Not fatal: withdrawal reconciliation needs no KYC lookup, and failing the
		// whole run for a secret only the deposit sweep uses would trade a small
		// problem for a bigger one.
		secret, loadErr := store.Load(ctx, "wallet-client-secret")
		if loadErr != nil {
			slog.Warn("reconcile: wallet client secret unreadable, KYC lookups will fail this run", "err", loadErr)
		}
		cfg.WalletClientSecret = secret
	}
	if cfg.BillingClientSecret == "" {
		if cfg.BillingClientSecret, err = store.Load(ctx, "billing-client-secret"); err != nil {
			return nil, err
		}
		if cfg.BillingClientSecret == "" {
			return nil, fmt.Errorf("billing-client-secret is not in SSM")
		}
	}

	repo := repositories.NewWalletRepository(clients.DynamoDB, cfg)
	users := repositories.NewUserRepository(clients.DynamoDB, cfg)
	audit := repositories.NewAuditRepository(clients.DynamoDB, cfg)
	svc := services.NewWalletService(repo, users, audit, lock.NewLocker(cache.NewMemoryBackend(16)), rail.NewBilling(billingclient.New(cfg, nil)), kycclient.New(cfg, nil))
	svc.SetBroadcaster(newBroadcaster(cfg))
	svc.SetSandboxPurchases(repositories.NewSandboxPurchaseRepository(clients.DynamoDB, cfg))
	svc.SetProductPurchases(repositories.NewProductPurchaseRepository(clients.DynamoDB, cfg))
	svc.SetAlerts(services.NewAlerts(ctx, cfg.AWSRegion, cfg.AlertsTopicARN, cfg.Env))

	resolved, reversed, alarmed, err := svc.ReconcileWithdrawals(ctx)
	if err != nil {
		return nil, err
	}
	swept, err := svc.SweepPendingDeposits(ctx)
	if err != nil {
		return nil, err
	}
	retriedDepositRefunds, err := svc.SweepDepositRefunds(ctx)
	if err != nil {
		return nil, err
	}
	staleHolds, err := svc.SweepStaleHolds(ctx)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Resolved: resolved, Reversed: reversed, Alarmed: alarmed, SweptDeposits: swept,
		RetriedDepositRefunds: retriedDepositRefunds, StaleHolds: staleHolds,
	}
	return res, nil
}

// newBroadcaster builds a publish-only WebSocket broadcaster so reconciliation
// outcomes (withdraw_completed/withdraw_reversed/withdraw_refund_failed) still
// reach the user even though this one-shot process never holds a WebSocket
// connection itself — Redis Pub/Sub fans the message out to whichever API
// instance does. Without Redis configured there is no cross-process delivery
// mechanism, so it returns nil — a safe no-op per SetBroadcaster's contract.
func newBroadcaster(cfg *config.Config) services.Broadcaster {
	if cfg.RedisURL == "" {
		return nil
	}
	rb, err := cache.NewRedisBackend(cfg.RedisURL)
	if err != nil {
		slog.Warn("reconcile: redis connection failed, withdrawal broadcasts disabled", "err", err)
		return nil
	}
	return ws.NewRedisRegistry(rb.Client())
}
