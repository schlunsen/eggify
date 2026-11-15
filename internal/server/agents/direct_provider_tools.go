package agents

// Tool exposure for the direct (OpenAI-compatible) provider path only.
// Claude SDK and Codex sessions never call anything in this file.
//
// eggify-tools offers 200+ tools once connectors are enabled (GPU, RunPod, n0,
// workspace connectors). Sending every schema on every call cost ~15k prompt
// tokens per request and made smaller models (Qwen, Llama, ...) choose tools
// badly. When the set is large, the direct path now sends only the core coding
// tools plus find_tools. The other tools are listed by name in the system
// prompt; find_tools loads their schemas on demand, and calling one by its
// exact name also works.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/schlunsen/eggify/internal/llmclient"
)

const (
	// findToolsName is handled by the direct loop itself, never sent to MCP.
	findToolsName = "find_tools"
	// findToolsMaxResults caps how many tools one find_tools call loads.
	findToolsMaxResults = 8
	// directAllToolsThreshold: tool sets this small are sent in full.
	directAllToolsThreshold = 30
	// directToolDescriptionMax caps descriptions in find_tools results.
	directToolDescriptionMax = 300
)

// directCoreTools are always sent with their full schemas.
var directCoreTools = map[string]bool{
	"Bash":            true,
	"Read":            true,
	"Write":           true,
	"Edit":            true,
	"Glob":            true,
	"Grep":            true,
	"store_memory":    true,
	"recall_memories": true,
}

// directToolSet is the tool view for one direct-provider turn.
type directToolSet struct {
	all      []llmclient.Tool          // every tool, discovery order
	byName   map[string]llmclient.Tool // every tool by name
	deferred bool                      // false: all tools are sent, no find_tools
	loaded   map[string]bool           // deferred tools whose schemas are sent
	core     map[string]bool           // extra tools always sent (invoke_skill when skills apply)
}

// isCore reports whether name is always sent with its schema.
func (ts *directToolSet) isCore(name string) bool {
	return directCoreTools[name] || ts.core[name]
}

// markCore always sends name (when it is a real tool) with its schema.
func (ts *directToolSet) markCore(name string) {
	if !ts.has(name) {
		return
	}
	if ts.core == nil {
		ts.core = map[string]bool{}
	}
	ts.core[name] = true
}

func newDirectToolSet(tools []llmclient.Tool, loaded map[string]bool) *directToolSet {
	ts := &directToolSet{
		all:      tools,
		byName:   make(map[string]llmclient.Tool, len(tools)),
		deferred: len(tools) > directAllToolsThreshold,
		loaded:   map[string]bool{},
	}
	for _, t := range tools {
		ts.byName[t.Function.Name] = t
	}
	for name := range loaded {
		if _, ok := ts.byName[name]; ok {
			ts.loaded[name] = true
		}
	}
	return ts
}

// offered returns the tools whose schemas go into the request.
func (ts *directToolSet) offered() []llmclient.Tool {
	if !ts.deferred {
		return ts.all
	}
	out := make([]llmclient.Tool, 0, len(directCoreTools)+len(ts.loaded)+1)
	for _, t := range ts.all {
		if ts.isCore(t.Function.Name) || ts.loaded[t.Function.Name] {
			out = append(out, t)
		}
	}
	return append(out, findToolsDefinition())
}

// deferredTools returns the tools not currently sent with a schema.
func (ts *directToolSet) deferredTools() []llmclient.Tool {
	if !ts.deferred {
		return nil
	}
	var out []llmclient.Tool
	for _, t := range ts.all {
		if !ts.isCore(t.Function.Name) && !ts.loaded[t.Function.Name] {
			out = append(out, t)
		}
	}
	return out
}

// has reports whether name is a real tool (offered or deferred).
func (ts *directToolSet) has(name string) bool {
	_, ok := ts.byName[name]
	return ok
}

// hasPrefix reports whether any tool name starts with prefix.
func (ts *directToolSet) hasPrefix(prefix string) bool {
	for _, t := range ts.all {
		if strings.HasPrefix(t.Function.Name, prefix) {
			return true
		}
	}
	return false
}

// load marks deferred tools as sent from now on and returns the newly loaded names.
func (ts *directToolSet) load(names ...string) []string {
	var added []string
	for _, n := range names {
		if !ts.deferred || ts.isCore(n) || ts.loaded[n] || !ts.has(n) {
			continue
		}
		ts.loaded[n] = true
		added = append(added, n)
	}
	return added
}

