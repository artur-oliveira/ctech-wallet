// Package id generates ULIDs for wallet, ledger, deposit, and withdrawal ids.
// ULIDs are lexicographically sortable by creation time, which lets ledger
// entries use a time-ordered sort key without a separate timestamp field.
package id

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// New returns a new ULID string. Uses crypto/rand entropy; safe for concurrent
// use. This is production code, so time.Now is the correct clock source.
func New() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
