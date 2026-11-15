// Package workspacetrust decides whether a session may load the Claude config
// that lives in its working directory.
//
// A repository can ship .claude/settings.json, .claude/settings.local.json and
// .mcp.json. The Claude CLI honours them when a session runs with the project
// and local setting sources: hooks run shell commands, permissions.allow
// pre-approves tools, env and apiKeyHelper change how the CLI talks to the
// model, and .mcp.json starts MCP servers. A freshly cloned repository (or an
// agent that was steered into editing those files) must not get that power
// without a person looking at it, so Eggify only loads that config for folders
// the user trusted, and only as long as the config still matches what they
// trusted (its fingerprint).
//
// The package depends on the standard library only, so both the database
// migrations and the agent session manager can use it.
package workspacetrust

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ConfigFiles are the files, relative to the working directory, whose
// trust-relevant content is fingerprinted.
var ConfigFiles = []string{
	filepath.Join(".claude", "settings.json"),
	filepath.Join(".claude", "settings.local.json"),
	".mcp.json",
}

// settingsKeys are the settings-file keys that grant power to the folder's
// config: they run commands, pre-approve tools, change the model endpoint or
// credentials, widen file access or start MCP servers / plugins. Keys that only
// restrict (deny, ask, disableAllHooks) or are cosmetic are left out, so
// editing them never asks the user again.
var settingsKeys = []string{
	"hooks",
	"env",
	"apiKeyHelper",
	"statusLine",
	"awsAuthRefresh",
	"awsCredentialExport",
	"otelHeadersHelper",
	"enableAllProjectMcpServers",
	"enabledMcpjsonServers",
	"mcpServers",
	"enabledPlugins",
	"extraKnownMarketplaces",
}

// permissionKeys are the trust-relevant keys inside "permissions".
var permissionKeys = []string{"allow", "defaultMode", "additionalDirectories"}

// Hook is one command hook from a settings file.
type Hook struct {
	File    string `json:"file"`
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Type    string `json:"type,omitempty"`
	Command string `json:"command,omitempty"`
}

// Rule is one permissions.allow entry.
type Rule struct {
	File string `json:"file"`
	Rule string `json:"rule"`
}

// MCPServer is one MCP server a config file would start.
type MCPServer struct {
	File    string   `json:"file"`
	Name    string   `json:"name"`
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// EnvVar is one env entry. Values are shown: a repository that sets
// ANTHROPIC_BASE_URL is exactly what the user has to see.
type EnvVar struct {
	File  string `json:"file"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Setting is any other trust-relevant key, rendered as compact JSON.
type Setting struct {
	File  string `json:"file"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Inventory is what a folder's config would enable.
type Inventory struct {
	Path       string      `json:"path"`
	Files      []string    `json:"files"`
	Hooks      []Hook      `json:"hooks"`
	Allow      []Rule      `json:"allowed_tools"`
	MCPServers []MCPServer `json:"mcp_servers"`
	Env        []EnvVar    `json:"env"`
	Other      []Setting   `json:"other"`
	// Scripts are files inside the folder that the config runs or reads
	// (hook scripts, MCP server code, apiKeyHelper, .claude/hooks/*), with
	// their content hashes. They are part of the fingerprint.
	Scripts     []ScriptFile `json:"scripts"`
	Fingerprint string       `json:"fingerprint"`
	// LegacyFingerprint is the fingerprint before scripts were covered
	// (settings JSON only), used to upgrade records trusted back then.
	LegacyFingerprint string `json:"-"`
	// watch lists the script files and directories whose stat changes must
	// invalidate a cached scan.
	watch []string
}

// HasConfig reports whether the folder has any trust-relevant config.
func (inv *Inventory) HasConfig() bool { return inv != nil && inv.Fingerprint != "" }

// Summary is a one-line description, e.g. "2 hooks, 3 allowed tools, 1 MCP server".
func (inv *Inventory) Summary() string {
	if inv == nil {
		return ""
	}
	var parts []string
	add := func(n int, one, many string) {
		if n == 1 {
			parts = append(parts, "1 "+one)
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	add(len(inv.Hooks), "hook", "hooks")
	add(len(inv.Allow), "allowed tool", "allowed tools")
	add(len(inv.MCPServers), "MCP server", "MCP servers")
	add(len(inv.Env), "env variable", "env variables")
	add(len(inv.Other), "other setting", "other settings")
	add(len(inv.Scripts), "script file", "script files")
	return strings.Join(parts, ", ")
}

// Options tune a scan.
type Options struct {
	// EggifyBinaries are the commands that count as Eggify's own eggify-tools server.
	// A ".mcp.json" entry named "eggify-tools" that runs one of them with
	// ["mcp-server"] and nothing else was written by Eggify (older versions did so
	// in every session folder) and is left out of the fingerprint.
	EggifyBinaries []string
}

// DefaultEggifyBinaries lists the paths Eggify may have written into .mcp.json:
// the running executable, `eggify` on PATH and the usual install locations.
func DefaultEggifyBinaries() []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		out = append(out, p)
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			out = append(out, r)
		}
	}
	if p, err := os.Executable(); err == nil {
		add(p)
	}
	if p, err := exec.LookPath("eggify"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			add(abs)
		}
	}
	add("/usr/local/bin/eggify")
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".local", "bin", "eggify"))
		add(filepath.Join(home, "go", "bin", "eggify"))
	}
	out = append(out, "eggify")
	return out
}

