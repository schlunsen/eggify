package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/perfmon"
)

func perfApp() (*fiber.App, *perfmon.Sampler) {
	s := perfmon.New(perfmon.Options{})
	h := NewPerfHandler(s)
	app := fiber.New()
	app.Get("/perf", h.HandleGetPerf)
	app.Post("/perf/client", h.HandlePostClient)
	app.Get("/system/usage", h.HandleGetUsage)
	return app, s
}

func TestPerfClientSampleIsStoredAndClamped(t *testing.T) {
	app, s := perfApp()
	body := `{"dom_nodes": 999999999999, "animations": -5, "fps": 59.9, "visible": true, "page": "` + strings.Repeat("x", 500) + `"}`
	req := httptest.NewRequest("POST", "/perf/client", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("status %v err %v", resp.StatusCode, err)
	}
	got := s.Report().Client
	if len(got) != 1 {
		t.Fatalf("stored %d", len(got))
	}
	c := got[0]
	if c.DOMNodes != 10_000_000 || c.Animations != 0 || len(c.Page) != 80 || c.FPS != 59.9 {
		t.Fatalf("not clamped: %+v", c)
	}
}

func TestPerfRejectsBadBody(t *testing.T) {
	app, _ := perfApp()
	req := httptest.NewRequest("POST", "/perf/client", strings.NewReader("{nope"))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestPerfReportShape(t *testing.T) {
	app, s := perfApp()
	s.Collect()
	resp, err := app.Test(httptest.NewRequest("GET", "/perf", nil))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("status %v err %v", resp.StatusCode, err)
	}
	var r perfmon.Report
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || len(r.Samples) != 1 || r.Interval <= 0 {
		t.Fatalf("bad report: %+v err %v", r, err)
	}
}

func TestSystemUsageShape(t *testing.T) {
	app, _ := perfApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/system/usage", nil))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("status %v err %v", resp.StatusCode, err)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control %q", cc)
	}
	var u map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cpu_percent", "mem_used_bytes", "mem_total_bytes", "cores", "eggify_cpu_percent", "eggify_rss_bytes", "available"} {
		if _, ok := u[k]; !ok {
			t.Fatalf("missing %s in %v", k, u)
		}
	}
}
