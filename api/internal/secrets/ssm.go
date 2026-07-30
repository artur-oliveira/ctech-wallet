// Package secrets loads api's own SSM SecureString parameters — today just
// the Asaas AES-256 master key and the Asaas webhook access token (plan §3.3,
// §2.3). Mirrors pix-gateway/internal/secrets' shape. None are ever written
// to disk or logged.
package secrets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Parameter paths. %s is the deployment environment (dev/stage/prod).
const (
	asaasMasterKeyParamFmt    = "/ctech-wallet/%s/asaas/api-key-master"
	asaasWebhookTokenParamFmt = "/ctech-wallet/%s/asaas/webhook-token"
	// asaasParentAPIKeyParamFmt is CTech's OWN Asaas parent/master account API
	// key — needed only for the §9.1a reversal leg, which moves money FROM the
	// parent account TO a user's subaccount and so must authenticate as the
	// parent, the mirror image of every other Asaas call in this codebase
	// (which authenticates as the subaccount).
	asaasParentAPIKeyParamFmt = "/ctech-wallet/%s/asaas/parent-api-key"
)

// SSMAPI is the subset of *ssm.Client this package needs (mockable in tests).
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Store reads api's secrets from SSM.
type Store struct {
	client SSMAPI
	env    string
}

func NewStore(client SSMAPI, environment string) *Store {
	return &Store{client: client, env: environment}
}

// LoadAsaasMasterKey fetches the fleet-wide AES-256 master key (hex-encoded)
// used to encrypt every subaccount's Asaas API key at rest (plan §3.3).
func (s *Store) LoadAsaasMasterKey(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(asaasMasterKeyParamFmt, s.env))
}

// LoadAsaasWebhookToken fetches the static token Asaas echoes back in the
// `asaas-access-token` header on every inbound webhook call (plan §2.3).
func (s *Store) LoadAsaasWebhookToken(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(asaasWebhookTokenParamFmt, s.env))
}

// LoadAsaasParentAPIKey fetches CTech's own Asaas parent-account API key
// (plan §9.1a reversal leg).
func (s *Store) LoadAsaasParentAPIKey(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(asaasParentAPIKeyParamFmt, s.env))
}

func (s *Store) get(ctx context.Context, name string) (string, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("ssm: get %s: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil || *out.Parameter.Value == "" {
		return "", fmt.Errorf("ssm: parameter %s is empty", name)
	}
	return *out.Parameter.Value, nil
}
