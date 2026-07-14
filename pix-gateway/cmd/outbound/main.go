// Command outbound is the Lambda pix-gateway invokes for every outbound Inter
// PIX call api needs (CreateCharge, QueryCharge, DictLookup, Transfer,
// QueryTransfer, Refund, Ping). api's LambdaPixClient calls it synchronously
// (RequestResponse) — one op per invocation, mirroring the PixClient interface
// api already depends on.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/config"
	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/inter"
	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/rpc"
	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/secrets"
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
	h := &handler{pix: pixClient}
	lambda.Start(h.handle)
}

// newInter builds the real Inter client, reading the mTLS keypair AND the OAuth
// client secret from SSM directly — this Lambda has no start.sh to export env
// vars (same reasoning as api/cmd/reconcile's newPix).
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
	if cfg.InterClientSecret == "" {
		s, err := store.LoadInterClientSecret(ctx)
		if err != nil {
			return nil, fmt.Errorf("load client secret: %w", err)
		}
		cfg.InterClientSecret = s
	}
	return inter.NewInterClient(cfg, kp)
}

// handle dispatches on Op, decodes Payload into the matching *Args struct,
// calls the corresponding PixClient method, and encodes the result. Every
// error becomes Response.Error — Lambda invoke errors are reserved for
// transport failures, not business/bank errors, so api's LambdaPixClient reads
// a normal (non-error) Invoke response and inspects Response.Error itself.
func (h *handler) handle(ctx context.Context, req rpc.Request) rpc.Response {
	// Seed the bearer api passed per call; inter reads it from ctx in do/doIdem.
	ctx = inter.WithBearer(ctx, req.OAuthToken)
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

	case rpc.OpDictLookup:
		var a rpc.DictLookupArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		d, err := h.pix.DictLookup(ctx, a.PixKey)
		if err != nil {
			return toResp(err)
		}
		return okResp(rpc.DictResult{Key: d.Key, CPF: d.CPF, Name: d.Name})

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
	return rpc.ChargeResult{
		Txid: c.Txid, Amount: c.Amount, QRCode: c.QRCode, QRCodeB64: c.QRCodeB64,
		Status: c.Status, PayerCPF: c.PayerCPF, E2EID: c.E2EID,
	}
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

// toResp maps inter errors to the wire sentinels api knows how to handle:
// a missing DICT owner, and an Inter 401 (bad/expired bearer). Everything else
// is an opaque bank/transport failure string.
func toResp(err error) rpc.Response {
	if errors.Is(err, inter.ErrKeyNotFound) {
		return rpc.Response{Error: rpc.ErrKeyNotFoundSentinel}
	}
	if inter.IsUnauthorized(err) {
		return rpc.Response{Error: rpc.ErrUnauthorizedSentinel}
	}
	return errResp(err)
}

func errResp(err error) rpc.Response {
	return rpc.Response{Error: err.Error()}
}
