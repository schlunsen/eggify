package agentruntime

import (
	"context"
	"encoding/json"

	"github.com/schlunsen/eggify/internal/codexapp"
)

// codexApp is the part of codexapp.Client the login flow and account check use.
type codexApp interface {
	Call(ctx context.Context, method string, params, result any) error
	Notify(method string, params any) error
	Events() <-chan codexapp.Message
	Close()
}

// startCodexApp starts `codex app-server` and completes the initialize
// handshake. Replaced in tests.
var startCodexApp = func(ctx context.Context, path string) (codexApp, error) {
	app, err := codexapp.Start(ctx, path, cliEnv(), homeDir())
	if err != nil {
		return nil, err
	}
	if err := app.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "eggify", "version": "1.0"}}, nil); err != nil {
		app.Close()
		return nil, err
	}
	if err := app.Notify("initialized", map[string]any{}); err != nil {
		app.Close()
		return nil, err
	}
	return app, nil
}

// readCodexAccount asks codex app-server for the signed-in account (plan type
// and e-mail). Read-only: refreshToken is false.
func readCodexAccount(ctx context.Context, path string) (*CodexAccount, error) {
	app, err := startCodexApp(ctx, path)
	if err != nil {
		return nil, err
	}
	defer app.Close()
	var res struct {
		Account *CodexAccount `json:"account"`
	}
	if err := app.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &res); err != nil {
		return nil, err
	}
	return res.Account, nil
}

// codexLoginCompleted is the account/login/completed notification.
type codexLoginCompleted struct {
	LoginID string `json:"loginId"`
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func parseCodexLoginCompleted(raw json.RawMessage) (codexLoginCompleted, bool) {
	var c codexLoginCompleted
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, false
	}
	return c, true
}
