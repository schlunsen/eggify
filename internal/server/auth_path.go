package server

import (
	"path"
	"strings"
)

// authPath normalises a request path for auth decisions: lower case (routing
// used to be case-insensitive, so GET /API/agent/sessions reached the
// /api/agent/sessions handler while a "/api/" prefix check missed it) and
// cleaned, with a leading "/".
//
// clean reports whether cleaning left the path unchanged apart from case and
// trailing slashes. Fiber routes on the raw path, so a path with "..", "./"
// or "//" in it is routed differently from how it cleans (a wildcard route
// such as /api/x/* could carry "../../sw.js"). Callers grant exemptions only
// when clean is true; anything else goes through the normal auth check.
func authPath(raw string) (p string, clean bool) {
	lower := strings.ToLower(raw)
	p = path.Clean("/" + lower)
	trimmed := strings.TrimRight(lower, "/")
	if trimmed == "" && strings.HasPrefix(lower, "/") {
		trimmed = "/"
	}
	return p, p == trimmed
}

// isAPIPath reports whether a normalised path (see authPath) is under /api.
// path.Clean drops the trailing slash, so "/api/" arrives as "/api".
func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}
