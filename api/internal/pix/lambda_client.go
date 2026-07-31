// Package pix — lambda_client.go implements PixClient by invoking pix-gateway's
// outbound Lambda synchronously (RequestResponse). This replaces InterClient's
// direct mTLS HTTP calls: api no longer talks to Inter at all — pix-gateway
// does, over IPv4 Lambda egress. Every PixClient method here does the same
// marshal → Invoke → unmarshal dance; only the Op and Args/Result types differ.
package pix

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"gopkg.aoctech.app/wallet/api/internal/lambdarpc"
	rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
)

// LambdaPixClient implements PixClient by invoking pix-gateway's outbound
// Lambda. It never talks to Inter directly. On every call it pulls a fresh
// bearer from the InterTokenManager and passes it to pix-gateway on the wire.
type LambdaPixClient struct {
	invoker  lambdarpc.Invoker
	tokenMgr *InterTokenManager
}

// NewLambdaPixClient builds the client. functionName is the pix-gateway
// outbound Lambda's name or ARN (config.PixGatewayFunctionName). tokenMgr
// supplies the OAuth bearer for each call.
func NewLambdaPixClient(client *lambda.Client, functionName string, tokenMgr *InterTokenManager) *LambdaPixClient {
	return &LambdaPixClient{
		invoker:  lambdarpc.NewAWSInvoker(client, functionName),
		tokenMgr: tokenMgr,
	}
}

func (c *LambdaPixClient) call(ctx context.Context, op rpccontract.Op, args any, out any) error {
	token, err := c.tokenMgr.Get(ctx, false)
	if err != nil {
		return err
	}
	resp, err := lambdarpc.Call(ctx, c.invoker, op, token, args)
	if err != nil {
		return err
	}
	// Inter rejected the bearer (401). Drop the cached token and force-refresh
	// a genuinely new one, then retry the op once.
	if resp.Error == rpccontract.ErrUnauthorizedSentinel {
		c.tokenMgr.Invalidate(ctx)
		newToken, ferr := c.tokenMgr.Get(ctx, true)
		if ferr != nil {
			return errors.New(resp.Error)
		}
		resp, err = lambdarpc.Call(ctx, c.invoker, op, newToken, args)
		if err != nil {
			return err
		}
	}
	if resp.Error == rpccontract.ErrKeyNotFoundSentinel {
		return ErrKeyNotFound
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return lambdarpc.DecodePayload(resp, out)
}

func (c *LambdaPixClient) CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error) {
	var res rpccontract.ChargeResult
	if err := c.call(ctx, rpccontract.OpCreateCharge, rpccontract.CreateChargeArgs{Txid: txid, Amount: amount, PayerHintCPF: payerHintCPF}, &res); err != nil {
		return nil, err
	}
	return chargeFromRPC(res), nil
}

func (c *LambdaPixClient) QueryCharge(ctx context.Context, txid string) (*Charge, error) {
	var res rpccontract.ChargeResult
	if err := c.call(ctx, rpccontract.OpQueryCharge, rpccontract.QueryChargeArgs{Txid: txid}, &res); err != nil {
		return nil, err
	}
	return chargeFromRPC(res), nil
}

func (c *LambdaPixClient) Transfer(ctx context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error) {
	var res rpccontract.TransferResult
	if err := c.call(ctx, rpccontract.OpTransfer, rpccontract.TransferArgs{PixKey: pixKey, Amount: amount, IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) QueryTransfer(ctx context.Context, idemKey string) (*TransferResult, error) {
	var res rpccontract.TransferResult
	if err := c.call(ctx, rpccontract.OpQueryTransfer, rpccontract.QueryTransferArgs{IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) Refund(ctx context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error) {
	var res rpccontract.TransferResult
	if err := c.call(ctx, rpccontract.OpRefund, rpccontract.RefundArgs{E2EID: e2eID, Amount: amount, IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) Ping(ctx context.Context) error {
	return c.call(ctx, rpccontract.OpPing, struct{}{}, nil)
}

func chargeFromRPC(r rpccontract.ChargeResult) *Charge {
	payments := make([]Payment, len(r.Payments))
	for i, p := range r.Payments {
		payments[i] = Payment{E2EID: p.E2EID, Amount: p.Amount, PayerCPF: p.PayerCPF, Refunds: refundsFromRPC(p.Refunds)}
	}
	return &Charge{
		Txid: r.Txid, Amount: r.Amount, QRCode: r.QRCode, QRCodeB64: r.QRCodeB64,
		Status: r.Status, PayerCPF: r.PayerCPF, E2EID: r.E2EID, Refunds: refundsFromRPC(r.Refunds), Payments: payments,
	}
}

func refundsFromRPC(refunds []rpccontract.RefundResult) []Refund {
	out := make([]Refund, len(refunds))
	for i, r := range refunds {
		out[i] = Refund{RtrID: r.RtrID, Amount: r.Amount, Status: r.Status}
	}
	return out
}

func transferFromRPC(r rpccontract.TransferResult) *TransferResult {
	return &TransferResult{E2EID: r.E2EID, Status: r.Status}
}

var _ PixClient = (*LambdaPixClient)(nil)
