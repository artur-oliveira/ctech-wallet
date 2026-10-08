package alerting

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/api-commons/alerts"
)

func TestFromEnvWithoutTopicIsNop(t *testing.T) {
	t.Setenv("ALERTS_TOPIC_ARN", "")
	p := FromEnv(context.Background())
	if _, ok := p.(alerts.Nop); !ok {
		t.Fatalf("empty topic must yield alerts.Nop, got %T", p)
	}
	p.Alert(context.Background(), alerts.Alert{Job: "x", Summary: "y"}) // must not panic or block
}

type recorder struct{ got []alerts.Alert }

func (r *recorder) Alert(_ context.Context, a alerts.Alert) { r.got = append(r.got, a) }

func TestFailureBuildsAlert(t *testing.T) {
	r := &recorder{}
	Failure(context.Background(), r, "webhook", "confirm-deposit failed", errors.New("boom"), "txid=abc123")
	if len(r.got) != 1 {
		t.Fatalf("alerts = %d", len(r.got))
	}
	a := r.got[0]
	if a.Job != "webhook" || a.Summary != "confirm-deposit failed" || a.Detail != "txid=abc123" || a.Err == nil {
		t.Fatalf("alert = %+v", a)
	}
}

func TestFailureWithNilPublisherIsSafe(t *testing.T) {
	Failure(context.Background(), nil, "job", "summary", nil, "") // existing handlers are built without a publisher
}
