package asaas

import (
	"context"
	"errors"
)

// ErrKeyNotFound mirrors pix.ErrKeyNotFound — a transfer's destination PIX key
// is not registered at the bank. Kept as a distinct sentinel (not an alias)
// because the two providers are independent failure domains, even though
// callers may want to treat them the same way.
var ErrKeyNotFound = errors.New("asaas: destination key not registered")

// AsaasClient is the surface the wallet service depends on for Asaas custody
// operations. One implementation per environment (fake for tests, Lambda-
// backed for real — plan §2.1/§2.2), same DI shape as pix.PixClient.
//
// apiKey is passed per call, never stored as client state, because every
// subaccount authenticates with its own key and the parent uses its own — a
// single AsaasClient instance is safe to share across all users.
type AsaasClient interface {
	CreateAccount(ctx context.Context, req CreateAccountRequest) (*Account, error)
	UploadDocument(ctx context.Context, subaccountAPIKey, documentID string, file []byte) error
	CreateStaticPixKey(ctx context.Context, subaccountAPIKey string) (*PixAddressKey, error)

	CreatePixQRCode(ctx context.Context, subaccountAPIKey string, req QRCodeRequest) (*QRCode, error)
	QueryPayment(ctx context.Context, apiKey, paymentID string) (*Payment, error)

	CreateTransfer(ctx context.Context, apiKey string, req TransferRequest) (*Transfer, error)
	// QueryTransfer looks a transfer up by its ExternalReference (real Asaas:
	// GET /v3/transfers?externalReference=..., plan §6) — never by Asaas's own
	// opaque transfer ID, since every caller in this codebase already knows the
	// reference it submitted and nothing else.
	QueryTransfer(ctx context.Context, apiKey, externalReference string) (*Transfer, error)

	// QueryAccountBalance reads a subaccount's (or the parent's) current Asaas
	// balance — the read side of Invariant #13's conservation check (plan §6).
	QueryAccountBalance(ctx context.Context, apiKey string) (int64, error)
}
