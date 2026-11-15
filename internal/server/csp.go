package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/net/html"

	"github.com/schlunsen/eggify/internal/logging"
)

// CSP modes, selected with the EGGIFY_CSP environment variable.
const (
	cspModeEnforce    = "enforce"     // default
	cspModeReportOnly = "report-only" // send Content-Security-Policy-Report-Only
	cspModeOff        = "off"         // no CSP header at all
)

// contentSecurityPolicy is the dashboard's CSP, computed once at startup.
//
// The SPA's generated HTML has a few inline <script> elements (Nuxt config,
// import map, console banner). Instead of 'unsafe-inline' they are allowed by
// their sha256 hash, so an injected inline script or event handler (e.g. from
// agent-written markdown or SVG that slipped past sanitization) does not run.
type contentSecurityPolicy struct {
	mode string
	// before/after the per-request connect-src origins.
	head, tail string
}

// Versions of the packages whose onnxruntime files are loaded from jsDelivr at
// run time. They must match internal/server/frontend/package-lock.json
// (TestCSPJsDelivrVersionsMatchPackageLock fails after an upgrade until they
// are updated here).
const (
	// @huggingface/transformers sets env.backends.onnx.wasm.wasmPaths to
	// .../@huggingface/transformers@<version>/dist/ (kokoro-js uses the same copy).
	transformersJSVersion = "3.7.6"
	// parakeet.js hardcodes .../onnxruntime-web@<version>/dist/ (the
	// onnxruntime-web version it pins as a dependency).
	parakeetORTVersion = "1.22.0-dev.20250409-89f8206ba4"
)

// jsDelivrSources are the only jsDelivr paths the dashboard may load scripts
// (and fetch .wasm) from. CSP path sources ending in "/" match that folder.
var jsDelivrSources = []string{
	"https://cdn.jsdelivr.net/npm/@huggingface/transformers@" + transformersJSVersion + "/dist/",
	"https://cdn.jsdelivr.net/npm/onnxruntime-web@" + parakeetORTVersion + "/dist/",
}

// webLLMModelLibSource is where @mlc-ai/web-llm fetches its prebuilt model
// libraries (connect-src only).
const webLLMModelLibSource = "https://raw.githubusercontent.com/mlc-ai/binary-mlc-llm-libs/"

// cspModeFromEnv reads EGGIFY_CSP (enforce | report-only | off).
func cspModeFromEnv() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EGGIFY_CSP"))) {
	case "off", "disabled", "false", "0", "none":
		return cspModeOff
	case "report-only", "report", "reportonly":
		return cspModeReportOnly
	default:
		return cspModeEnforce
	}
}

// newContentSecurityPolicy builds the policy, hashing the inline scripts of
// every .html file in the embedded frontend.
func newContentSecurityPolicy(public fs.FS, mode string) (*contentSecurityPolicy, error) {
	hashes, err := inlineScriptHashes(public)
	if err != nil {
		return nil, err
	}
	scriptSrc := []string{"'self'", "'wasm-unsafe-eval'"}
	for _, h := range hashes {
		scriptSrc = append(scriptSrc, "'"+h+"'")
	}
	// Transformers.js (Whisper, Kokoro TTS) and parakeet.js load the
	// onnxruntime wasm glue module from jsDelivr. Only those exact package
	// folders are allowed: all of cdn.jsdelivr.net would let injected markup
	// load any npm package or GitHub file as a script.
	scriptSrc = append(scriptSrc, jsDelivrSources...)

	head := strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scriptSrc, " "),
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' data: https://fonts.gstatic.com",
		// No remote images except the Refero style library (screenshots and
		// site icons) - markdown previews never load remote images.
		"img-src 'self' data: blob: https://*.refero.design https://*.gstatic.com",
		"media-src 'self' data: blob:",
		"worker-src 'self' blob:",
		"frame-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
	// Models for in-browser speech/TTS/tagging (Hugging Face, the onnxruntime
	// .wasm files next to the glue modules above, web-llm's model libraries on
	// GitHub) and @nuxt/icon's icon API.
	tail := "blob: data: https://huggingface.co https://*.huggingface.co https://*.hf.co " +
		strings.Join(jsDelivrSources, " ") + " " + webLLMModelLibSource + " https://api.iconify.design"
	return &contentSecurityPolicy{mode: mode, head: head, tail: tail}, nil
}

