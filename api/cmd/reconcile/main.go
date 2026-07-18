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

	awslambda "github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api/internal/awsclient"
	"gopkg.aoctech.app/api/internal/cache"
	"gopkg.aoctech.app/api/internal/config"
	"gopkg.aoctech.app/api/internal/kycclient"
	"gopkg.aoctech.app/api/internal/lock"
	"gopkg.aoctech.app/api/internal/pix"
	"gopkg.aoctech.app/api/internal/repositories"
	"gopkg.aoctech.app/api/internal/services"
	"gopkg.aoctech.app/api/internal/ws"
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
		awslambda.Start(handler)
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
	pixClient, err := newPix(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pix: %w", err)
	}

	repo := repositories.NewWalletRepository(clients.DynamoDB, cfg)
	users := repositories.NewUserRepository(clients.DynamoDB, cfg)
	audit := repositories.NewAuditRepository(clients.DynamoDB, cfg)
	svc := services.NewWalletService(repo, users, audit, lock.NewLocker(cache.NewMemoryBackend(16)), pixClient, kycclient.New(cfg))
	svc.SetBroadcaster(newBroadcaster(cfg))

	resolved, reversed, alarmed, err := svc.ReconcileWithdrawals(ctx)
	if err != nil {
		return nil, err
	}
	return &Result{Resolved: resolved, Reversed: reversed, Alarmed: alarmed}, nil
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

// newPix builds api's PixClient the same way cmd/server does — by invoking
// pix-gateway's outbound Lambda. Reconciliation's QueryTransfer call is one of
// the 7 ops that Lambda multiplexes; reconcile no longer talks to Inter
// directly, same as the rest of api (see
// docs/specs/2026-07-13-pix-gateway-lambda-design.md).
func newPix(ctx context.Context, cfg *config.Config) (pix.PixClient, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	lc := lambda.NewFromConfig(awsCfg)
	// Reconcile is single-process; an in-memory cache+locker guard token refresh.
	memCache := cache.NewMemoryBackend(16)
	mgr := pix.NewInterTokenManager(lc, cfg, lock.NewLocker(memCache), memCache)
	return pix.NewLambdaPixClient(lc, cfg.PixGatewayFunctionName, mgr), nil
}
