// Package eggify holds files from the repository root that the binary embeds.
package eggify

import _ "embed"

// Changelog is the repository's CHANGELOG.md, the single source of the in-app "What's new".
//
//go:embed CHANGELOG.md
var Changelog []byte
