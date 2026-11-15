package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestNotchGeometrySizes(t *testing.T) {
	g := notchGeometry{HasNotch: true, NotchWidth: 185.4, MenuHeight: 32}
	if w, h := g.size(false); w != 185+2*notchEar || h != 32 {
		t.Fatalf("collapsed = %dx%d", w, h)
	}
	if w, h := g.size(true); w != notchStageWidth || h != 32+notchStageHeight {
		t.Fatalf("expanded = %dx%d", w, h)
	}
	// No housing (external display): a pill in the middle of the menu bar.
	bare := notchGeometry{MenuHeight: 24}
	if w, h := bare.size(false); w != notchBareWidth || h != 24 {
		t.Fatalf("bare = %dx%d", w, h)
	}
	// Nonsense from AppKit falls back to safe values.
	odd := notchGeometry{HasNotch: true, NotchWidth: 5000, MenuHeight: 0}
	if w, h := odd.size(false); w != notchBareWidth || h != 24 {
		t.Fatalf("odd = %dx%d", w, h)
	}
}

func TestNotchPagePath(t *testing.T) {
	p := notchGeometry{HasNotch: true, NotchWidth: 200, MenuHeight: 37}.pagePath()
	if !strings.HasPrefix(p, notchPath+"?") || safeNext(p) != p {
		t.Fatalf("path %q is not a local page", p)
	}
	q, _ := url.ParseQuery(strings.TrimPrefix(p, notchPath+"?"))
	if q.Get("nw") != "200" || q.Get("mh") != "37" || q.Get("ear") != "104" {
		t.Fatalf("query = %v", q)
	}
}

func TestNotchOpensOnlyAgentPages(t *testing.T) {
	id := "0b8f5a2e-3c1d-4e5f-8a9b-1c2d3e4f5a6b"
	if got := notchSessionPath(id); got != "/agents?session="+id {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"", "../settings", "x&next=//evil", id + "/../../api"} {
		if got := notchSessionPath(bad); got != "/agents" {
			t.Fatalf("%q → %q", bad, got)
		}
	}
}

func TestNotchPrefsDefaultOnAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if !loadNotchPrefs(dir).Enabled {
		t.Fatal("the theatre should be on by default")
	}
	if err := saveNotchPrefs(dir, notchPrefs{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if loadNotchPrefs(dir).Enabled {
		t.Fatal("saved off, loaded on")
	}
}