// headerName is the response header to send, or "" when CSP is off.
func (p *contentSecurityPolicy) headerName() string {
	if p == nil {
		return ""
	}
	switch p.mode {
	case cspModeOff:
		return ""
	case cspModeReportOnly:
		return fiber.HeaderContentSecurityPolicyReportOnly
	default:
		return fiber.HeaderContentSecurityPolicy
	}
}

// value renders the policy for a request to host. WebSocket connections are
// only allowed back to the host the page was loaded from (CSP 'self' does not
// cover ws:/wss: in every browser), not to arbitrary ws: hosts.
func (p *contentSecurityPolicy) value(host string) string {
	connect := "connect-src 'self'"
	if h := sanitizeCSPHost(host); h != "" {
		connect += " wss://" + h + " ws://" + h
	}
	return p.head + "; " + connect + " " + p.tail
}

// apply sets the CSP header on the response (no-op when off).
func (p *contentSecurityPolicy) apply(c *fiber.Ctx) {
	if name := p.headerName(); name != "" {
		c.Set(name, p.value(c.Hostname()))
	}
}

// sanitizeCSPHost returns host (with optional port) if it only contains
// characters valid in a host, so a crafted Host header can't add directives.
func sanitizeCSPHost(host string) string {
	if host == "" || len(host) > 255 {
		return ""
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == ':', r == '[', r == ']':
		default:
			return ""
		}
	}
	return host
}

// inlineScriptHashes returns the sorted, de-duplicated CSP hash sources
// ("sha256-…") of every executable inline <script> in the .html files of fsys.
// JSON data blocks are skipped: browsers don't execute them, so CSP doesn't
// apply to them.
func inlineScriptHashes(fsys fs.FS) ([]string, error) {
	seen := map[string]bool{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(path.Ext(p), ".html") {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		for _, h := range inlineScriptHashesInHTML(data) {
			seen[h] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// inlineScriptHashesInHTML hashes the text of each inline executable script
// in one HTML document, exactly as the browser sees it after parsing.
func inlineScriptHashesInHTML(doc []byte) []string {
	var hashes []string
	z := html.NewTokenizer(bytes.NewReader(doc))
	inScript := false
	var buf bytes.Buffer
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return hashes
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" {
				continue
			}
			src, typ := false, ""
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				switch string(k) {
				case "src":
					src = true
				case "type":
					typ = strings.ToLower(strings.TrimSpace(string(v)))
				}
			}
			inScript = !src && isExecutableScriptType(typ)
			buf.Reset()
		case html.TextToken:
			if inScript {
				buf.Write(z.Text())
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "script" {
				if inScript && buf.Len() > 0 {
					sum := sha256.Sum256(buf.Bytes())
					hashes = append(hashes, "sha256-"+base64.StdEncoding.EncodeToString(sum[:]))
				}
				inScript = false
			}
		}
	}
}

// isExecutableScriptType reports whether a <script type> runs (or, for import
// maps, is checked against script-src).
func isExecutableScriptType(typ string) bool {
	switch typ {
	case "", "module", "importmap", "text/javascript", "application/javascript",
		"text/ecmascript", "application/ecmascript", "speculationrules":
		return true
	}
	return false
}

// setupContentSecurityPolicy computes the CSP from the embedded frontend.
// Failure is logged and the server falls back to a policy without inline
// script hashes (the SPA's inline scripts would then be blocked, so this is
// loud).
func (s *Server) setupContentSecurityPolicy() {
	mode := cspModeFromEnv()
	public, err := embeddedPublicFS()
	if err == nil {
		s.csp, err = newContentSecurityPolicy(public, mode)
	}
	if err != nil {
		logging.Error("Content-Security-Policy: failed to hash inline scripts: %v", err)
		s.csp, _ = newContentSecurityPolicy(fstest.MapFS{}, mode)
	}
	switch mode {
	case cspModeOff:
		logging.Warning("Content-Security-Policy disabled (EGGIFY_CSP=off)")
	case cspModeReportOnly:
		logging.Warning("Content-Security-Policy in report-only mode (EGGIFY_CSP=report-only)")
	}
}
