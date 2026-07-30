// Package asaas — lambda_client.go implements AsaasClient by invoking
// pix-gateway's outbound Lambda, mirroring pix.LambdaPixClient exactly (same
// Invoke/Request/Response shape, same rpc-contract package) — api never
// talks to Asaas directly, same posture as Inter.
package asaas

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
)

// lambdaInvoker is the subset of *lambda.Client this client depends on —
// mirrors pix.lambdaInvoker so both can be faked identically in tests.
type lambdaInvoker interface {
	invoke(ctx context.Context, payload []byte) ([]byte, error)
}

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

// LambdaAsaasClient implements AsaasClient by invoking pix-gateway's outbound
// Lambda. It never talks to Asaas directly.
type LambdaAsaasClient struct {
	invoker lambdaInvoker
}

// NewLambdaAsaasClient builds the client. functionName is pix-gateway's
// outbound Lambda's name/ARN — the SAME function api's LambdaPixClient
// invokes for Inter ops (config.PixGatewayFunctionName); the two Ops just
// carry different op discriminators in the same wire contract (plan §2.2).
func NewLambdaAsaasClient(client *lambda.Client, functionName string) *LambdaAsaasClient {
	return &LambdaAsaasClient{invoker: &awsLambdaInvoker{client: client, functionName: functionName}}
}

func (c *LambdaAsaasClient) call(ctx context.Context, op rpccontract.Op, apiKey string, args any, out any) error {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return err
	}
	// apiKey travels in OAuthToken — the transport field already exists to
	// carry "the credential this call authenticates with" (Inter's bearer
	// today); every Asaas *Args struct also repeats it in-payload for
	// CreateAccount's parent-key case where the caller and the credential
	// being used are the SAME field slot, avoiding a second wire field.
	reqJSON, err := json.Marshal(rpccontract.Request{Op: op, OAuthToken: apiKey, Payload: argsJSON})
	if err != nil {
		return err
	}
	respJSON, err := c.invoker.invoke(ctx, reqJSON)
	if err != nil {
		return err
	}
	var resp rpccontract.Response
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("asaas: %s", resp.Error)
	}
	if out != nil && len(resp.Payload) > 0 {
		return json.Unmarshal(resp.Payload, out)
	}
	return nil
}

func (c *LambdaAsaasClient) CreateAccount(ctx context.Context, req CreateAccountRequest) (*Account, error) {
	var res rpccontract.AsaasAccountResult
	args := rpccontract.AsaasCreateAccountArgs{
		Name: req.Name, CPF: req.CPF, Email: req.Email, MobilePhone: req.MobilePhone, BirthDate: req.BirthDate,
		Address: req.Address, AddressNumber: req.AddressNumber, Complement: req.Complement,
		Province: req.Province, City: req.City, State: req.State, PostalCode: req.PostalCode, IncomeValue: req.IncomeValue,
	}
	if err := c.call(ctx, rpccontract.OpAsaasCreateAccount, "", args, &res); err != nil {
		return nil, err
	}
	return &Account{ID: res.ID, WalletID: res.WalletID, APIKey: res.APIKey, Status: res.Status, OnboardingURL: res.OnboardingURL}, nil
}

func (c *LambdaAsaasClient) UploadDocument(ctx context.Context, subaccountAPIKey, documentID string, file []byte) error {
	return c.call(ctx, rpccontract.OpAsaasUploadDocument, subaccountAPIKey,
		rpccontract.AsaasUploadDocumentArgs{APIKey: subaccountAPIKey, DocumentID: documentID, File: file}, nil)
}

func (c *LambdaAsaasClient) CreateStaticPixKey(ctx context.Context, subaccountAPIKey string) (*PixAddressKey, error) {
	var res rpccontract.AsaasPixAddressKeyResult
	if err := c.call(ctx, rpccontract.OpAsaasCreateStaticPixKey, subaccountAPIKey,
		rpccontract.AsaasCreateStaticPixKeyArgs{APIKey: subaccountAPIKey}, &res); err != nil {
		return nil, err
	}
	return &PixAddressKey{Key: res.Key, Status: res.Status}, nil
}

func (c *LambdaAsaasClient) CreatePixQRCode(ctx context.Context, subaccountAPIKey string, req QRCodeRequest) (*QRCode, error) {
	var res rpccontract.AsaasQRCodeResult
	args := rpccontract.AsaasCreatePixQRCodeArgs{
		APIKey: subaccountAPIKey, AddressKey: req.AddressKey, Value: req.Value, Format: req.Format,
		ExpirationSeconds: req.ExpirationSeconds, AllowsMultiplePayments: req.AllowsMultiplePayments,
		ExternalReference: req.ExternalReference,
	}
	if err := c.call(ctx, rpccontract.OpAsaasCreatePixQRCode, subaccountAPIKey, args, &res); err != nil {
		return nil, err
	}
	return &QRCode{PixQRCodeID: res.PixQRCodeID, Payload: res.Payload, EncodedImage: res.EncodedImage, ExpirationDate: res.ExpirationDate}, nil
}

func (c *LambdaAsaasClient) QueryPayment(ctx context.Context, apiKey, paymentID string) (*Payment, error) {
	var res rpccontract.AsaasPaymentResult
	if err := c.call(ctx, rpccontract.OpAsaasQueryPayment, apiKey,
		rpccontract.AsaasQueryPaymentArgs{APIKey: apiKey, PaymentID: paymentID}, &res); err != nil {
		return nil, err
	}
	return &Payment{ID: res.ID, Value: res.Value, Status: res.Status, ExternalReference: res.ExternalReference}, nil
}

func (c *LambdaAsaasClient) CreateTransfer(ctx context.Context, apiKey string, req TransferRequest) (*Transfer, error) {
	var res rpccontract.AsaasTransferResult
	args := rpccontract.AsaasCreateTransferArgs{
		APIKey: apiKey, Value: req.Value, PixAddressKey: req.PixAddressKey, PixAddressKeyType: req.PixAddressKeyType,
		WalletID: req.WalletID, ExternalReference: req.ExternalReference,
	}
	if err := c.call(ctx, rpccontract.OpAsaasCreateTransfer, apiKey, args, &res); err != nil {
		return nil, err
	}
	return &Transfer{ID: res.ID, Status: res.Status, TransferFee: res.TransferFee, ExternalReference: res.ExternalReference}, nil
}

func (c *LambdaAsaasClient) QueryTransfer(ctx context.Context, apiKey, externalReference string) (*Transfer, error) {
	var res rpccontract.AsaasTransferResult
	if err := c.call(ctx, rpccontract.OpAsaasQueryTransfer, apiKey,
		rpccontract.AsaasQueryTransferArgs{APIKey: apiKey, ExternalReference: externalReference}, &res); err != nil {
		return nil, err
	}
	return &Transfer{ID: res.ID, Status: res.Status, TransferFee: res.TransferFee, ExternalReference: res.ExternalReference}, nil
}

func (c *LambdaAsaasClient) QueryAccountBalance(ctx context.Context, apiKey string) (int64, error) {
	var res rpccontract.AsaasBalanceResult
	if err := c.call(ctx, rpccontract.OpAsaasQueryAccountBalance, apiKey,
		rpccontract.AsaasQueryAccountBalanceArgs{APIKey: apiKey}, &res); err != nil {
		return 0, err
	}
	return res.Balance, nil
}

var _ AsaasClient = (*LambdaAsaasClient)(nil)
