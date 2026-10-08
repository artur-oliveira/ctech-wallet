package wallet

// Audit event types. The ledger records money; this records everything else that
// must be provable after the fact — consent, activation, and every change to a
// personal gambling limit.
const (
	EventGamblingActivated        = "gambling_activated"
	EventSelfExcluded             = "self_excluded"
	EventSelfExclusionRevoked     = "self_exclusion_revoked"
	EventGameLimitsChanged        = "game_limits_changed"
	EventGamblingAddendumAccepted = "gambling_addendum_accepted"
	EventTermsAddendumAccepted    = "terms_addendum_accepted"
)

// AuditEvent is an immutable record of a non-money action. Like LedgerEntry it is
// append-only: never updated, never deleted. Before/After carry the change for
// events that mutate settings, and are empty for events that do not.
type AuditEvent struct {
	UserID    string `dynamodbav:"pk" json:"user_id"`
	SK        string `dynamodbav:"sk" json:"-"`
	EventID   string `dynamodbav:"event_id" json:"event_id"`
	EventType string `dynamodbav:"event_type" json:"event_type"`
	Actor     string `dynamodbav:"actor" json:"actor"`
	Before    string `dynamodbav:"before,omitempty" json:"before,omitempty"`
	After     string `dynamodbav:"after,omitempty" json:"after,omitempty"`
	IP        string `dynamodbav:"ip,omitempty" json:"ip,omitempty"`
	UserAgent string `dynamodbav:"user_agent,omitempty" json:"user_agent,omitempty"`
	CreatedAt string `dynamodbav:"created_at" json:"created_at"`
}
