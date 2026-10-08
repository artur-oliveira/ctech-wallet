// Command webhook receives Inter's PIX payment callback over the mTLS-verified
// API Gateway HTTP API custom domain (pix.wallet.aoctech.app). It never trusts
// the payload for money movement (Financial Safety Invariant 11) — it only
// extracts txids and wakes the owning API confirmation flow, which re-queries
// Inter through LambdaPixClient. This Lambda carries no Inter mTLS credentials.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/alerting"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/config"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/secrets"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/walletclient"
)

// Direct-sale prefixes distinguish sandbox-credit and generic-product charges
// from real-wallet deposits. The API mints these deterministic txids so one
// Inter webhook integration can route each wake-up to its owning confirmation
// flow without trusting any payment fact from the callback body.
const (
	sandboxPurchaseTxidPrefix         = "sbxp"
	productPurchaseTxidPrefix         = "prdp"
	confirmDepositFailureBody         = "confirm-deposit failed"
	confirmSandboxPurchaseFailureBody = "confirm-sandbox-purchase failed"
	confirmProductPurchaseFailureBody = "confirm-product-purchase failed"
)

// confirmer is the subset of *walletclient.Client the handler depends on —
// small enough to fake in tests.
type confirmer interface {
	ConfirmDeposit(ctx context.Context, txid, payerCPF, payerName string) error
	ConfirmSandboxPurchase(ctx context.Context, txid string) error
	ConfirmProductPurchase(ctx context.Context, txid string) error
}

// Alert job names, shown in the e-mail subject: "[pix-gateway/<env>] <job>".
const (
	jobWebhook        = "webhook"
	jobWebhookStartup = "webhook-startup"
)

type handler struct {
	confirmer     confirmer
	webhookSecret string
	alerts        alerts.Publisher // nil-safe: handlers built without one (tests) stay valid
	// hmacAlerted limits the hmac-mismatch alert to one per cold start, so a
	// scanner hitting the endpoint cannot flood the inbox.
	hmacAlerted atomic.Bool
}

// webhookPayload is the minimal shape read from Inter's PIX webhook — a
// wake-up signal only, never trusted for amount/status (those come from api's
// own re-query).
type pixWebhookPayload struct {
	Pix []pixWebhookPayloadDetail
}

