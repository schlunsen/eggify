package remoteaccess

import (
	"context"
	"errors"
	"testing"

	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

type fakeTunnel struct {
	ngrok.Tunnel // unused methods panic if called
	closed       bool
}

func (t *fakeTunnel) URL() string  { return "https://fake.ngrok-free.app" }
func (t *fakeTunnel) Close() error { t.closed = true; return nil }

type fakeSession struct {
	ngrok.Session
	tunnel    *fakeTunnel
	listenErr error
	closed    bool
}

func (s *fakeSession) Listen(context.Context, config.Tunnel) (ngrok.Tunnel, error) {
	if s.listenErr != nil {
		return nil, s.listenErr
	}
	return s.tunnel, nil
}
func (s *fakeSession) Close() error { s.closed = true; return nil }

// Regression: the session context must stay alive after Open returns.
// Cancelling it on return killed each session right after it connected, and
// the reconnect loop opened a new one per second until ngrok refused with
// ERR_NGROK_108 (account agent-session limit).
func TestNgrokSessionOutlivesOpen(t *testing.T) {
	var sessCtx context.Context
	sess := &fakeSession{tunnel: &fakeTunnel{}}
	orig := ngrokConnect
	ngrokConnect = func(ctx context.Context, _ ...ngrok.ConnectOption) (ngrok.Session, error) {
		sessCtx = ctx
		return sess, nil
	}
	defer func() { ngrokConnect = orig }()

	ln, err := NewNgrokProvider("test").Open(context.Background(), OpenConfig{AuthToken: "tok"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if sessCtx.Err() != nil {
		t.Fatal("session context was cancelled when Open returned; the session would drop immediately")
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if sessCtx.Err() == nil || !sess.closed || !sess.tunnel.closed {
		t.Fatalf("Close must end the tunnel, the session and its context (ctxErr=%v sessClosed=%v tunClosed=%v)",
			sessCtx.Err(), sess.closed, sess.tunnel.closed)
	}
}

// A failed Listen must not leave the session or its context running.
func TestNgrokOpenFailureReleasesSession(t *testing.T) {
	var sessCtx context.Context
	sess := &fakeSession{listenErr: errors.New("boom")}
	orig := ngrokConnect
	ngrokConnect = func(ctx context.Context, _ ...ngrok.ConnectOption) (ngrok.Session, error) {
		sessCtx = ctx
		return sess, nil
	}
	defer func() { ngrokConnect = orig }()

	if _, err := NewNgrokProvider("test").Open(context.Background(), OpenConfig{AuthToken: "tok"}); err == nil {
		t.Fatal("expected an error")
	}
	if sessCtx.Err() == nil || !sess.closed {
		t.Fatalf("failed open must close the session and cancel its context (ctxErr=%v closed=%v)", sessCtx.Err(), sess.closed)
	}
}
