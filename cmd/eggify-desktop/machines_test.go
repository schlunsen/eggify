package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/machineproxy"
	"github.com/schlunsen/eggify/internal/server/handlers"
)

func TestMachineStore_EncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	st := machineStore{path: filepath.Join(dir, machinesFile), key: key}
	if list, err := st.load(); err != nil || list != nil {
		t.Fatalf("missing file: %v %v", list, err)
	}
	in := []savedMachine{{ID: "abc", Name: "Studio", BaseURL: "https://abc.r.example.com", Pin: "pin", Token: "SECRET-TOKEN", Port: 51234, AddedAt: time.Now().UTC().Truncate(time.Second)}}
	if err := st.save(in); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(st.path)
	if strings.Contains(string(raw), "SECRET-TOKEN") || strings.Contains(string(raw), "Studio") {
		t.Fatalf("machines file is not encrypted: %s", raw)
	}
	if fi, _ := os.Stat(st.path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	out, err := st.load()
	if err != nil || len(out) != 1 || out[0].Token != "SECRET-TOKEN" || out[0].Port != 51234 {
		t.Fatalf("load: %+v %v", out, err)
	}
	other := machineStore{path: st.path, key: make([]byte, 32)}
	if _, err := other.load(); err == nil {
		t.Fatal("another key decrypted the machines file")
	}
}

func TestMachineManager_PairCodeKeepsTokenInside(t *testing.T) {
	mm := &machineManager{saved: []savedMachine{{ID: "abc", Name: "Studio", BaseURL: "https://abc.r.example.com", Pin: "pin", Token: "SECRET-TOKEN"}}}
	list := mm.List()
	if len(list) != 1 || list[0].ID != "abc" || list[0].Name != "Studio" || list[0].BaseURL != "https://abc.r.example.com" {
		t.Fatalf("list = %+v", list)
	}
	if b, _ := json.Marshal(list); strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "pin") {
		t.Fatalf("list leaks secrets: %s", b)
	}

	old := createPairing
	t.Cleanup(func() { createPairing = old })
	var gotToken, gotDevice string
	createPairing = func(_ context.Context, m machineproxy.Machine, token, device string) (*machineproxy.PairCode, error) {
		gotToken, gotDevice = token, device
		return &machineproxy.PairCode{PairURL: "eggify://pair?u=" + m.BaseURL + "&c=x&p=" + m.Pin, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	u, exp, err := mm.PairCode(context.Background(), "abc", "iPhone (added via MacBook)")
	if err != nil || !strings.HasPrefix(u, "eggify://pair?") || exp.IsZero() {
		t.Fatalf("pair code: %q %v %v", u, exp, err)
	}
	if gotToken != "SECRET-TOKEN" || gotDevice != "iPhone (added via MacBook)" {
		t.Fatalf("called with %q %q", gotToken, gotDevice)
	}
	if strings.Contains(u, "SECRET") {
		t.Fatal("token in the pair link")
	}
	if _, _, err := mm.PairCode(context.Background(), "nope", ""); !errors.Is(err, handlers.ErrPairedMachineNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
}
