package agents

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnWriterClosesOnceAfterAFailedWrite(t *testing.T) {
	var w connWriter
	var closes atomic.Int32
	closed := make(chan struct{}, 4)
	closeFn := func() { closes.Add(1); closed <- struct{}{} }

	if err := w.write(func() error { return nil }, closeFn); err != nil {
		t.Fatalf("healthy write: %v", err)
	}
	enobufs := errors.New("write: no buffer space available")
	if err := w.write(func() error { return enobufs }, closeFn); !errors.Is(err, enobufs) {
		t.Fatalf("failed write returned %v", err)
	}
	// Later writes never reach the socket again.
	called := false
	if err := w.write(func() error { called = true; return nil }, closeFn); !errors.Is(err, errConnWriteClosed) {
		t.Fatalf("write after failure returned %v, want errConnWriteClosed", err)
	}
	if called {
		t.Fatal("write after failure reached the connection")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection was not closed after the failed write")
	}
	time.Sleep(20 * time.Millisecond)
	if n := closes.Load(); n != 1 {
		t.Fatalf("closed %d times, want 1", n)
	}
	if !w.isBroken() {
		t.Fatal("writer not marked broken")
	}
}

func TestConnWriterClosesABackedUpConnection(t *testing.T) {
	var w connWriter
	closed := make(chan struct{}, 1)
	closeFn := func() { closed <- struct{}{} }

	// One write hangs (a client that stopped reading); the rest queue behind it.
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = w.write(func() error { close(started); <-release; return nil }, closeFn)
	}()
	<-started

	var wg sync.WaitGroup
	var backedUp atomic.Int32
	for i := 0; i < wsMaxPendingWrites+10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.write(func() error { return nil }, closeFn); errors.Is(err, errConnBackedUp) {
				backedUp.Add(1)
			}
		}()
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("a backed-up connection was not closed")
	}
	close(release)
	wg.Wait()
	if backedUp.Load() == 0 {
		t.Fatal("no write was refused for overflowing the queue")
	}
	if !w.isBroken() {
		t.Fatal("writer not marked broken after overflow")
	}
}

func TestPermissionForwarderIsOnePerConnection(t *testing.T) {
	s := &AgentSession{}
	connA, connB := new(int), new(int)

	stopA, genA, ok := s.StartPermissionForwarder(connA)
	if !ok {
		t.Fatal("first start refused")
	}
	if _, _, ok := s.StartPermissionForwarder(connA); ok {
		t.Fatal("second start on the same connection spawned another forwarder")
	}

	// Moving to another connection stops the first forwarder.
	stopB, genB, ok := s.StartPermissionForwarder(connB)
	if !ok {
		t.Fatal("start on a new connection refused")
	}
	select {
	case <-stopA:
	default:
		t.Fatal("forwarder for the old connection was not stopped")
	}
	// The old forwarder exiting must not clear the new one.
	s.permissionForwarderExited(genA)
	if running, owner := s.permissionForwarderState(); !running || owner != connB {
		t.Fatalf("new forwarder lost after the old one exited: running=%v", running)
	}

	s.StopPermissionForwarder()
	select {
	case <-stopB:
	default:
		t.Fatal("StopPermissionForwarder did not stop the goroutine")
	}
	s.permissionForwarderExited(genB) // must not panic on a closed channel
	if running, _ := s.permissionForwarderState(); running {
		t.Fatal("still running after stop")
	}
}

// Every list_sessions/load_messages registers the listed sessions on the
// connection. On the same connection that must neither start another
// permission forwarder nor deny pending approvals.
func TestRepeatedListSessionsKeepsOneForwarderAndPendingApprovals(t *testing.T) {
	withEnforced(t, false)
	w := newWSHarness(t)
	conn := w.dial(t, "")
	sid := w.session.ID.String()

	for i := 0; i < 5; i++ {
		if err := conn.WriteJSON(map[string]any{"type": "list_sessions"}); err != nil {
			t.Fatal(err)
		}
		w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "sessions_list" })
	}
	running, _ := w.session.permissionForwarderState()
	if !running {
		t.Fatal("no permission forwarder after list_sessions")
	}
	w.session.permForwarderMu.Lock()
	gen := w.session.permForwarderGen
	w.session.permForwarderMu.Unlock()
	if gen != 1 {
		t.Fatalf("permission forwarder started %d times on one connection, want 1", gen)
	}

	ch := w.addPending("perm-keep")
	for i := 0; i < 3; i++ {
		if err := conn.WriteJSON(map[string]any{"type": "load_messages", "session_id": sid}); err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteJSON(map[string]any{"type": "list_sessions"}); err != nil {
			t.Fatal(err)
		}
		w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "sessions_list" })
	}
	select {
	case r := <-ch:
		t.Fatalf("pending approval was answered by a re-list: %+v", r)
	default:
	}
}
