package v1

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/billingclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
)

const eventSecret = "evt-secret"

var eventNow = time.Unix(1_800_000_000, 0)

type fakeLookup struct {
	inv *billingclient.Invoice
	err error
	ids []string
}

func (f *fakeLookup) InvoiceByID(_ context.Context, id string) (*billingclient.Invoice, error) {
	f.ids = append(f.ids, id)
	return f.inv, f.err
}

type fakeConfirm struct {
	txids []string
	err   error
}

func (f *fakeConfirm) ConfirmDeposit(_ context.Context, txid, _, _ string, sweep bool) error {
	f.txids = append(f.txids, txid)
	return f.err
}

func eventApp(t *testing.T, lookup *fakeLookup, confirm *fakeConfirm) *fiber.App {
	t.Helper()
	app := fiber.New()
	RegisterBillingEvents(app.Group("/v1.0"), &BillingEvents{Secret: eventSecret, Invoices: lookup, Confirm: confirm, Now: func() time.Time { return eventNow }})
	return app
}

func deliver(t *testing.T, app *fiber.App, body string, sign func(ts string, body []byte) string, at time.Time) int {
	t.Helper()
	ts := strconv.FormatInt(at.Unix(), 10)
	req := httptest.NewRequest("POST", "/v1.0"+PathBillingEvents, bytes.NewReader([]byte(body)))
	req.Header.Set(billingclient.HeaderTimestamp, ts)
	req.Header.Set(billingclient.HeaderSignature, sign(ts, []byte(body)))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func goodSig(ts string, body []byte) string { return "v1=" + billingclient.Sign(eventSecret, ts, body) }

const paidEvent = `{"id":"evt_1","type":"invoice.paid","data":{"object":"invoice","id":"in_1"}}`

func TestABadSignatureIsRefusedAndTouchesNothing(t *testing.T) {
	lookup, confirm := &fakeLookup{}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	forged := func(ts string, body []byte) string { return "v1=" + billingclient.Sign("another-secret", ts, body) }
	if code := deliver(t, app, paidEvent, forged, eventNow); code != 401 {
		t.Fatalf("status %d", code)
	}
	if len(lookup.ids) != 0 || len(confirm.txids) != 0 {
		t.Fatal("a forged event reached the ledger path")
	}
}

func TestAStaleDeliveryIsRefused(t *testing.T) {
	lookup, confirm := &fakeLookup{}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	if code := deliver(t, app, paidEvent, goodSig, eventNow.Add(-6*time.Minute)); code != 401 {
		t.Fatalf("status %d", code)
	}
	if len(confirm.txids) != 0 {
		t.Fatal("a replayed event reached the ledger path")
	}
}

func TestAPaidInvoiceWakesTheDepositItNamesInItsMetadata(t *testing.T) {
	lookup, confirm := &fakeLookup{inv: &billingclient.Invoice{ID: "in_1", Txid: "dep-1"}}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	if code := deliver(t, app, paidEvent, goodSig, eventNow); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(lookup.ids) != 1 || lookup.ids[0] != "in_1" || len(confirm.txids) != 1 || confirm.txids[0] != "dep-1" {
		t.Fatalf("lookups %v confirms %v", lookup.ids, confirm.txids)
	}
}

func TestTheEventBodyNeverNamesTheDeposit(t *testing.T) {
	// A body that tries to name a txid itself is ignored: only the invoice Billing
	// returns decides which deposit is woken.
	lookup, confirm := &fakeLookup{inv: &billingclient.Invoice{ID: "in_1", Txid: "dep-real"}}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	body := `{"type":"invoice.paid","txid":"dep-evil","data":{"object":"invoice","id":"in_1","txid":"dep-evil"}}`
	deliver(t, app, body, goodSig, eventNow)
	if len(confirm.txids) != 1 || confirm.txids[0] != "dep-real" {
		t.Fatalf("confirms %v", confirm.txids)
	}
}

func TestTwoDeliveriesOfOneEventAskTwiceAndLeaveTheCreditToTheServiceToDedupe(t *testing.T) {
	lookup, confirm := &fakeLookup{inv: &billingclient.Invoice{ID: "in_1", Txid: "dep-1"}}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	for i := 0; i < 2; i++ {
		if code := deliver(t, app, paidEvent, goodSig, eventNow); code != 200 {
			t.Fatalf("status %d", code)
		}
	}
	if len(confirm.txids) != 2 {
		t.Fatalf("confirms %v", confirm.txids)
	}
}

func TestOtherEventsAndOtherObjectsAreAcknowledgedAndIgnored(t *testing.T) {
	lookup, confirm := &fakeLookup{inv: &billingclient.Invoice{ID: "in_1", Txid: "dep-1"}}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	for _, body := range []string{
		`{"type":"invoice.finalized","data":{"object":"invoice","id":"in_1"}}`,
		`{"type":"invoice.paid","data":{"object":"subscription","id":"sub_1"}}`,
		`{"type":"invoice.paid","data":{"object":"invoice"}}`,
	} {
		if code := deliver(t, app, body, goodSig, eventNow); code != 200 {
			t.Fatalf("%s: status %d", body, code)
		}
	}
	if len(lookup.ids) != 0 || len(confirm.txids) != 0 {
		t.Fatal("an event that is not a paid invoice woke something")
	}
}

func TestAnInvoiceThatIsNotADepositIsAcknowledged(t *testing.T) {
	lookup, confirm := &fakeLookup{inv: &billingclient.Invoice{ID: "in_9"}}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	if code := deliver(t, app, paidEvent, goodSig, eventNow); code != 200 || len(confirm.txids) != 0 {
		t.Fatalf("status %d confirms %v", code, confirm.txids)
	}
}

func TestAnUnknownInvoiceIsAcknowledgedSoBillingStopsRetrying(t *testing.T) {
	lookup, confirm := &fakeLookup{err: billingclient.ErrNotFound}, &fakeConfirm{}
	app := eventApp(t, lookup, confirm)
	if code := deliver(t, app, paidEvent, goodSig, eventNow); code != 200 {
		t.Fatalf("status %d", code)
	}
}

func TestBillingBeingUnreadableAndADepositNotYetSettledAreRetried(t *testing.T) {
	app := eventApp(t, &fakeLookup{err: errors.New("503")}, &fakeConfirm{})
	if code := deliver(t, app, paidEvent, goodSig, eventNow); code < 500 {
		t.Fatalf("an unreadable invoice must be retried: %d", code)
	}
	app = eventApp(t, &fakeLookup{inv: &billingclient.Invoice{ID: "in_1", Txid: "dep-1"}}, &fakeConfirm{err: errors.New("payer not recorded yet")})
	if code := deliver(t, app, paidEvent, goodSig, eventNow); code < 500 {
		t.Fatalf("a deposit held for its payer must be retried: %d", code)
	}
}

// The route is mounted through Register, beside the /internal group whose prefix
// carries the JWT check. A delivery signed by Billing must reach the handler; the
// handler's own signature check, not the JWT, is what refuses a forgery; and the
// rest of /internal stays behind the JWT.
func TestTheEventRouteSitsBesideTheInternalGroupWithoutItsJWTCheck(t *testing.T) {
	lookup, confirm := &fakeLookup{inv: &billingclient.Invoice{ID: "in_1", Txid: "dep-1"}}, &fakeConfirm{}
	app := fiber.New()
	Register(app, cache.NewMemoryBackend(testCacheSize), nil, &config.Config{TablePrefix: "test"}, nil, nil, nil, nil, nil,
		WithBillingEvents(&BillingEvents{Secret: eventSecret, Invoices: lookup, Confirm: confirm, Now: func() time.Time { return eventNow }}))

	if code := deliver(t, app, paidEvent, goodSig, eventNow); code != 200 {
		t.Fatalf("a genuine delivery through the real router: %d (the JWT check is in front of it)", code)
	}
	if len(confirm.txids) != 1 || confirm.txids[0] != "dep-1" {
		t.Fatalf("confirms %v", confirm.txids)
	}
	forged := func(ts string, body []byte) string { return "v1=" + billingclient.Sign("nope", ts, body) }
	if code := deliver(t, app, paidEvent, forged, eventNow); code != 401 || len(confirm.txids) != 1 {
		t.Fatalf("a forgery: %d confirms %v", code, confirm.txids)
	}
	// The other internal routes still need their token.
	req := httptest.NewRequest("POST", "/v1.0/internal/wallet/charge", bytes.NewReader([]byte(`{}`)))
	resp, err := app.Test(req)
	if err != nil || resp.StatusCode != 401 {
		t.Fatalf("an internal route without a token: %v %v", resp, err)
	}
}

type fakeWithdrawals struct {
	calls int
	err   error
}

func (f *fakeWithdrawals) ReconcileWithdrawals(context.Context) (int, int, int, error) {
	f.calls++
	return 1, 0, 0, f.err
}

func payoutApp(t *testing.T, w *fakeWithdrawals) *fiber.App {
	t.Helper()
	app := fiber.New()
	e := &BillingEvents{Secret: eventSecret, Invoices: &fakeLookup{}, Confirm: &fakeConfirm{}, Now: func() time.Time { return eventNow }}
	if w != nil {
		e.Withdrawals = w
	}
	RegisterBillingEvents(app.Group("/v1.0"), e)
	return app
}

const payoutEvent = `{"id":"evt_9","type":"payout.done","data":{"object":"payout","id":"po_1"}}`

func TestASettledPayoutWakesTheWithdrawalsAndNothingInTheBodyDecidesAnything(t *testing.T) {
	w := &fakeWithdrawals{}
	app := payoutApp(t, w)
	if code := deliver(t, app, payoutEvent, goodSig, eventNow); code != 200 || w.calls != 1 {
		t.Fatalf("status %d calls %d", code, w.calls)
	}
	failed := `{"type":"payout.failed","data":{"object":"payout","id":"po_2"}}`
	if code := deliver(t, app, failed, goodSig, eventNow); code != 200 || w.calls != 2 {
		t.Fatalf("a failed payout wakes them too: %d %d", code, w.calls)
	}
}

func TestAPayoutEventIsRetriedWhenTheWithdrawalsCannotBeReconciled(t *testing.T) {
	app := payoutApp(t, &fakeWithdrawals{err: errors.New("table down")})
	if code := deliver(t, app, payoutEvent, goodSig, eventNow); code < 500 {
		t.Fatalf("status %d", code)
	}
}

func TestAForgedPayoutEventWakesNothingAndWithoutAReconcilerItIsIgnored(t *testing.T) {
	w := &fakeWithdrawals{}
	forged := func(ts string, body []byte) string { return "v1=" + billingclient.Sign("nope", ts, body) }
	if code := deliver(t, payoutApp(t, w), payoutEvent, forged, eventNow); code != 401 || w.calls != 0 {
		t.Fatalf("%d %d", code, w.calls)
	}
	if code := deliver(t, payoutApp(t, nil), payoutEvent, goodSig, eventNow); code != 200 {
		t.Fatalf("no reconciler: %d", code)
	}
}
