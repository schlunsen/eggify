package agents

import (
	"errors"
	"sync"
	"sync/atomic"
)

// wsMaxPendingWrites bounds how many writes may wait for one connection. Every
// broadcast writes from its own goroutine, so a client that stops reading (a
// hung page) used to collect an unbounded pile of goroutines, each holding its
// payload, behind the connection's write mutex.
const wsMaxPendingWrites = 256

var (
	// errConnWriteClosed is returned for writes to a connection that already
	// failed a write or overflowed its queue and is being closed.
	errConnWriteClosed = errors.New("websocket: connection closed after a failed write")
	// errConnBackedUp is returned when a connection has too many writes waiting.
	errConnBackedUp = errors.New("websocket: too many pending writes, closing slow connection")
)

// connWriter serialises the writes to one WebSocket connection and closes the
// connection on the first failed write or when its queue overflows.
//
// A failed write leaves a websocket connection unusable (the library keeps
// returning the same error, and a partly written frame corrupts the stream),
// but the read loop kept running: the page could still send, never got an
// answer, and never reconnected. In the v2.6.12 incident every reply to the
// desktop page failed with ENOBUFS while list_sessions/load_messages kept
// arriving on the same socket. Closing it makes the client reconnect on a
// fresh socket instead.
type connWriter struct {
	mu        sync.Mutex
	pending   atomic.Int32
	broken    atomic.Bool
	closeOnce sync.Once
}

// write runs fn under the connection's write lock. closeConn is called once,
// asynchronously, when the connection has to be dropped.
func (w *connWriter) write(fn func() error, closeConn func()) error {
	if w.broken.Load() {
		return errConnWriteClosed
	}
	if n := w.pending.Add(1); n > wsMaxPendingWrites {
		w.pending.Add(-1)
		w.fail(closeConn)
		return errConnBackedUp
	}
	defer w.pending.Add(-1)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken.Load() {
		return errConnWriteClosed
	}
	if err := fn(); err != nil {
		w.fail(closeConn)
		return err
	}
	return nil
}

func (w *connWriter) fail(closeConn func()) {
	w.broken.Store(true)
	w.closeOnce.Do(func() {
		if closeConn != nil {
			go closeConn()
		}
	})
}

// isBroken reports whether the connection is being closed after a failed write.
func (w *connWriter) isBroken() bool { return w.broken.Load() }
