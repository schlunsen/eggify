package handlers

import "testing"

// The app subdomain feature needs both the sandbox's subdomain and the apps
// base domain; neither has a default.
func TestAppsNeedBothSubdomainAndBaseDomain(t *testing.T) {
	t.Setenv("EGGIFY_SUBDOMAIN", "xyz2")
	t.Setenv("EGGIFY_APPS_BASE_DOMAIN", "")
	if AppsEnabled() {
		t.Fatal("enabled without a base domain")
	}
	if h := NewAppsHandler(); h.subdomain != "" || h.baseDomain != "" {
		t.Fatalf("handler should be off: %+v", h)
	}
	t.Setenv("EGGIFY_SUBDOMAIN", "")
	t.Setenv("EGGIFY_APPS_BASE_DOMAIN", "example.com")
	if AppsEnabled() {
		t.Fatal("enabled without a subdomain")
	}
	t.Setenv("EGGIFY_SUBDOMAIN", "xyz2")
	for _, junk := range []string{".", " . ", "  "} {
		t.Setenv("EGGIFY_APPS_BASE_DOMAIN", junk)
		if AppsEnabled() {
			t.Fatalf("enabled with base domain %q", junk)
		}
	}
	t.Setenv("EGGIFY_APPS_BASE_DOMAIN", ".Example.com")
	h := NewAppsHandler()
	if !AppsEnabled() || h.subdomain != "xyz2" || h.baseDomain != "example.com" {
		t.Fatalf("handler = %+v", h)
	}
}
