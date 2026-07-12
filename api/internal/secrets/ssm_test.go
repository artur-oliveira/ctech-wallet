package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	params    map[string]string
	decrypted []string // names fetched WithDecryption
	err       error
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if in.WithDecryption != nil && *in.WithDecryption {
		f.decrypted = append(f.decrypted, *in.Name)
	}
	v, ok := f.params[*in.Name]
	if !ok {
		return &ssm.GetParameterOutput{}, nil
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: aws.String(v)}}, nil
}

func TestLoadInterMTLS(t *testing.T) {
	f := &fakeSSM{params: map[string]string{
		"/ctech-wallet/prod/inter/mtls-cert": "CERT-PEM",
		"/ctech-wallet/prod/inter/mtls-key":  "KEY-PEM",
	}}

	kp, err := NewStore(f, "prod").LoadInterMTLS(context.Background())
	if err != nil {
		t.Fatalf("LoadInterMTLS: %v", err)
	}
	if string(kp.CertPEM) != "CERT-PEM" || string(kp.KeyPEM) != "KEY-PEM" {
		t.Fatalf("bad keypair: %+v", kp)
	}
	// Both are SecureStrings — decryption must always be requested.
	if len(f.decrypted) != 2 {
		t.Errorf("expected both params fetched WithDecryption, got %v", f.decrypted)
	}
}

func TestLoadInterMTLSEmptyParamFails(t *testing.T) {
	// A missing/empty parameter must be a hard error, never an empty keypair.
	f := &fakeSSM{params: map[string]string{"/ctech-wallet/dev/inter/mtls-cert": "CERT"}}
	if _, err := NewStore(f, "dev").LoadInterMTLS(context.Background()); err == nil {
		t.Fatal("expected error for missing mtls-key, got nil")
	}
}

func TestLoadInterMTLSPropagatesError(t *testing.T) {
	f := &fakeSSM{err: errors.New("access denied")}
	if _, err := NewStore(f, "prod").LoadInterMTLS(context.Background()); err == nil {
		t.Fatal("expected SSM error to propagate, got nil")
	}
}
