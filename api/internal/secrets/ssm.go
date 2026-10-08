// Package secrets loads api's own SSM SecureString parameters — today just
// the M2M client registry. Mirrors pix-gateway/internal/secrets' shape. None
// are ever written to disk or logged.
package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Parameter paths. %s is the deployment environment (dev/stage/prod).
const (
	// m2mClientsParamFmt holds a JSON object (client_id → {webhook_url,
	// hmac_secret}) for every M2M caller registered for the sandbox-purchase
	// notify-back (e.g. ctech-poker) — admin-provisioned directly in SSM, the
	// same "no API write path" posture as the wallets table's fee/deposit-range
	// overrides (see services.M2MClient). Tolerant of being unset entirely:
	// most environments never configure an M2M sandbox-purchase client.
	m2mClientsParamFmt = "/ctech-wallet/%s/m2m-clients"
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

// LoadM2MClients fetches the raw M2M client registry JSON (plan: M2M
// sandbox-purchase integration). A missing parameter is NOT an error — it means no M2M sandbox-purchase client is
// registered in this environment yet, and callers should treat that as an
// empty registry, not fail startup.
func (s *Store) LoadM2MClients(ctx context.Context) (string, error) {
	name := fmt.Sprintf(m2mClientsParamFmt, s.env)
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		if _, ok := errors.AsType[*ssmtypes.ParameterNotFound](err); ok {
			return "", nil
		}
		return "", fmt.Errorf("ssm: get %s: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", nil
	}
	return *out.Parameter.Value, nil
}