type pixWebhookPayloadDetail struct {
	EndToEndId  string    `json:"endToEndId"`
	Txid        string    `json:"txid"`
	Valor       string    `json:"valor"`
	Chave       string    `json:"chave"`
	Horario     time.Time `json:"horario"`
	InfoPagador string    `json:"infoPagador"`
	Pagador     struct {
		Nome    string `json:"nome"`
		CpfCnpj string `json:"cpfCnpj"`
	} `json:"pagador"`
	ComponentesValor struct {
		Saque struct {
			Valor                     string `json:"valor"`
			ModalidadeAgente          string `json:"modalidadeAgente"`
			PrestadorDoServicoDeSaque string `json:"prestadorDoServicoDeSaque"`
		} `json:"saque"`
	} `json:"componentesValor"`
	Devolucoes []interface{} `json:"devolucoes"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	// Built first and from the environment, so even a config failure can report.
	notify := alerting.FromEnv(ctx)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		alerting.Failure(ctx, notify, jobWebhookStartup, "webhook Lambda did not start: config load failed", alerting.SanitizedErr(err), "")
		os.Exit(1)
	}
	client, err := newWalletClient(ctx, cfg)
	if err != nil {
		slog.Error("walletclient init failed", "err", err)
		alerting.Failure(ctx, notify, jobWebhookStartup, "webhook Lambda did not start: wallet client init failed", alerting.SanitizedErr(err), "")
		os.Exit(1)
	}
	secret, err := loadWebhookSecret(ctx, cfg)
	if err != nil {
		slog.Error("webhook secret load failed", "err", err)
		alerting.Failure(ctx, notify, jobWebhookStartup, "webhook Lambda did not start: webhook secret load failed", alerting.SanitizedErr(err), "")
		os.Exit(1)
	}
	// client and secret (and the SSM-backed M2M secret + HTTP transport client
	// wraps) are built once at cold start and reused for every invocation — no
	// per-call SSM.
	h := &handler{confirmer: client, webhookSecret: secret, alerts: notify}
	lambda.Start(h.handle)
}

func loadWebhookSecret(ctx context.Context, cfg *config.Config) (string, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return "", err
	}
	store := secrets.NewStore(ssm.NewFromConfig(awsCfg), cfg.Env)
	return store.LoadInterWebhookSecret(ctx)
}

func newWalletClient(ctx context.Context, cfg *config.Config) (*walletclient.Client, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	store := secrets.NewStore(ssm.NewFromConfig(awsCfg), cfg.Env)
	secret, err := store.LoadPixGatewayClientSecret(ctx)
	if err != nil {
		return nil, err
	}
	return walletclient.New(cfg, secret), nil
}

func (h *handler) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	// The shared secret rides in the ?hmac= query param because Banco Inter
	// controls the webhook request format — the only thing we choose is the
	// callback URL registered with Inter, so a header is not an option (B35).
	// Primary auth is the mTLS custom domain; this is defense in depth. API
	// Gateway access logs must never include $request.querystring.
	if subtle.ConstantTimeCompare([]byte(req.QueryStringParameters["hmac"]), []byte(h.webhookSecret)) != 1 {
		slog.WarnContext(ctx, "webhook rejected: hmac mismatch")
		if h.hmacAlerted.CompareAndSwap(false, true) {
			alerting.Failure(ctx, h.alerts, jobWebhook, "webhook rejected: hmac mismatch (possible misconfiguration or probing)", nil, "")
		}
		return events.APIGatewayV2HTTPResponse{StatusCode: 401, Body: "unauthorized"}, nil
	}
	var body pixWebhookPayload
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		slog.ErrorContext(ctx, "webhook request malformed", "err", err)
		// A JSON syntax error names offsets and token kinds, never values.
		alerting.Failure(ctx, h.alerts, jobWebhook, "webhook payload malformed", alerting.SanitizedErr(err), "")
		return events.APIGatewayV2HTTPResponse{StatusCode: 400, Body: "malformed webhook payload"}, nil
	}
	details := body.Pix
	if len(details) == 0 {
		// Inter may send a single detail object directly, without the "pix"
		// list wrapper — fall back to parsing it as one.
		var single pixWebhookPayloadDetail
		if err := json.Unmarshal([]byte(req.Body), &single); err == nil && single.Txid != "" {
			details = []pixWebhookPayloadDetail{single}
		}
	}
	txids := make([]string, 0, len(details))
	for _, p := range details {
		if p.Txid != "" {
			txids = append(txids, p.Txid)
		}
	}
	slog.InfoContext(ctx, "webhook request", "txids", txids)

	resp := events.APIGatewayV2HTTPResponse{StatusCode: 200}
	for _, p := range details {
		if p.Txid == "" {
			continue
		}
		// Dispatch on the txid prefix — a sandbox-purchase txid (minted by
		// PurchaseSandboxDirect) never carries a payer CPF/name gate, so it
		// routes to a different confirmation call entirely (plan §9.3), not a
		// variant of ConfirmDeposit.
		failureBody, err := h.confirm(ctx, p)
		if err != nil {
			slog.ErrorContext(ctx, "webhook response", "status", http.StatusInternalServerError, "txid", p.Txid, "err", err)
			// txid only: the payer (CPF, name) never leaves this process.
			alerting.Failure(ctx, h.alerts, jobWebhook, "webhook confirmation failed; Inter will retry", alerting.SanitizedErr(err), "txid="+p.Txid)
			// Non-200 so Inter retries the whole payload later; ConfirmDeposit is
			// idempotent per txid so a retry never double-credits.
			return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusInternalServerError, Body: failureBody}, nil
		}
	}
	slog.InfoContext(ctx, "webhook response", "status", resp.StatusCode, "txids", txids)
	return resp, nil
}

// confirm routes one Inter wake-up to the owning API confirmation flow. The
// returned body preserves the operation-specific failure response while the
// caller owns the common retry status and logging policy.
func (h *handler) confirm(ctx context.Context, detail pixWebhookPayloadDetail) (string, error) {
	if strings.HasPrefix(detail.Txid, sandboxPurchaseTxidPrefix) {
		return confirmSandboxPurchaseFailureBody, h.confirmer.ConfirmSandboxPurchase(ctx, detail.Txid)
	}
	if strings.HasPrefix(detail.Txid, productPurchaseTxidPrefix) {
		return confirmProductPurchaseFailureBody, h.confirmer.ConfirmProductPurchase(ctx, detail.Txid)
	}
	return confirmDepositFailureBody, h.confirmer.ConfirmDeposit(ctx, detail.Txid, detail.Pagador.CpfCnpj, detail.Pagador.Nome)
}
