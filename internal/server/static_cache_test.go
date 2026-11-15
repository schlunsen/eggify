package server

import (
	"io/fs"
	"testing"
)

func TestImmutableAsset(t *testing.T) {
	for path, want := range map[string]bool{
		"/_nuxt/BfDzkIvk.js":                    true,
		"/_nuxt/entry.B5eNWLhz.css":             true,
		"/_nuxt/kokoro-worker-CcF9sTiv.js":      true,
		"/_nuxt/ort.bundle.min-Bd9_JzVL.js":     true,
		"/_nuxt/Dd--DdUV.js":                    true,
		"/_nuxt/builds/latest.json":             false,
		"/_nuxt/builds/meta/a83bce4b-6bb5.json": false,
		"/_nuxt/":                               false,
		"/_nuxt/noext":                          false,
		"/_nuxt/short.js":                       false,
		"/index.html":                           false,
		"/agents":                               false,
		"/zen/audio/track.mp3":                  false,
	} {
		if got := immutableAsset(path); got != want {
			t.Errorf("immutableAsset(%q) = %v, want %v", path, got, want)
		}
	}
}

// Every file Vite writes into /_nuxt/ (not the builds manifest) has a
// hashed name, so all of it is cached for good.
func TestImmutableAsset_CoversTheBuild(t *testing.T) {
	pub, err := embeddedPublicFS()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(pub, "_nuxt")
	if err != nil {
		t.Skip("no frontend build embedded")
	}
	for _, e := range entries {
		if !e.IsDir() && !immutableAsset("/_nuxt/"+e.Name()) {
			t.Errorf("/_nuxt/%s is not recognised as a hashed asset", e.Name())
		}
	}
}
