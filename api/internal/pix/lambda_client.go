// Package pix — lambda_client.go implements PixClient by invoking pix-gateway's
// outbound Lambda synchronously (RequestResponse). This replaces InterClient's
// direct mTLS HTTP calls: api no longer talks to Inter at all — pix-gateway
// does, over IPv4 Lambda egress. Every PixClient method here does the same
// marshal → Invoke → unmarshal dance; only the Op and Args/Result types differ.
package pix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// lambdaInvoker is the subset of *lambda.Client LambdaPixClient depends on —
// small enough to fake in tests without touching AWS.
type lambdaInvoker interface {
	invoke(ctx context.Context, payload []byte) ([]byte, error)
}

// awsLambdaInvoker adapts *lambda.Client to lambdaInvoker.
type awsLambdaInvoker struct {
	client       *lambda.Client
	functionName string
}

func (a *awsLambdaInvoker) invoke(ctx context.Context, payload []byte) ([]byte, error) {
	out, err := a.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   &a.functionName,
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        payload,
	})
	if err != nil {
		return nil, fmt.Errorf("pix-gateway invoke: %w", err)
	}
	if out.FunctionError != nil {
		return nil, fmt.Errorf("pix-gateway invoke: function error: %s: %s", *out.FunctionError, string(out.Payload))
	}
	return out.Payload, nil
}

// LambdaPixClient implements PixClient by invoking pix-gateway's outbound
// Lambda. It never talks to Inter directly. On every call it pulls a fresh
// bearer from the InterTokenManager and passes it to pix-gateway on the wire.
type LambdaPixClient struct {
	invoker  lambdaInvoker
	tokenMgr *InterTokenManager
}

// NewLambdaPixClient builds the client. functionName is the pix-gateway
// outbound Lambda's name or ARN (config.PixGatewayFunctionName). tokenMgr
// supplies the OAuth bearer for each call.
func NewLambdaPixClient(client *lambda.Client, functionName string, tokenMgr *InterTokenManager) *LambdaPixClient {
	return &LambdaPixClient{
		invoker:  &awsLambdaInvoker{client: client, functionName: functionName},
		tokenMgr: tokenMgr,
	}
}

func (c *LambdaPixClient) call(ctx context.Context, op rpcOp, args any, out any) error {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return err
	}
	token, err := c.tokenMgr.Get(ctx, false)
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(rpcRequest{Op: op, OAuthToken: token, Payload: argsJSON})
	if err != nil {
		return err
	}
	respJSON, err := c.invoker.invoke(ctx, reqJSON)
	if err != nil {
		return err
	}
	var resp rpcResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return err
	}
	// Inter rejected the bearer (401). Force-refresh and retry the op once.
	if resp.Error == errUnauthorizedSentinel {
		newToken, ferr := c.tokenMgr.Get(ctx, true)
		if ferr != nil {
			return errors.New(resp.Error)
		}
		reqJSON, err = json.Marshal(rpcRequest{Op: op, OAuthToken: newToken, Payload: argsJSON})
		if err != nil {
			return err
		}
		respJSON, err = c.invoker.invoke(ctx, reqJSON)
		if err != nil {
			return err
		}
		// Reset: json.Unmarshal leaves fields absent from the JSON intact, so the
		// prior attempt's Error would otherwise leak into the retried response.
		resp = rpcResponse{}
		if err := json.Unmarshal(respJSON, &resp); err != nil {
			return err
		}
	}
	if resp.Error != "" {
		if resp.Error == errKeyNotFoundSentinel {
			return ErrKeyNotFound
		}
		return errors.New(resp.Error)
	}
	if out != nil && len(resp.Payload) > 0 {
		return json.Unmarshal(resp.Payload, out)
	}
	return nil
}

func (c *LambdaPixClient) CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error) {
	var res rpcChargeResult
	if err := c.call(ctx, opCreateCharge, rpcCreateChargeArgs{Txid: txid, Amount: amount, PayerHintCPF: payerHintCPF}, &res); err != nil {
		return nil, err
	}
	return chargeFromRPC(res), nil
}

func (c *LambdaPixClient) QueryCharge(ctx context.Context, txid string) (*Charge, error) {
	var res rpcChargeResult
	if err := c.call(ctx, opQueryCharge, rpcQueryChargeArgs{Txid: txid}, &res); err != nil {
		return nil, err
	}
	return chargeFromRPC(res), nil
}

func (c *LambdaPixClient) DictLookup(ctx context.Context, pixKey string) (*DictAccount, error) {
	var res rpcDictResult
	if err := c.call(ctx, opDictLookup, rpcDictLookupArgs{PixKey: pixKey}, &res); err != nil {
		return nil, err
	}
	return &DictAccount{Key: res.Key, CPF: res.CPF, Name: res.Name}, nil
}

func (c *LambdaPixClient) Transfer(ctx context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error) {
	var res rpcTransferResult
	if err := c.call(ctx, opTransfer, rpcTransferArgs{PixKey: pixKey, Amount: amount, IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) QueryTransfer(ctx context.Context, idemKey string) (*TransferResult, error) {
	var res rpcTransferResult
	if err := c.call(ctx, opQueryTransfer, rpcQueryTransferArgs{IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) Refund(ctx context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error) {
	var res rpcTransferResult
	if err := c.call(ctx, opRefund, rpcRefundArgs{E2EID: e2eID, Amount: amount, IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) Ping(ctx context.Context) error {
	return c.call(ctx, opPing, struct{}{}, nil)
}

func chargeFromRPC(r rpcChargeResult) *Charge {
	return &Charge{
		Txid: r.Txid, Amount: r.Amount, QRCode: r.QRCode, QRCodeB64: r.QRCodeB64,
		Status: r.Status, PayerCPF: r.PayerCPF, E2EID: r.E2EID,
	}
}

func transferFromRPC(r rpcTransferResult) *TransferResult {
	return &TransferResult{E2EID: r.E2EID, Status: r.Status}
}

var _ PixClient = (*LambdaPixClient)(nil)
