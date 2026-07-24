package inter

import (
	"context"
	"errors"
)

// bearerCtxKey carries the OAuth bearer api passes per call. The token travels
// in the Lambda Invoke payload and is seeded into the request context by the
// handler, then read by do/doIdem — so the PixClient method signatures stay
// token-free and the api-side interface never changes.
type bearerCtxKey struct{}

// WithBearer returns a copy of ctx carrying the bearer for this invocation.
func WithBearer(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerCtxKey{}, token)
}

// bearerFromContext returns the bearer seeded by the handler, or "".
func bearerFromContext(ctx context.Context) string {
	if t, ok := ctx.Value(bearerCtxKey{}).(string); ok {
		return t
	}
	return ""
}

// IsUnauthorized reports whether err is an Inter HTTP 401.
func IsUnauthorized(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.Code == 401
}

// ErrKeyNotFound means the destination PIX key on a Transfer call is not
// registered at the bank (Inter HTTP 404) — the one PixClient error callers
// must distinguish from a generic bank/transport failure (see rpc.ErrKeyNotFoundSentinel).
var ErrKeyNotFound = errors.New("inter: pix key not registered")

// IsKeyNotFound reports whether err wraps ErrKeyNotFound.
func IsKeyNotFound(err error) bool {
	return errors.Is(err, ErrKeyNotFound)
}
