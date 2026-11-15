package oauth

import "testing"

func TestParseAuthParam(t *testing.T) {
	h := `Bearer resource_metadata="https://nzero.pro/.well-known/oauth-protected-resource/api/v1/mcp", error="invalid_token"`
	if got := ParseResourceMetadataURL(h); got != "https://nzero.pro/.well-known/oauth-protected-resource/api/v1/mcp" {
		t.Fatalf("resource_metadata = %q", got)
	}
	if got := ParseAuthParam(h, "error"); got != "invalid_token" {
		t.Fatalf("error = %q", got)
	}
	if got := ParseAuthParam(`Bearer error=invalid_token, realm="x"`, "realm"); got != "x" {
		t.Fatalf("realm = %q", got)
	}
	if got := ParseResourceMetadataURL(""); got != "" {
		t.Fatalf("empty header = %q", got)
	}
}

func TestNormalizeResourceAndWellKnown(t *testing.T) {
	if normalizeResource("HTTPS://Nzero.pro/api/v1/mcp/?workspace=1") != normalizeResource("https://nzero.pro/api/v1/mcp") {
		t.Fatal("resource normalisation")
	}
	if u, _ := wellKnownURL("https://h/api/v1/mcp", "oauth-protected-resource"); u != "https://h/.well-known/oauth-protected-resource/api/v1/mcp" {
		t.Fatalf("well-known = %s", u)
	}
	if u, _ := wellKnownURL("https://h", "oauth-authorization-server"); u != "https://h/.well-known/oauth-authorization-server" {
		t.Fatalf("well-known issuer = %s", u)
	}
}

func TestPKCEAndAuthorizeURL(t *testing.T) {
	p, err := NewPKCE()
	if err != nil || len(p.Verifier) != 43 || p.Challenge != S256Challenge(p.Verifier) {
		t.Fatalf("pkce: %+v %v", p, err)
	}
	// RFC 7636 appendix B test vector.
	if S256Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal("S256 vector")
	}
	u, _ := AuthorizeURL("https://h/api/v1/oauth/authorize", AuthorizeParams{ClientID: "c", RedirectURI: "http://127.0.0.1:1/cb", State: "s", CodeChallenge: "x", Resource: "https://h/api/v1/mcp", Scopes: []string{"app:read", "app:code"}})
	for _, want := range []string{"code_challenge_method=S256", "resource=https%3A%2F%2Fh%2Fapi%2Fv1%2Fmcp", "response_type=code", "scope=app%3Aread+app%3Acode", "state=s"} {
		if !contains(u, want) {
			t.Errorf("authorize URL %s lacks %s", u, want)
		}
	}
	if CheckEndpoint("http://example.com") == nil || CheckEndpoint("http://127.0.0.1:9") != nil || CheckEndpoint("https://x") != nil {
		t.Fatal("CheckEndpoint")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
