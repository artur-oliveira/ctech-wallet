// Package secrets loads the Inter partner-bank mTLS keypair from SSM Parameter
// Store (SecureString), holding it in memory only — it is never written to disk.
//
// Only the certificate and key live here. The short secrets (Inter OAuth client
// secret, PIX webhook secret) arrive as env vars exported by start.sh from SSM,
// matching ctech-account's GOOGLE_CLIENT_SECRET pattern. The keypair is fetched
// with the SDK instead so a bank certificate can be rotated without a redeploy,
// and so a multi-KB PEM never has to travel through shell/systemd.
//
// Mirrors ctech-account's keystore.SSMAPI shape, including the mockable interface.
// Cert and key are SEPARATE parameters: a SecureString value is capped at 4 KB on
// the standard tier, and a PEM cert (~2 KB) plus its key (~1.7 KB) would sit at
// or over that limit if combined.
package secrets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Parameter paths. %s is the deployment environment (dev/stage/prod).
const (
	interCertParamFmt   = "/ctech-wallet/%s/inter/mtls-cert"
	interKeyParamFmt    = "/ctech-wallet/%s/inter/mtls-key"
	interSecretParamFmt = "/ctech-wallet/%s/inter/client-secret"
)

// SSMAPI is the subset of *ssm.Client this package needs (mockable in tests).
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Store reads the wallet's secrets from SSM.
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
//
// On EC2 this value arrives as an env var (exported by start.sh), but the
// reconciliation Lambda has no start.sh — it must read the secret itself, and a
// SecureString must never be resolved into a CloudFormation template.
func (s *Store) LoadInterClientSecret(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(interSecretParamFmt, s.env))
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
