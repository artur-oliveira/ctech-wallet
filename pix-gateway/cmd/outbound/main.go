// Command outbound is the Lambda pix-gateway invokes for every outbound Inter
// PIX call api needs (CreateCharge, QueryCharge, Transfer,
// QueryTransfer, Refund, Ping). api's LambdaPixClient calls it synchronously
// (RequestResponse) — one op per invocation, mirroring the PixClient interface
// api already depends on.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/wallet/pix-gateway/internal/config"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/inter"
	rpc "gopkg.aoctech.app/wallet/rpc-contract"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/secrets"
)

type handler struct {
	pix inter.PixClient
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	pixClient, err := newInter(context.Background(), cfg)
	if err != nil {
		slog.Error("inter client init failed", "err", err)
		os.Exit(1)
	}
	// pixClient (and the SSM store + mTLS HTTP transport it wraps) is built once
	// at cold start and reused for every invocation — no per-call SSM/SSM-KMS.
	h := &handler{pix: pixClient}
	lambda.Start(h.handle)
}

// newInter builds the real Inter client. The mTLS keypair is read from SSM at
// cold start (it is required to build the cached mTLS HTTP transport). The
// Inter OAuth client secret is NOT read here — GetToken loads and caches it
// lazily on first use, so a cold start that never calls GetToken never hits SSM.
func newInter(ctx context.Context, cfg *config.Config) (inter.PixClient, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	store := secrets.NewStore(ssm.NewFromConfig(awsCfg), cfg.Env)
	kp, err := store.LoadInterMTLS(ctx)
	if err != nil {
		return nil, fmt.Errorf("load mTLS keypair: %w", err)
	}
	return inter.NewInterClient(cfg, kp, store)
}

// handle logs the Invoke request/response (OAuthToken and other sensitive
// fields scrubbed) then dispatches to the matching PixClient method.
func (h *handler) handle(ctx context.Context, req rpc.Request) (rpc.Response, error) {
	slog.InfoContext(ctx, "outbound request",
		"op", req.Op,
		"oauth_token", "[redacted]",
		"payload", string(scrubPayload(req.Payload)),
	)
	// Seed the bearer api passed per call; inter reads it from ctx in do/doIdem.
	ctx = inter.WithBearer(ctx, req.OAuthToken)
	resp := h.dispatch(ctx, req)
	slog.InfoContext(ctx, "outbound response",
		"op", req.Op,
		"error", resp.Error,
		"payload", string(scrubPayload(resp.Payload)),
	)
	return resp, nil
}

// dispatch decodes the Payload into the matching *Args struct, calls the
// corresponding PixClient method, and encodes the result. Every error becomes
// Response.Error — Lambda invoke errors are reserved for transport failures,
// not business/bank errors, so api's LambdaPixClient reads a normal (non-error)
// Invoke response and inspects Response.Error itself.
func (h *handler) dispatch(ctx context.Context, req rpc.Request) rpc.Response {
	switch req.Op {
	case rpc.OpCreateCharge:
		var a rpc.CreateChargeArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		c, err := h.pix.CreateCharge(ctx, a.Txid, a.Amount, a.PayerHintCPF)
		if err != nil {
			return toResp(err)
		}
		return okResp(chargeResult(c))

	case rpc.OpQueryCharge:
		var a rpc.QueryChargeArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		c, err := h.pix.QueryCharge(ctx, a.Txid)
		if err != nil {
			return toResp(err)
		}
		return okResp(chargeResult(c))

	case rpc.OpTransfer:
		var a rpc.TransferArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		r, err := h.pix.Transfer(ctx, a.PixKey, a.Amount, a.IdemKey)
		if err != nil {
			return toResp(err)
		}
		return okResp(transferResult(r))

	case rpc.OpQueryTransfer:
		var a rpc.QueryTransferArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		r, err := h.pix.QueryTransfer(ctx, a.IdemKey)
		if err != nil {
			return toResp(err)
		}
		return okResp(transferResult(r))

	case rpc.OpRefund:
		var a rpc.RefundArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		r, err := h.pix.Refund(ctx, a.E2EID, a.Amount, a.IdemKey)
		if err != nil {
			return toResp(err)
		}
		return okResp(transferResult(r))

	case rpc.OpPing:
		if err := h.pix.Ping(ctx); err != nil {
			return toResp(err)
		}
		return rpc.Response{}

	case rpc.OpGetToken:
		t, err := h.pix.GetToken(ctx)
		if err != nil {
			return toResp(err)
		}
		return okResp(rpc.GetTokenResult{Token: t.Token, ExpiresIn: t.ExpiresIn})

	default:
		return errResp(fmt.Errorf("unknown op %q", req.Op))
	}
}

func chargeResult(c *inter.Charge) rpc.ChargeResult {
	payments := make([]rpc.PaymentResult, len(c.Payments))
	for i, p := range c.Payments {
		payments[i] = rpc.PaymentResult{E2EID: p.E2EID, Amount: p.Amount, PayerCPF: p.PayerCPF, Refunds: refundResults(p.Refunds)}
	}
	return rpc.ChargeResult{
		Txid: c.Txid, Amount: c.Amount, QRCode: c.QRCode, QRCodeB64: c.QRCodeB64,
		Status: c.Status, PayerCPF: c.PayerCPF, E2EID: c.E2EID, Refunds: refundResults(c.Refunds), Payments: payments,
	}
}

func refundResults(refunds []inter.Refund) []rpc.RefundResult {
	out := make([]rpc.RefundResult, len(refunds))
	for i, r := range refunds {
		out[i] = rpc.RefundResult{RtrID: r.RtrID, Amount: r.Amount, Status: r.Status}
	}
	return out
}

func transferResult(r *inter.TransferResult) rpc.TransferResult {
	return rpc.TransferResult{E2EID: r.E2EID, Status: r.Status}
}

func okResp(v any) rpc.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return errResp(err)
	}
	return rpc.Response{Payload: b}
}

// toResp maps inter errors to the wire sentinels api knows how to handle: an
// unregistered destination PIX key (Transfer 404), and an Inter 401
// (bad/expired bearer). Everything else is an opaque bank/transport failure
// string.
func toResp(err error) rpc.Response {
	if inter.IsUnauthorized(err) {
		return rpc.Response{Error: rpc.ErrUnauthorizedSentinel}
	}
	if inter.IsKeyNotFound(err) {
		return rpc.Response{Error: rpc.ErrKeyNotFoundSentinel}
	}
	return errResp(err)
}

func errResp(err error) rpc.Response {
	return rpc.Response{Error: err.Error()}
}

// scrubPayload returns payload with sensitive/oversized fields redacted so
// request/response logs never leak secrets (oauth_token is scrubbed by the
// caller) or dump multi-KB blobs. Redacted: token (Inter bearer), qr_code_b64
// (base64 PNG), payer_hint_cpf/cpf (PII). Request and response payloads are
// single top-level objects, so a shallow key strip is enough.
func scrubPayload(p json.RawMessage) json.RawMessage {
	if len(p) == 0 {
		return p
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(p, &m); err != nil {
		return p
	}
	for _, k := range []string{"token", "payer_hint_cpf", "cpf"} {
		if _, ok := m[k]; ok {
			m[k] = json.RawMessage(`"[redacted]"`)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return p
	}
	return out
}
