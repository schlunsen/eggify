package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// Another Mac's dashboard is shown through the machine proxy (ADR 014) and
// calls this Mac's desktop app through /wails/*: the proxy serves it locally so
// the machine chip, the usage meter and the menu's shortcut events keep
// working. Wails v3 tells a binding nothing about where a call came from (no
// origin in the context), so the check has to happen before Wails sees the
// request. This filter is that check: on a machine-proxy origin only the
// bindings the switcher needs get through, everything else Wails offers to a
// page (the other DesktopService methods such as SelectFolder, RevealFolder,
// SetMenuShortcuts, InstallUpdate, OpenExternal and Notify, and Wails' own
// Clipboard, Browser, Dialog, Window, Application, System and Events
// requests) is refused with 403 before it reaches the runtime.

// machinePageBindings are the DesktopService bindings another Mac's page may
// call, by the name the frontend uses (rt.Call.ByName).
var machinePageBindings = map[string]bool{
	"main.DesktopService.Machines":                true,
	"main.DesktopService.SwitchMachine":           true,
	"main.DesktopService.MachineUsage":            true,
	"main.DesktopService.AddMachineFromClipboard": true, // asks for confirmation itself
}

// Wails' runtime request objects (messageprocessor.go): a binding call and
// the cancelling of one are the only ones a machine page may send.
const (
	wailsObjectCall       = 0
	wailsObjectCancelCall = 10
	wailsCallBinding      = 0
	wailsRuntimePath      = "/wails/runtime"
	maxMachinePageBody    = 64 << 10
)

type wailsRuntimeBody struct {
	Object *int            `json:"object"`
	Method *int            `json:"method"`
	Args   json.RawMessage `json:"args"`
}

type wailsCallArgs struct {
	CallID     string            `json:"call-id"`
	MethodName string            `json:"methodName"`
	Args       []json.RawMessage `json:"args"`
}

// filterMachinePageWails wraps the handler that serves /wails/* on a machine
// proxy's origin.
func filterMachinePageWails(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wailsRuntimePath {
			// runtime.js and the other assets are read-only files.
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			refuseMachinePageCall(w)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("x-wails-chunk-id") != "" || r.URL.RawQuery != "" {
			// No chunked bodies and no query-string calls (the runtime's
			// fallback form): a bound call here is a small JSON body.
			refuseMachinePageCall(w)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMachinePageBody))
		if err != nil {
			refuseMachinePageCall(w)
			return
		}
		canonical, ok := allowedMachinePageCall(raw)
		if !ok {
			refuseMachinePageCall(w)
			return
		}
		// Wails reads the canonical body this filter built, not the page's
		// bytes, so a body that two JSON parsers read differently (duplicate
		// keys, odd casing) cannot get a call past the check.
		r.Body = io.NopCloser(bytes.NewReader(canonical))
		r.ContentLength = int64(len(canonical))
		r.Header.Del("Content-Length")
		next.ServeHTTP(w, r)
	})
}

// allowedMachinePageCall returns the body to forward when raw is a call to an
// allowed binding by name, or a cancel request.
func allowedMachinePageCall(raw []byte) ([]byte, bool) {
	var body wailsRuntimeBody
	if err := json.Unmarshal(raw, &body); err != nil || body.Object == nil || body.Method == nil {
		return nil, false
	}
	switch *body.Object {
	case wailsObjectCancelCall:
		var a struct {
			CallID string `json:"call-id"`
		}
		if json.Unmarshal(body.Args, &a) != nil || a.CallID == "" {
			return nil, false
		}
		out, _ := json.Marshal(map[string]any{"object": wailsObjectCancelCall, "method": *body.Method, "args": map[string]any{"call-id": a.CallID}})
		return out, true
	case wailsObjectCall:
		if *body.Method != wailsCallBinding {
			return nil, false
		}
		var a wailsCallArgs
		if json.Unmarshal(body.Args, &a) != nil || a.CallID == "" || !machinePageBindings[a.MethodName] {
			return nil, false
		}
		if a.Args == nil {
			a.Args = []json.RawMessage{}
		}
		// Only the name: a methodID in the body could name another binding.
		out, _ := json.Marshal(map[string]any{
			"object": wailsObjectCall, "method": wailsCallBinding,
			"args": map[string]any{"call-id": a.CallID, "methodName": a.MethodName, "args": a.Args},
		})
		return out, true
	}
	return nil, false
}

func refuseMachinePageCall(w http.ResponseWriter) {
	http.Error(w, "this call is not available on another Mac's page", http.StatusForbidden)
}