// Canonical resolves dir to an absolute, symlink-free path, so trust granted
// to one spelling of a folder holds for every other spelling and a symlink
// cannot borrow another folder's trust.
func Canonical(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return filepath.Clean(abs), nil
}

// Scan reads the folder's config files and returns what they would enable.
// A folder without any trust-relevant config has an empty Fingerprint.
// A file that exists but cannot be parsed counts as config (its raw bytes are
// fingerprinted): the CLI's parser may be more lenient than ours.
func Scan(dir string, opts Options) (*Inventory, error) {
	canonical, err := Canonical(dir)
	if err != nil {
		return nil, err
	}
	inv := &Inventory{Path: canonical, Files: []string{}, Hooks: []Hook{}, Allow: []Rule{}, MCPServers: []MCPServer{}, Env: []EnvVar{}, Other: []Setting{}, Scripts: []ScriptFile{}}
	material := map[string]any{}

	for _, rel := range ConfigFiles {
		data, err := os.ReadFile(filepath.Join(canonical, rel))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || isDirErr(err) {
				continue
			}
			return nil, err
		}
		file := filepath.ToSlash(rel)
		var relevant map[string]any
		if rel == ".mcp.json" {
			relevant = extractMCPFile(file, data, opts, inv)
		} else {
			relevant = extractSettingsFile(file, data, inv)
		}
		if len(relevant) > 0 {
			material[file] = relevant
			inv.Files = append(inv.Files, file)
		}
	}

	if len(material) > 0 {
		// encoding/json sorts map keys, so the encoding is canonical.
		b, err := json.Marshal(material)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		inv.LegacyFingerprint = hex.EncodeToString(sum[:])

		// v2 also covers the files the config runs (see scripts.go).
		scripts, overCap, watch := scanScripts(canonical, inv)
		inv.watch = watch
		v2 := map[string]any{"config": material, "scripts": scripts}
		if overCap {
			inv.Other = append(inv.Other, Setting{File: HooksDir, Key: "(too many script files)",
				Value: fmt.Sprintf("the config refers to more than %d files or %d MiB; Eggify cannot fingerprint them, so the folder cannot stay trusted", maxScriptFiles, maxScriptBytes>>20)})
			v2["over_cap"] = unmatchableNonce()
		}
		if b, err = json.Marshal(v2); err != nil {
			return nil, err
		}
		sum = sha256.Sum256(b)
		inv.Fingerprint = FingerprintVersion + hex.EncodeToString(sum[:])
	}
	return inv, nil
}

func isDirErr(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "is a directory")
}

func decodeObject(data []byte) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, false
	}
	return obj, true
}

func unparsable(file string, data []byte, inv *Inventory) map[string]any {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	sum := sha256.Sum256(data)
	inv.Other = append(inv.Other, Setting{File: file, Key: "(unparsable file)", Value: "the file is not valid JSON; Eggify cannot show what it contains"})
	return map[string]any{"_raw_sha256": hex.EncodeToString(sum[:])}
}

