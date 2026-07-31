// Command reconcile resolves withdrawals stuck in the processing state. It runs
// as a scheduled Lambda (EventBridge Scheduler) in deployed environments, and as
// a one-shot CLI locally.
//
// It asks the bank whether each processing payout actually went through, then
// completes or reverses it, alarming on any reversal whose credit-back fails.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	_ "time/tzdata" // responsible-gambling windows need America/Sao_Paulo everywhere

	awslambda "github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ws"
	"gopkg.aoctech.app/wallet/api/internal/asaas"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	"gopkg.aoctech.app/wallet/api/internal/pix"
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
	SweptSandboxPurchases int `json:"swept_sandbox_purchases"`
	RetriedSandboxRefunds int `json:"retried_sandbox_refunds"`
	RetriedM2MWebhooks    int `json:"retried_m2m_webhooks"`
	StaleHolds            int `json:"stale_holds_alarmed"`
	TransfersResolved     int `json:"asaas_transfers_resolved"`
	TransfersRetried      int `json:"asaas_transfers_retried"`
	TransfersAlarmed      int `json:"asaas_transfers_alarmed"`
	ConservationChecked   int `json:"conservation_checked"`
	ConservationDrifted   int `json:"conservation_drifted"`
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
		"swept_deposits", res.SweptDeposits, "retried_m2m_webhooks", res.RetriedM2MWebhooks, "stale_holds_alarmed", res.StaleHolds,
		"asaas_transfers_resolved", res.TransfersResolved, "asaas_transfers_retried", res.TransfersRetried,
		"asaas_transfers_alarmed", res.TransfersAlarmed, "conservation_checked", res.ConservationChecked,
		"conservation_drifted", res.ConservationDrifted)
	if res.Alarmed > 0 || res.StaleHolds > 0 || res.TransfersAlarmed > 0 || res.ConservationDrifted > 0 {
		os.Exit(3) // non-zero so the scheduler/alarm notices unresolved refunds/stale holds/drift
	}
}

func handler(ctx context.Context) (*Result, error) {
	res, err := run(ctx)
	if err != nil {
		return nil, err
	}
	if res.Alarmed > 0 || res.StaleHolds > 0 || res.TransfersAlarmed > 0 || res.ConservationDrifted > 0 {
		// Surface as a Lambda error so the schedule's failure alarm fires. The
		// affected withdrawals are already flagged refund_failed; stale holds and
		// conservation drift are never auto-resolved (Invariant #12, #13) — all
		// need manual reconciliation.
		return res, fmt.Errorf("reconcile: %d reversal(s), %d stale hold(s), %d asaas transfer alarm(s), %d conservation drift(s) need manual reconciliation",
			res.Alarmed, res.StaleHolds, res.TransfersAlarmed, res.ConservationDrifted)
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
	svc.SetSandboxPurchases(repositories.NewSandboxPurchaseRepository(clients.DynamoDB, cfg))
	m2mClients, err := newM2MClients(ctx, cfg, clients)
	if err != nil {
		return nil, err
	}
	svc.SetM2MClients(m2mClients)

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
	sweptSandbox, err := svc.SweepPendingSandboxPurchases(ctx)
	if err != nil {
		return nil, err
	}
	retriedSandboxRefunds, err := svc.SweepRefundPendingSandboxPurchases(ctx)
	if err != nil {
		return nil, err
	}
	retriedWebhooks, err := svc.RetryFailedM2MWebhooks(ctx)
	if err != nil {
		return nil, err
	}
	staleHolds, err := svc.SweepStaleHolds(ctx)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Resolved: resolved, Reversed: reversed, Alarmed: alarmed, SweptDeposits: swept,
		RetriedDepositRefunds: retriedDepositRefunds, SweptSandboxPurchases: sweptSandbox,
		RetriedSandboxRefunds: retriedSandboxRefunds, RetriedM2MWebhooks: retriedWebhooks, StaleHolds: staleHolds,
	}
	if cfg.AsaasCustodyEnabled {
		baasSvc, err := newBaasService(ctx, cfg, clients, repo, audit, kycclient.New(cfg))
		if err != nil {
			return nil, fmt.Errorf("baas: %w", err)
		}
		baasSvc.SetWithdrawalReverser(svc.ReverseWithdrawal)
		tResolved, tRetried, tAlarmed, err := baasSvc.ReconcileTransferIntents(ctx)
		if err != nil {
			return nil, err
		}
		checked, drifted, err := baasSvc.RunConservationCheck(ctx)
		if err != nil {
			return nil, err
		}
		res.TransfersResolved, res.TransfersRetried, res.TransfersAlarmed = tResolved, tRetried, tAlarmed
		res.ConservationChecked, res.ConservationDrifted = checked, drifted
	}
	return res, nil
}

// newM2MClients loads the M2M sandbox-purchase client registry the same way
// api's own app.go does (see app.newM2MClients) — reconcile is not wired
// through fx, so this is a plain function instead of an fx.Provide entry. An
// unset SSM parameter is a valid "no M2M client registered" state, not an error.
func newM2MClients(ctx context.Context, cfg *config.Config, clients *awsclient.Clients) (map[string]services.M2MClient, error) {
	store := secrets.NewStore(clients.SSM, cfg.Env)
	raw, err := store.LoadM2MClients(ctx)
	if err != nil {
		return nil, fmt.Errorf("m2m clients: %w", err)
	}
	m := map[string]services.M2MClient{}
	if raw == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("m2m clients: invalid json: %w", err)
	}
	return m, nil
}

// newBaasService wires the Asaas custody service for the reconcile job — same
// SSM-fetch-once shape as cmd/server's app.go, but constructed directly here
// since cmd/reconcile does not use the fx DI container. Uses the same real
// Lambda-backed AsaasClient as cmd/server (invokes pix-gateway's outbound
// Lambda) — safe to build unconditionally since this whole function is only
// ever called inside the `if cfg.AsaasCustodyEnabled` branch above.
func newBaasService(ctx context.Context, cfg *config.Config, clients *awsclient.Clients, repo *repositories.WalletRepository, audit *repositories.AuditRepository, kyc services.KYCClient) (*services.BaasService, error) {
	store := secrets.NewStore(clients.SSM, cfg.Env)
	hexKey, err := store.LoadAsaasMasterKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("asaas master key: %w", err)
	}
	masterKey, err := asaas.MasterKeyFromHex(hexKey)
	if err != nil {
		return nil, err
	}
	parentAPIKey, err := store.LoadAsaasParentAPIKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("asaas parent api key: %w", err)
	}
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	asaasClient := asaas.NewLambdaAsaasClient(lambda.NewFromConfig(awsCfg), cfg.PixGatewayFunctionName)
	baasRepo := repositories.NewBaasRepository(clients.DynamoDB, cfg)
	return services.NewBaasService(baasRepo, repo, asaasClient, audit, kyc, masterKey, cfg.AsaasParentWalletID, parentAPIKey), nil
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
