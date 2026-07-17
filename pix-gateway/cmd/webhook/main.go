// Command webhook receives Inter's PIX payment callback over the mTLS-verified
// API Gateway HTTP API custom domain (pix.wallet.aoctech.app). It never trusts
// the payload for money movement (Financial Safety Invariant 11) — it only
// extracts the txid(s) and asks api to re-derive and credit the deposit via
// WalletService.ConfirmDeposit, which re-queries Inter itself through
// LambdaPixClient. This Lambda carries no Inter mTLS credentials at all.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/config"
	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/secrets"
	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/walletclient"
)

// confirmer is the subset of *walletclient.Client the handler depends on —
// small enough to fake in tests.
type confirmer interface {
	ConfirmDeposit(ctx context.Context, txid, payerCPF, payerName string) error
}

type handler struct {
	confirmer confirmer
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

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	client, err := newWalletClient(context.Background(), cfg)
	if err != nil {
		slog.Error("walletclient init failed", "err", err)
		os.Exit(1)
	}
	// client (and the SSM-backed M2M secret + HTTP transport it wraps) is built
	// once at cold start and reused for every invocation — no per-call SSM.
	h := &handler{confirmer: client}
	lambda.Start(h.handle)
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
	var body pixWebhookPayload
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		slog.ErrorContext(ctx, "webhook request malformed", "body", req.Body, "err", err)
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
	slog.InfoContext(ctx, "webhook request", "txids", txids, "body", req.Body)

	resp := events.APIGatewayV2HTTPResponse{StatusCode: 200}
	for _, p := range details {
		if p.Txid == "" {
			continue
		}
		if err := h.confirmer.ConfirmDeposit(ctx, p.Txid, p.Pagador.CpfCnpj, p.Pagador.Nome); err != nil {
			slog.ErrorContext(ctx, "webhook response", "status", 500, "txid", p.Txid, "err", err)
			// Non-200 so Inter retries the whole payload later; ConfirmDeposit is
			// idempotent per txid so a retry never double-credits.
			return events.APIGatewayV2HTTPResponse{StatusCode: 500, Body: "confirm-deposit failed"}, nil
		}
	}
	slog.InfoContext(ctx, "webhook response", "status", resp.StatusCode, "txids", txids)
	return resp, nil
}
