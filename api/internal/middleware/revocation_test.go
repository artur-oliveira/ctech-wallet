package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
)

type downCache struct{ cache.Backend }

func (downCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("valkey down")
}

func TestRequireUnrevoked(t *testing.T) {
	rev := cache.NewMemoryBackend(10)
	cutoff := time.Now()
	if err := jwtverify.Revoke(context.Background(), rev, "u-locked", cutoff, jwtverify.RevocationTTL); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		claim *Claims
		rev   cache.Backend
		want  int
	}{
		{"active user", &Claims{Sub: "u-ok", SID: "s", IssuedAt: cutoff.Unix()}, rev, 200},
		{"token of a locked user", &Claims{Sub: "u-locked", SID: "s", IssuedAt: cutoff.Unix()}, rev, 401},
		{"token issued after the cut-off (request cancelled)", &Claims{Sub: "u-locked", SID: "s", IssuedAt: cutoff.Add(time.Minute).Unix()}, rev, 200},
		{"revocation list unreachable fails closed", &Claims{Sub: "u-ok", SID: "s", IssuedAt: cutoff.Unix()}, downCache{}, 503},
		{"no backend configured (route tests)", &Claims{Sub: "u-ok", SID: "s"}, nil, 200},
		{"no claims", nil, rev, 401},
	}
	for _, tc := range cases {
		if got := gateApp(t, tc.claim, RequireUnrevoked(tc.rev)); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}