func compact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func extractSettingsFile(file string, data []byte, inv *Inventory) map[string]any {
	obj, ok := decodeObject(data)
	if !ok {
		return unparsable(file, data, inv)
	}
	out := map[string]any{}
	for _, key := range settingsKeys {
		v, present := obj[key]
		if !present || isEmpty(v) {
			continue
		}
		out[key] = v
		switch key {
		case "hooks":
			inv.Hooks = append(inv.Hooks, listHooks(file, v)...)
		case "env":
			if m, ok := v.(map[string]any); ok {
				names := sortedKeys(m)
				for _, name := range names {
					val, isStr := m[name].(string)
					if !isStr {
						val = compact(m[name])
					}
					inv.Env = append(inv.Env, EnvVar{File: file, Name: name, Value: val})
				}
			} else {
				inv.Other = append(inv.Other, Setting{File: file, Key: key, Value: compact(v)})
			}
		case "mcpServers":
			inv.MCPServers = append(inv.MCPServers, listMCPServers(file, v, Options{})...)
		default:
			inv.Other = append(inv.Other, Setting{File: file, Key: key, Value: compact(v)})
		}
	}
	if perms, ok := obj["permissions"].(map[string]any); ok {
		p := map[string]any{}
		for _, key := range permissionKeys {
			v, present := perms[key]
			if !present || isEmpty(v) {
				continue
			}
			if key == "allow" {
				rules := stringList(v)
				// Order does not change what is allowed.
				sort.Strings(rules)
				rules = dedupe(rules)
				if len(rules) == 0 {
					continue
				}
				p[key] = rules
				for _, r := range rules {
					inv.Allow = append(inv.Allow, Rule{File: file, Rule: r})
				}
				continue
			}
			if key == "defaultMode" && v == "default" {
				continue
			}
			p[key] = v
			inv.Other = append(inv.Other, Setting{File: file, Key: "permissions." + key, Value: compact(v)})
		}
		if len(p) > 0 {
			out["permissions"] = p
		}
	}
	return out
}

func extractMCPFile(file string, data []byte, opts Options, inv *Inventory) map[string]any {
	obj, ok := decodeObject(data)
	if !ok {
		return unparsable(file, data, inv)
	}
	servers, _ := obj["mcpServers"].(map[string]any)
	kept := map[string]any{}
	for name, cfg := range servers {
		if name == "eggify-tools" && isEggifyOwnServer(cfg, opts.EggifyBinaries) {
			continue
		}
		kept[name] = cfg
	}
	if len(kept) == 0 {
		return nil
	}
	inv.MCPServers = append(inv.MCPServers, listMCPServers(file, kept, opts)...)
	return map[string]any{"mcpServers": kept}
}

// isEggifyOwnServer matches exactly the entry Eggify writes: a known eggify binary,
// args ["mcp-server"], optionally a description/type, and nothing else (no
// env, no extra args, no URL).
func isEggifyOwnServer(cfg any, binaries []string) bool {
	m, ok := cfg.(map[string]any)
	if !ok {
		return false
	}
	for k, v := range m {
		switch k {
		case "command", "args", "description":
		case "type":
			if v != "stdio" {
				return false
			}
		default:
			return false
		}
	}
	cmd, _ := m["command"].(string)
	args := stringList(m["args"])
	if len(args) != 1 || args[0] != "mcp-server" || cmd == "" {
		return false
	}
	for _, b := range binaries {
		if b != "" && cmd == b {
			return true
		}
	}
	return false
}

func listMCPServers(file string, v any, _ Options) []MCPServer {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	var out []MCPServer
	for _, name := range sortedKeys(m) {
		s := MCPServer{File: file, Name: name}
		if cfg, ok := m[name].(map[string]any); ok {
			s.Type, _ = cfg["type"].(string)
			s.Command, _ = cfg["command"].(string)
			s.Args = stringList(cfg["args"])
			s.URL, _ = cfg["url"].(string)
		}
		out = append(out, s)
	}
	return out
}

func listHooks(file string, v any) []Hook {
	events, ok := v.(map[string]any)
	if !ok {
		return []Hook{{File: file, Event: "(unrecognised)", Command: compact(v)}}
	}
	var out []Hook
	for _, event := range sortedKeys(events) {
		groups, ok := events[event].([]any)
		if !ok {
			out = append(out, Hook{File: file, Event: event, Command: compact(events[event])})
			continue
		}
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				out = append(out, Hook{File: file, Event: event, Command: compact(g)})
				continue
			}
			matcher, _ := gm["matcher"].(string)
			hooks, ok := gm["hooks"].([]any)
			if !ok {
				out = append(out, Hook{File: file, Event: event, Matcher: matcher, Command: compact(gm)})
				continue
			}
			for _, h := range hooks {
				hm, _ := h.(map[string]any)
				hook := Hook{File: file, Event: event, Matcher: matcher}
				if hm != nil {
					hook.Type, _ = hm["type"].(string)
					hook.Command, _ = hm["command"].(string)
					if hook.Command == "" {
						hook.Command = compact(hm)
					}
				} else {
					hook.Command = compact(h)
				}
				out = append(out, hook)
			}
		}
	}
	return out
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

func stringList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		} else {
			out = append(out, compact(it))
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}
