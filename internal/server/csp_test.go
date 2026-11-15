package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v2"
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

const testPage = `<!DOCTYPE html><html><head>
<script type="importmap">{"imports":{"#entry":"/_nuxt/a.js"}}</script>
<script type="text/javascript">
  console.log('banner & <stuff>')
</script>
<script type="module" src="/_nuxt/a.js" crossorigin></script>
<script id="__NUXT_DATA__" type="application/json">[1,2]</script>
</head><body><div id="__nuxt"></div><script>window.__NUXT__={};</script></body></html>`

func TestInlineScriptHashesInHTML(t *testing.T) {
	got := inlineScriptHashesInHTML([]byte(testPage))
	want := []string{
		sha(`{"imports":{"#entry":"/_nuxt/a.js"}}`),
		sha("\n  console.log('banner & <stuff>')\n"),
		sha(`window.__NUXT__={};`),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hashes %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hash %d = %s, want %s", i, got[i], want[i])
		}
	}
	// External and JSON data scripts are not hashed.
	for _, h := range got {
		if h == sha("") || h == sha("[1,2]") {
			t.Errorf("unexpected hash %s", h)
		}
	}
}

func TestInlineScriptHashesCRLFNormalized(t *testing.T) {
	// Browsers hash the parsed text, where CRLF has become LF.
	got := inlineScriptHashesInHTML([]byte("<script>a()\r\nb()</script>"))
	if len(got) != 1 || got[0] != sha("a()\nb()") {
		t.Fatalf("got %v, want %s", got, sha("a()\nb()"))
	}
}

func TestInlineScriptHashesWalksAllHTML(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":       {Data: []byte(`<script>one()</script>`)},
		"200.html":         {Data: []byte(`<script>one()</script>`)},
		"sub/page.html":    {Data: []byte(`<script>two()</script>`)},
		"_nuxt/chunk.js":   {Data: []byte(`<script>notHtml()</script>`)},
		"_nuxt/readme.txt": {Data: []byte(`<script>nope()</script>`)},
	}
	got, err := inlineScriptHashes(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 de-duplicated hashes", got)
	}
	joined := strings.Join(got, " ")
	for _, s := range []string{"one()", "two()"} {
		if !strings.Contains(joined, sha(s)) {
			t.Errorf("missing hash of %s", s)
		}
	}
}

func TestEmbeddedFrontendInlineScriptsAreHashed(t *testing.T) {
	public, err := embeddedPublicFS()
	if err != nil {
		t.Fatal(err)
	}
	index, err := fs.ReadFile(public, "index.html")
	if err != nil {
		t.Skip("no embedded frontend build")
	}
	p, err := newContentSecurityPolicy(public, cspModeEnforce)
	if err != nil {
		t.Fatal(err)
	}
	v := p.value("localhost:3333")
	for _, h := range inlineScriptHashesInHTML(index) {
		if !strings.Contains(v, "'"+h+"'") {
			t.Errorf("policy misses inline script hash %s", h)
		}
	}
}

func TestPolicyDirectives(t *testing.T) {
	p, err := newContentSecurityPolicy(fstest.MapFS{"index.html": {Data: []byte(`<script>x()</script>`)}}, cspModeEnforce)
	if err != nil {
		t.Fatal(err)
	}
	v := p.value("localhost:3333")
	mustHave := []string{
		"default-src 'self'",
		"script-src 'self' 'wasm-unsafe-eval' '" + sha("x()") + "'",
		"object-src 'none'",
		"base-uri 'self'",
		"frame-ancestors 'none'",
		"worker-src 'self' blob:",
		"img-src 'self' data: blob:",
		"connect-src 'self' wss://localhost:3333 ws://localhost:3333 ",
	}
	for _, s := range mustHave {
		if !strings.Contains(v, s) {
			t.Errorf("policy missing %q\n%s", s, v)
		}
	}
	for _, s := range []string{"'unsafe-eval'", "connect-src 'self' ws: wss:", " https:;", "script-src 'self' 'unsafe-inline'"} {
		if strings.Contains(v, s) {
			t.Errorf("policy must not contain %q\n%s", s, v)
		}
	}
	if strings.Contains(strings.SplitN(strings.SplitN(v, "script-src", 2)[1], ";", 2)[0], "'unsafe-inline'") {
		t.Errorf("script-src allows 'unsafe-inline': %s", v)
	}
}

func TestPolicyRejectsHostileHost(t *testing.T) {
	p, _ := newContentSecurityPolicy(fstest.MapFS{}, cspModeEnforce)
	v := p.value("evil.com; script-src *")
	if strings.Contains(v, "evil.com") || strings.Contains(v, "script-src *") {
		t.Fatalf("host header leaked into policy: %s", v)
	}
	if !strings.Contains(v, "connect-src 'self' blob:") {
		t.Fatalf("connect-src should fall back to 'self': %s", v)
	}
}

