package wallet

// CurrentTermsAddendumVersion is the wallet-specific ToS addendum version (see
// docs/legal/wallet-terms-addendum.md and ui/src/app/terms-addendum). Bump it to
// re-gate every user on their next /auth/me: acceptance is a computed equality
// against this constant, never a stored boolean, so a bump takes effect at once.
//
// The UI page renders this same version — keep the two in sync when bumping.
const CurrentTermsAddendumVersion = "1.0"

// KYC levels, mirrored from ctech-account's `kyc_level` claim. They live in the
// domain (not the middleware) because services gate on them too: activation
// requires `verified`, and the HTTP layer must not be the only place that knows
// what these strings mean.
const (
	KYCBasic    = "basic"
	KYCVerified = "verified"
)

// CurrentGamblingAddendumVersion is the responsible-gambling addendum version
// (see docs/legal/wallet-gambling-addendum.md and ui/src/app/gambling-addendum).
// It is a SEPARATE document from the wallet terms addendum: a user who accepted
// one has not accepted the other.
//
// Bumping it re-gates gambling for every user on their next call. A re-gated user
// keeps their game/sandbox balances and may still RETURN money to `real` — only
// funding and play are blocked. Money is never trapped by a terms change.
const CurrentGamblingAddendumVersion = "1.0"

// User holds the wallet-side per-user state. Identity itself lives in
// ctech-account; this row exists only to record which consent documents the user
// accepted, and when.
//
// Both acceptances live on ONE row, so every writer MUST update partially —
// a whole-row Put would silently revoke the other document's acceptance.
type User struct {
	UserID                  string `dynamodbav:"pk" json:"user_id"`
	TermsAddendumVersion    string `dynamodbav:"terms_addendum_version,omitempty" json:"-"`
	TermsAcceptedAt         string `dynamodbav:"terms_accepted_at,omitempty" json:"-"`
	GamblingAddendumVersion string `dynamodbav:"gambling_addendum_version,omitempty" json:"-"`
	GamblingActivatedAt     string `dynamodbav:"gambling_activated_at,omitempty" json:"-"`
	CreatedAt               string `dynamodbav:"created_at,omitempty" json:"-"`
	UpdatedAt               string `dynamodbav:"updated_at,omitempty" json:"-"`
}

// TermsAccepted reports whether the user has accepted the CURRENT addendum
// version. A user who accepted an older version must accept again.
func (u *User) TermsAccepted() bool {
	return u != nil && u.TermsAddendumVersion == CurrentTermsAddendumVersion
}

// GamblingAccepted reports whether the user accepted the CURRENT gambling
// addendum version. Like TermsAccepted, this is a computed equality — never a
// stored boolean — so bumping the constant re-gates everyone at once.
func (u *User) GamblingAccepted() bool {
	return u != nil && u.GamblingAddendumVersion == CurrentGamblingAddendumVersion
}
