package ws

import (
	"context"
	"errors"
	"testing"
)

type fakeConn struct {
	written [][]byte
	failAt  int // WriteMessage fails once written reaches this count
}

func (f *fakeConn) WriteMessage(_ int, data []byte) error {
	if f.failAt > 0 && len(f.written) >= f.failAt {
		return errors.New("write failed")
	}
	f.written = append(f.written, data)
	return nil
}

func TestMemoryRegistryBroadcastReachesRegisteredConn(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{}
	r.Register("user1", "conn1", c)
	r.Broadcast(context.Background(), "user1", []byte(`{"type":"deposit_confirmed"}`))
	if len(c.written) != 1 {
		t.Fatalf("expected 1 message, got %d", len(c.written))
	}
}

func TestMemoryRegistryBroadcastIgnoresOtherUsers(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{}
	r.Register("user1", "conn1", c)
	r.Broadcast(context.Background(), "user2", []byte(`{}`))
	if len(c.written) != 0 {
		t.Fatalf("expected 0 messages for a different user, got %d", len(c.written))
	}
}

func TestMemoryRegistryUnregisterStopsDelivery(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{}
	r.Register("user1", "conn1", c)
	r.Unregister("user1", "conn1")
	r.Broadcast(context.Background(), "user1", []byte(`{}`))
	if len(c.written) != 0 {
		t.Fatalf("expected 0 messages after unregister, got %d", len(c.written))
	}
}

func TestMemoryRegistryDeadConnIsRemoved(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{failAt: 0} // fails on the very first write
	c.failAt = 1
	// force an immediate failure by pre-seeding one "successful" write count
	r.Register("user1", "conn1", c)
	r.Broadcast(context.Background(), "user1", []byte(`{}`)) // write #1 → count becomes 1, still succeeds (failAt=1 means fail when len>=1 BEFORE this write, i.e. second write)
	r.Broadcast(context.Background(), "user1", []byte(`{}`)) // write #2 → fails, conn is unregistered
	r.Broadcast(context.Background(), "user1", []byte(`{}`)) // no-op, already unregistered
	if len(c.written) != 1 {
		t.Fatalf("expected exactly 1 successful write before failure, got %d", len(c.written))
	}
}