func findToolsDefinition() llmclient.Tool {
	return llmclient.Tool{
		Type: "function",
		Function: llmclient.Function{
			Name: findToolsName,
			Description: "Load more tools. Only the core tools are loaded at first; the system prompt lists every other tool by name. " +
				"Call this with keywords or tool names (e.g. \"gpu launch\", \"n0 tasks\", \"blender render\") to load up to 8 matching tools. " +
				"Loaded tools can be called from your next step on.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Keywords or exact tool names, space separated"}},"required":["query"]}`),
		},
	}
}

// findTools loads the deferred tools that best match query and returns the
// text the model gets back as the tool result.
func (ts *directToolSet) findTools(query string) string {
	matches := searchDirectTools(ts.deferredTools(), query, findToolsMaxResults)
	if len(matches) == 0 {
		return fmt.Sprintf("No unloaded tools matched %q. Tools you can load are listed by name in the system prompt; core tools are already loaded.", query)
	}
	names := make([]string, len(matches))
	for i, t := range matches {
		names[i] = t.Function.Name
	}
	ts.load(names...)
	var b strings.Builder
	fmt.Fprintf(&b, "Loaded %d tool(s). You can call them now:\n", len(matches))
	for _, t := range matches {
		fmt.Fprintf(&b, "- %s: %s\n", t.Function.Name, truncateRunes(oneLine(t.Function.Description), directToolDescriptionMax))
	}
	return strings.TrimRight(b.String(), "\n")
}

// searchDirectTools scores tools against the query terms: an exact name wins,
// then terms found in the name, then terms found in the description.
func searchDirectTools(tools []llmclient.Tool, query string, max int) []llmclient.Tool {
	q := strings.ToLower(strings.TrimSpace(query))
	terms := searchTerms(q)
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		tool  llmclient.Tool
		score int
	}
	var hits []scored
	for _, t := range tools {
		name := strings.ToLower(t.Function.Name)
		nameTerms := " " + strings.Join(searchTerms(name), " ") + " "
		desc := strings.ToLower(t.Function.Description)
		score := 0
		for _, field := range strings.Fields(q) {
			if field == name {
				score += 100
			}
		}
		for _, term := range terms {
			switch {
			case strings.Contains(nameTerms, " "+term+" "):
				score += 4
			case strings.Contains(name, term):
				score += 2
			case strings.Contains(desc, term):
				score++
			}
		}
		if score > 0 {
			hits = append(hits, scored{t, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].tool.Function.Name < hits[j].tool.Function.Name
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]llmclient.Tool, len(hits))
	for i, h := range hits {
		out[i] = h.tool
	}
	return out
}

// searchTerms splits on anything that is not a letter or digit (so "_" and
// "__" separate words) and drops one-letter noise.
func searchTerms(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := parts[:0]
	for _, p := range parts {
		if len(p) > 1 {
			out = append(out, p)
		}
	}
	return out
}

// deferredToolCatalog lists deferred tools by name, grouped, for the system
// prompt: "- ws__acme__blender: create_session, render_scene".
func deferredToolCatalog(tools []llmclient.Tool) string {
	if len(tools) == 0 {
		return ""
	}
	prefixCount := map[string]int{}
	for _, t := range tools {
		if p := underscorePrefix(t.Function.Name); p != "" {
			prefixCount[p]++
		}
	}
	groups := map[string][]string{}
	seps := map[string]string{}
	for _, t := range tools {
		name := t.Function.Name
		group, short, sep := "other", name, ""
		if i := strings.LastIndex(name, "__"); i > 0 {
			group, short, sep = name[:i], name[i+2:], "__"
		} else if p := underscorePrefix(name); p != "" && prefixCount[p] > 1 {
			group, short, sep = p, name[len(p)+1:], "_"
		}
		if prev, ok := seps[group]; ok && prev != sep {
			// "x__a" and "x_b" share a key: keep full names apart.
			group, short = "other", name
		} else {
			seps[group] = sep
		}
		groups[group] = append(groups[group], short)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "other") != (keys[j] == "other") {
			return keys[j] == "other"
		}
		return keys[i] < keys[j]
	})
	var b strings.Builder
	for _, k := range keys {
		if k == "other" {
			fmt.Fprintf(&b, "- %s\n", strings.Join(groups[k], ", "))
			continue
		}
		fmt.Fprintf(&b, "- %s%s*: %s\n", k, seps[k], strings.Join(groups[k], ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// underscorePrefix returns "gpu" for "gpu_launch" (single underscore names only).
func underscorePrefix(name string) string {
	if strings.Contains(name, "__") {
		return ""
	}
	if i := strings.Index(name, "_"); i > 0 {
		return name[:i]
	}
	return ""
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// directLoadedToolNames returns the deferred tools this session has loaded so
// far, so they stay loaded on later prompts (in memory; a restart resets it).
func (s *AgentSession) directLoadedToolNames() map[string]bool {
	s.directLoadedMu.Lock()
	defer s.directLoadedMu.Unlock()
	out := make(map[string]bool, len(s.directLoadedTools))
	for n := range s.directLoadedTools {
		out[n] = true
	}
	return out
}

func (s *AgentSession) rememberDirectLoadedTools(names map[string]bool) {
	s.directLoadedMu.Lock()
	defer s.directLoadedMu.Unlock()
	if s.directLoadedTools == nil {
		s.directLoadedTools = map[string]bool{}
	}
	for n := range names {
		s.directLoadedTools[n] = true
	}
}
