package secrets

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type mockSSM struct {
	values map[string]string
}

func (m *mockSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	v, ok := m.values[*in.Name]
	if !ok {
		return &ssm.GetParameterOutput{}, nil
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: aws.String(v)}}, nil
}

func TestLoadPixGatewayClientSecret(t *testing.T) {
	mock := &mockSSM{values: map[string]string{
		"/ctech-wallet/dev/pix-gateway/client-secret": "shh",
	}}
	store := NewStore(mock, "dev")
	got, err := store.LoadPixGatewayClientSecret(context.Background())
	if err != nil {
		t.Fatalf("LoadPixGatewayClientSecret: %v", err)
	}
	if got != "shh" {
		t.Fatalf("got %q, want %q", got, "shh")
	}
}

func TestLoadInterMTLS(t *testing.T) {
	mock := &mockSSM{values: map[string]string{
		"/ctech-wallet/dev/inter/mtls-cert": "CERT",
		"/ctech-wallet/dev/inter/mtls-key":  "KEY",
	}}
	store := NewStore(mock, "dev")
	kp, err := store.LoadInterMTLS(context.Background())
	if err != nil {
		t.Fatalf("LoadInterMTLS: %v", err)
	}
	if string(kp.CertPEM) != "CERT" || string(kp.KeyPEM) != "KEY" {
		t.Fatalf("bad keypair: %+v", kp)
	}
}
