package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/changelog"
	"github.com/schlunsen/eggify/internal/version"
)

type changelogReply struct {
	Current  string              `json:"current"`
	Total    int                 `json:"total"`
	Releases []changelog.Release `json:"releases"`
}

func fixtureReleases(n int, unreleased bool) []changelog.Release {
	var rs []changelog.Release
	if unreleased {
		rs = append(rs, changelog.Release{Version: "Unreleased", Unreleased: true, Sections: []changelog.Section{{Title: "Added", Items: []string{"x"}}}})
	}
	for i := n; i >= 1; i-- {
		rs = append(rs, changelog.Release{Version: "1." + string(rune('0'+i)) + ".0", Date: "2026-01-01"})
	}
	return rs
}

func getChangelog(t *testing.T, h *ChangelogHandler, query string) (int, changelogReply) {
	t.Helper()
	app := fiber.New()
	app.Get("/changelog", h.HandleGetChangelog)
	resp, err := app.Test(httptest.NewRequest("GET", "/changelog"+query, nil))
	if err != nil {
		t.Fatal(err)
	}
	var out changelogReply
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func fixtureHandler(n int, unreleased bool) *ChangelogHandler {
	return &ChangelogHandler{releases: func() []changelog.Release { return fixtureReleases(n, unreleased) }}
}

func TestChangelogRealFileServes(t *testing.T) {
	status, out := getChangelog(t, NewChangelogHandler(), "")
	if status != 200 || out.Current != version.Version || out.Total == 0 || len(out.Releases) == 0 {
		t.Fatalf("status %d %+v", status, out)
	}
}

func TestChangelogLimitAndTotal(t *testing.T) {
	h := fixtureHandler(5, false)
	if _, out := getChangelog(t, h, ""); len(out.Releases) != 5 || out.Total != 5 {
		t.Fatalf("total %d", out.Total)
	}
	if _, out := getChangelog(t, h, "?limit=2"); len(out.Releases) != 2 || out.Total != 5 {
		t.Fatalf("limit=2 gave %d of %d", len(out.Releases), out.Total)
	}
	if _, out := getChangelog(t, h, "?limit=100000"); len(out.Releases) != 5 {
		t.Fatalf("limit: %d", len(out.Releases))
	}
	if _, out := getChangelog(t, h, "?limit=-3"); len(out.Releases) != 5 {
		t.Fatalf("negative limit: %d", len(out.Releases))
	}
	many := fixtureHandler(9, false)
	many.releases = func() []changelog.Release {
		var rs []changelog.Release
		for i := 0; i < 250; i++ {
			rs = append(rs, changelog.Release{Version: "1.0.0"})
		}
		return rs
	}
	if _, out := getChangelog(t, many, "?limit=100000"); len(out.Releases) != 200 || out.Total != 250 {
		t.Fatalf("cap: %d of %d", len(out.Releases), out.Total)
	}
}

func TestChangelogSince(t *testing.T) {
	h := fixtureHandler(5, false) // 1.5.0 … 1.1.0
	_, out := getChangelog(t, h, "?since=1.3.0")
	if out.Total != 2 || len(out.Releases) != 2 || out.Releases[0].Version != "1.5.0" {
		t.Fatalf("since: %+v", out)
	}
	if _, out := getChangelog(t, h, "?since=999.0.0"); out.Total != 0 {
		t.Fatalf("future since: %+v", out)
	}
}

func TestChangelogUnreleasedLeadsAndIsAlwaysNewer(t *testing.T) {
	h := fixtureHandler(3, true)
	_, out := getChangelog(t, h, "")
	if out.Total != 4 || !out.Releases[0].Unreleased || out.Releases[0].Version != "Unreleased" {
		t.Fatalf("%+v", out)
	}
	_, out = getChangelog(t, h, "?since=1.3.0")
	if out.Total != 1 || !out.Releases[0].Unreleased {
		t.Fatalf("since keeps Unreleased: %+v", out)
	}
}

func TestChangelogSinceGarbageIs400(t *testing.T) {
	for _, q := range []string{"?since=garbage", "?since=1.0.0;rm", "?since=..%2Fx"} {
		if status, _ := getChangelog(t, fixtureHandler(2, false), q); status != 400 {
			t.Errorf("%s -> %d", q, status)
		}
	}
}
