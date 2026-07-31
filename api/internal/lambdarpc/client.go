// Package lambdarpc provides the transport shared by API clients that invoke
// the pix-gateway Lambda using the wallet RPC contract.
package lambdarpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
)

// Invoker is the minimal Lambda dependency required by an RPC client.
type Invoker interface {
	Invoke(ctx context.Context, payload []byte) ([]byte, error)
}

// AWSInvoker adapts the AWS SDK client to Invoker.
type AWSInvoker struct {
	client       *lambda.Client
	functionName string
}

func NewAWSInvoker(client *lambda.Client, functionName string) *AWSInvoker {
	return &AWSInvoker{client: client, functionName: functionName}
}

func (a *AWSInvoker) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
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

// Call performs the transport-only portion of an RPC call. Authentication
// refresh and provider-specific error mapping remain the caller's policy.
func Call(ctx context.Context, invoker Invoker, op rpccontract.Op, credential string, args any) (rpccontract.Response, error) {
	payload, err := json.Marshal(args)
	if err != nil {
		return rpccontract.Response{}, err
	}
	request, err := json.Marshal(rpccontract.Request{Op: op, OAuthToken: credential, Payload: payload})
	if err != nil {
		return rpccontract.Response{}, err
	}
	response, err := invoker.Invoke(ctx, request)
	if err != nil {
		return rpccontract.Response{}, err
	}
	var decoded rpccontract.Response
	if err := json.Unmarshal(response, &decoded); err != nil {
		return rpccontract.Response{}, err
	}
	return decoded, nil
}

// DecodePayload decodes a successful response payload when the operation has
// a result. Empty results are valid for command-style operations.
func DecodePayload(response rpccontract.Response, out any) error {
	if out == nil || len(response.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(response.Payload, out)
}

var _ Invoker = (*AWSInvoker)(nil)