func TestCSPModeFromEnv(t *testing.T) {
	cases := map[string]string{
		"":            cspModeEnforce,
		"enforce":     cspModeEnforce,
		"garbage":     cspModeEnforce,
		"off":         cspModeOff,
		"OFF":         cspModeOff,
		"report-only": cspModeReportOnly,
		"report":      cspModeReportOnly,
	}
	for in, want := range cases {
		t.Setenv("EGGIFY_CSP", in)
		if got := cspModeFromEnv(); got != want {
			t.Errorf("EGGIFY_CSP=%q -> %q, want %q", in, got, want)
		}
	}
}

func TestCSPHeaderOnResponses(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte(`<script>x()</script>`)}}
	for _, tc := range []struct {
		mode, header, absent string
	}{
		{cspModeEnforce, fiber.HeaderContentSecurityPolicy, fiber.HeaderContentSecurityPolicyReportOnly},
		{cspModeReportOnly, fiber.HeaderContentSecurityPolicyReportOnly, fiber.HeaderContentSecurityPolicy},
		{cspModeOff, "", fiber.HeaderContentSecurityPolicy},
	} {
		p, err := newContentSecurityPolicy(fsys, tc.mode)
		if err != nil {
			t.Fatal(err)
		}
		app := fiber.New()
		app.Use(func(c *fiber.Ctx) error { p.apply(c); return c.Next() })
		app.Get("/", func(c *fiber.Ctx) error {
			c.Set("Content-Type", "text/html")
			return c.SendString("<html></html>")
		})
		req := httptest.NewRequest("GET", "http://eggify.local:3333/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		if tc.header != "" {
			got := resp.Header.Get(tc.header)
			if !strings.Contains(got, "'"+sha("x()")+"'") || !strings.Contains(got, "wss://eggify.local:3333") {
				t.Errorf("mode %s: %s = %q", tc.mode, tc.header, got)
			}
		}
		if resp.Header.Get(tc.absent) != "" {
			t.Errorf("mode %s: unexpected %s header", tc.mode, tc.absent)
		}
		if tc.mode == cspModeOff && resp.Header.Get(fiber.HeaderContentSecurityPolicyReportOnly) != "" {
			t.Errorf("mode off: unexpected report-only header")
		}
	}
}

// directive returns the sources of one directive in policy v.
func directive(v, name string) []string {
	for _, d := range strings.Split(v, ";") {
		f := strings.Fields(d)
		if len(f) > 0 && f[0] == name {
			return f[1:]
		}
	}
	return nil
}

// jsDelivr (and GitHub raw) host whole npm registries / repositories: a bare
// host source would let injected markup load any package as a script.
func TestPolicyRestrictsCDNsToExactPaths(t *testing.T) {
	p, err := newContentSecurityPolicy(fstest.MapFS{}, cspModeEnforce)
	if err != nil {
		t.Fatal(err)
	}
	v := p.value("localhost:3333")
	for _, name := range []string{"script-src", "connect-src", "worker-src", "default-src"} {
		for _, src := range directive(v, name) {
			host := strings.TrimSuffix(strings.TrimPrefix(src, "https://"), "/")
			if host == "cdn.jsdelivr.net" || host == "raw.githubusercontent.com" || host == "*.jsdelivr.net" || src == "https:" || src == "*" {
				t.Errorf("%s allows a whole CDN host: %q\n%s", name, src, v)
			}
			if strings.HasPrefix(src, "https://cdn.jsdelivr.net/") && !strings.HasSuffix(src, "/dist/") {
				t.Errorf("%s jsDelivr source is not a pinned package folder: %q", name, src)
			}
		}
	}
	script := strings.Join(directive(v, "script-src"), " ")
	for _, want := range []string{
		"https://cdn.jsdelivr.net/npm/@huggingface/transformers@" + transformersJSVersion + "/dist/",
		"https://cdn.jsdelivr.net/npm/onnxruntime-web@" + parakeetORTVersion + "/dist/",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script-src misses %q: %s", want, script)
		}
		if !strings.Contains(strings.Join(directive(v, "connect-src"), " "), want) {
			t.Errorf("connect-src misses %q", want)
		}
	}
	if strings.Contains(script, "raw.githubusercontent.com") || strings.Contains(script, "huggingface.co") || strings.Contains(script, "hf.co") {
		t.Errorf("script-src must not allow model hosts: %s", script)
	}
}

// The pinned jsDelivr versions must follow the frontend's lockfile, or speech
// features break after an upgrade (the CDN path would be refused).
func TestCSPJsDelivrVersionsMatchPackageLock(t *testing.T) {
	data, err := os.ReadFile("frontend/package-lock.json")
	if err != nil {
		t.Skipf("no package-lock.json: %v", err)
	}
	var lock struct {
		Packages map[string]struct {
			Version      string            `json:"version"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if got := lock.Packages["node_modules/@huggingface/transformers"].Version; got != transformersJSVersion {
		t.Errorf("@huggingface/transformers is %q in package-lock.json, csp.go pins %q", got, transformersJSVersion)
	}
	if got := lock.Packages["node_modules/parakeet.js"].Dependencies["onnxruntime-web"]; got != parakeetORTVersion {
		t.Errorf("parakeet.js pins onnxruntime-web %q in package-lock.json, csp.go pins %q (check parakeet.js/src/backend.js)", got, parakeetORTVersion)
	}
}
