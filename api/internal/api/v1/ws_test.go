package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/drain"

	fws "github.com/fasthttp/websocket"
)

func TestIsClientPing(t *testing.T) {
	if !isClientPing([]byte(`{"type":"ping"}`)) {
		t.Error("expected {\"type\":\"ping\"} to be detected as a client ping")
	}
	if isClientPing([]byte(`{"type":"pong"}`)) {
		t.Error("expected {\"type\":\"pong\"} not to be detected as a client ping")
	}
	if isClientPing([]byte(`not json`)) {
		t.Error("expected malformed input not to be detected as a client ping")
	}
}

func TestStartHeartbeat_MissingPongClosesReadLoop(t *testing.T) {
	const pingInterval = 20 * time.Millisecond
	const pongWait = 60 * time.Millisecond

	serverDone := make(chan struct{})
	upgrader := fws.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		defer conn.Close()
		heartbeatDone := make(chan struct{})
		go startHeartbeat(context.Background(), conn, heartbeatDone, pingInterval, pongWait, nil)
		_, _, _ = conn.ReadMessage() // blocks until the read deadline trips
		close(heartbeatDone)
		close(serverDone)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := (&fws.Dialer{}).Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()
	// Swallow the native ping instead of answering it, simulating a client
	// stuck without the browser's automatic pong reply.
	client.SetPingHandler(func(string) error { return nil })

	select {
	case <-serverDone:
		// expected: the server's read loop unblocked once the deadline tripped
	case <-time.After(2 * time.Second):
		t.Fatal("server did not close a connection that never sent a pong within pongWait")
	}
}

// Draining a tracker tells a registered connection to reconnect elsewhere
// (close 1001) and refuses new registrations.
func TestDrainAsksClientsToReconnect(t *testing.T) {
	var tracker drain.Tracker
	registered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&fws.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		tracker.Register("c1", func(context.Context) error { return closeGoingAway(conn) })
		close(registered)
		_, _, _ = conn.ReadMessage() // returns once closeGoingAway drops the socket
	}))
	defer srv.Close()

	client, _, err := (&fws.Dialer{}).Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()
	<-registered

	if err := tracker.Drain(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	_, _, err = client.ReadMessage()
	if !fws.IsCloseError(err, fws.CloseGoingAway) {
		t.Fatalf("client read error = %v, want close 1001", err)
	}
	if tracker.Register("c2", func(context.Context) error { return nil }) {
		t.Fatal("a connection registered after drain started")
	}
}
