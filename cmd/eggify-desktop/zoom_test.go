package main

import (
	"path/filepath"
	"testing"
)

func TestNextZoom(t *testing.T) {
	cases := []struct {
		cur  float64
		dir  int
		want float64
	}{
		{1, 1, 1.1}, {1, -1, 0.9},
		{1.1, 1, 1.25}, {0.9, -1, 0.8},
		{3, 1, 3}, {0.5, -1, 0.5},
		{1.05, 1, 1.1}, {1.05, -1, 1}, // between steps: nearest step in that direction
	}
	for _, c := range cases {
		if got := nextZoom(c.cur, c.dir); got != c.want {
			t.Errorf("nextZoom(%v, %d) = %v, want %v", c.cur, c.dir, got, c.want)
		}
	}
}

func TestZoomPersistence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "eggify", "desktop-zoom.json")
	if got := loadZoom(p); got != 1 {
		t.Fatalf("missing file: got %v, want 1", got)
	}
	if err := saveZoom(p, 1.25); err != nil {
		t.Fatal(err)
	}
	if got := loadZoom(p); got != 1.25 {
		t.Fatalf("round trip: got %v", got)
	}
	if err := saveZoom(p, 40); err != nil {
		t.Fatal(err)
	}
	if got := loadZoom(p); got != 1 {
		t.Fatalf("out of range must fall back to 1, got %v", got)
	}
}
