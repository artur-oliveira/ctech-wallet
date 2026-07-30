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
	"math"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/wallet/pix-gateway/internal/asaas"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/config"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/inter"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/secrets"
	rpc "gopkg.aoctech.app/wallet/rpc-contract"
)

type handler struct {
	pix   inter.PixClient
	asaas *asaas.AsaasClient
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
	// asaasClient needs no SSM/cold-start secret at all — every Asaas call
	// carries its own api_key in the request payload (plan §2.2), unlike Inter's
	// shared OAuth bearer.
	h := &handler{pix: pixClient, asaas: asaas.NewAsaasClient(cfg.AsaasBaseURL)}
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

	case rpc.OpAsaasCreateAccount:
		var a rpc.AsaasCreateAccountArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		// The parent account's own API key travels as req.OAuthToken — reusing
		// that transport field (not a new one) since it already exists to carry
		// "the credential this call authenticates with," exactly its purpose for
		// Inter's bearer above.
		acc, err := h.asaas.CreateAccount(ctx, req.OAuthToken, asaas.CreateAccountArgs{
			Name: a.Name, CPF: a.CPF, Email: a.Email, MobilePhone: a.MobilePhone, BirthDate: a.BirthDate,
			Address: a.Address, AddressNumber: a.AddressNumber, Complement: a.Complement,
			Province: a.Province, City: a.City, State: a.State, PostalCode: a.PostalCode, IncomeValue: a.IncomeValue,
		})
		if err != nil {
			return errResp(err)
		}
		return okResp(rpc.AsaasAccountResult{ID: acc.ID, WalletID: acc.WalletID, APIKey: acc.APIKey, Status: acc.Status})

	case rpc.OpAsaasUploadDocument:
		var a rpc.AsaasUploadDocumentArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		if err := h.asaas.UploadDocument(ctx, a.APIKey, a.DocumentID, a.File); err != nil {
			return errResp(err)
		}
		return rpc.Response{}

	case rpc.OpAsaasCreateStaticPixKey:
		var a rpc.AsaasCreateStaticPixKeyArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		k, err := h.asaas.CreateStaticPixKey(ctx, a.APIKey)
		if err != nil {
			return errResp(err)
		}
		return okResp(rpc.AsaasPixAddressKeyResult{Key: k.Key, Status: k.Status})

	case rpc.OpAsaasCreatePixQRCode:
		var a rpc.AsaasCreatePixQRCodeArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		qr, err := h.asaas.CreatePixQRCode(ctx, a.APIKey, asaas.CreatePixQRCodeArgs{
			AddressKey: a.AddressKey, Value: a.Value, Format: a.Format,
			ExpirationSeconds: a.ExpirationSeconds, AllowsMultiplePayments: a.AllowsMultiplePayments,
			ExternalReference: a.ExternalReference,
		})
		if err != nil {
			return errResp(err)
		}
		return okResp(rpc.AsaasQRCodeResult{
			PixQRCodeID: qr.ID, Payload: qr.Payload, EncodedImage: qr.EncodedImage, ExpirationDate: qr.ExpirationDate,
		})

	case rpc.OpAsaasQueryPayment:
		var a rpc.AsaasQueryPaymentArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		p, err := h.asaas.QueryPayment(ctx, a.APIKey, a.PaymentID)
		if err != nil {
			return errResp(err)
		}
		return okResp(rpc.AsaasPaymentResult{
			ID: p.ID, Value: asaasCentavos(p.Value), Status: p.Status, ExternalReference: p.ExternalReference,
		})

	case rpc.OpAsaasCreateTransfer:
		var a rpc.AsaasCreateTransferArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		t, err := h.asaas.CreateTransfer(ctx, a.APIKey, asaas.CreateTransferArgs{
			Value: a.Value, PixAddressKey: a.PixAddressKey, PixAddressKeyType: a.PixAddressKeyType,
			WalletID: a.WalletID, ExternalReference: a.ExternalReference,
		})
		if err != nil {
			return errResp(err)
		}
		return okResp(rpc.AsaasTransferResult{
			ID: t.ID, Status: t.Status, TransferFee: asaasCentavos(t.TransferFee), ExternalReference: t.ExternalReference,
		})

	case rpc.OpAsaasQueryTransfer:
		var a rpc.AsaasQueryTransferArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		t, err := h.asaas.QueryTransfer(ctx, a.APIKey, a.ExternalReference)
		if err != nil {
			return errResp(err)
		}
		return okResp(rpc.AsaasTransferResult{
			ID: t.ID, Status: t.Status, TransferFee: asaasCentavos(t.TransferFee), ExternalReference: t.ExternalReference,
		})

	case rpc.OpAsaasQueryAccountBalance:
		var a rpc.AsaasQueryAccountBalanceArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return toResp(err)
		}
		balance, err := h.asaas.QueryAccountBalance(ctx, a.APIKey)
		if err != nil {
			return errResp(err)
		}
		return okResp(rpc.AsaasBalanceResult{Balance: balance})

	default:
		return errResp(fmt.Errorf("unknown op %q", req.Op))
	}
}

// asaasCentavos rounds a decimal-reais float (as Asaas reports it on the
// wire) to integer centavos — the one conversion point on the response side,
// mirroring internal/asaas/money.go's reaisToCentavos without exporting it
// across packages.
func asaasCentavos(reais float64) int64 {
	return int64(math.Round(reais * 100))
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
