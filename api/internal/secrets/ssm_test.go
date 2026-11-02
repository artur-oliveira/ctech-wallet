package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	value *string
	calls int
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls++
	if f.value == nil {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: f.value}}, nil
}

// Each SecureString read is a billed KMS Decrypt: repeated Loads within CacheTTL
// must hit SSM once, and a rotated value must be picked up after it.
func TestLoadCachesForTTL(t *testing.T) {
	f := &fakeSSM{value: aws.String("v1")}
	s := NewStore(f, "prod")
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if got, err := s.Load(context.Background(), "billing-client-secret"); err != nil || got != "v1" {
			t.Fatalf("got %q, %v", got, err)
		}
	}
	if f.calls != 1 {
		t.Fatalf("ssm calls = %d, want 1", f.calls)
	}
	f.value = aws.String("v2")
	now = now.Add(CacheTTL + time.Second)
	if got, _ := s.Load(context.Background(), "billing-client-secret"); got != "v2" || f.calls != 2 {
		t.Fatalf("got %q after TTL, calls %d", got, f.calls)
	}
}

func TestLoadMissingIsNotCached(t *testing.T) {
	f := &fakeSSM{}
	s := NewStore(f, "prod")
	for i := 0; i < 2; i++ {
		if got, err := s.Load(context.Background(), "x"); err != nil || got != "" {
			t.Fatalf("got %q, %v", got, err)
		}
	}
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 (missing must be retried)", f.calls)
	}
	f.value = aws.String("now-set")
	if got, _ := s.LoadM2MClients(context.Background()); got != "now-set" {
		t.Fatalf("got %q", got)
	}
}
