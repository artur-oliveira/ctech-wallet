// Package secrets loads pix-gateway's SSM SecureString parameters: the Inter
// mTLS keypair, the Inter OAuth client secret, and pix-gateway's own M2M client
// secret (used to call api's confirm-deposit endpoint). None are ever written
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
	interCertParamFmt        = "/ctech-wallet/%s/inter/mtls-cert"
	interKeyParamFmt         = "/ctech-wallet/%s/inter/mtls-key"
	interSecretParamFmt        = "/ctech-wallet/%s/inter/client-secret"
	pixGatewaySecretParamFmt   = "/ctech-wallet/%s/pix-gateway/client-secret"
	interWebhookSecretParamFmt = "/ctech-wallet/%s/inter/webhook-secret"
)

// SSMAPI is the subset of *ssm.Client this package needs (mockable in tests).
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Store reads pix-gateway's secrets from SSM.
type Store struct {
	client SSMAPI
	env    string
}

func NewStore(client SSMAPI, environment string) *Store {
	return &Store{client: client, env: environment}
}

// MTLSKeypair is the Inter client certificate and private key, in memory.
type MTLSKeypair struct {
	CertPEM []byte
	KeyPEM  []byte
}

// LoadInterMTLS fetches the Inter mTLS keypair. Both values are SecureStrings,
// so WithDecryption is always set.
func (s *Store) LoadInterMTLS(ctx context.Context) (*MTLSKeypair, error) {
	cert, err := s.get(ctx, fmt.Sprintf(interCertParamFmt, s.env))
	if err != nil {
		return nil, err
	}
	key, err := s.get(ctx, fmt.Sprintf(interKeyParamFmt, s.env))
	if err != nil {
		return nil, err
	}
	return &MTLSKeypair{CertPEM: []byte(cert), KeyPEM: []byte(key)}, nil
}

// LoadInterClientSecret fetches the Inter OAuth client secret.
func (s *Store) LoadInterClientSecret(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(interSecretParamFmt, s.env))
}

// LoadPixGatewayClientSecret fetches pix-gateway's own M2M client secret, used
// by the webhook Lambda to obtain a client_credentials token for calling api's
// confirm-deposit endpoint.
func (s *Store) LoadPixGatewayClientSecret(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(pixGatewaySecretParamFmt, s.env))
}

// LoadInterWebhookSecret fetches the static hmac value registered with
// Inter's webhook configuration — Inter echoes it back as a query parameter
// (?hmac=<secret>) on every callback; this is not a body signature.
func (s *Store) LoadInterWebhookSecret(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(interWebhookSecretParamFmt, s.env))
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
