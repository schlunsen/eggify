package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/schlunsen/eggify/internal/llmclient"
)

func tool(name, desc string) llmclient.Tool {
	return llmclient.Tool{Type: "function", Function: llmclient.Function{
		Name: name, Description: desc, Parameters: json.RawMessage(`{"type":"object"}`),
	}}
}

// bigToolSet mirrors a real eggify-tools list with connectors enabled.
func bigToolSet() []llmclient.Tool {
	ts := []llmclient.Tool{
		tool("Bash", "Run a shell command"), tool("Read", "Read a file"), tool("Write", "Write a file"),
		tool("Edit", "Edit a file"), tool("Glob", "Find files"), tool("Grep", "Search files"),
		tool("store_memory", "Store a memory"), tool("recall_memories", "Recall memories"),
		tool("gpu_launch", "Launch a RunPod GPU pod"), tool("gpu_status", "GPU session status"),
		tool("n0_tasks_list", "Tasks on an n0 board"), tool("n0_app_status", "Status of the n0 app"),
		tool("runpod__create_pod", "Create a RunPod pod"),
		tool("ws__acme__blender__render_scene", "Render the Blender scene"),
		tool("ws__acme__blender__create_session", "Start a Blender session"),
		tool("deploy_app", "Deploy an app"),
	}
	for i := 0; len(ts) <= directAllToolsThreshold; i++ {
		ts = append(ts, tool(fmt.Sprintf("ws__acme__rover__move_%d", i), "Move the rover"))
	}
	return ts
}

func names(ts []llmclient.Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Function.Name
	}
	return out
}

func TestDirectToolSet_SmallSetIsSentWhole(t *testing.T) {
	small := []llmclient.Tool{tool("Bash", ""), tool("gpu_launch", ""), tool("deploy_app", "")}
	ts := newDirectToolSet(small, nil)
	if ts.deferred {
		t.Fatal("a small tool set must not be deferred")
	}
	if got := names(ts.offered()); strings.Join(got, ",") != "Bash,gpu_launch,deploy_app" {
		t.Fatalf("offered = %v, want every tool and no find_tools", got)
	}
	if ts.findTools("gpu") == "" || len(ts.deferredTools()) != 0 {
		t.Fatal("nothing is deferred in a small set")
	}
}

func TestDirectToolSet_LargeSetSendsCoreAndFindTools(t *testing.T) {
	ts := newDirectToolSet(bigToolSet(), nil)
	if !ts.deferred {
		t.Fatal("a large tool set must be deferred")
	}
	got := names(ts.offered())
	want := []string{"Bash", "Read", "Write", "Edit", "Glob", "Grep", "store_memory", "recall_memories", findToolsName}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("offered = %v, want %v", got, want)
	}
	if !ts.has("gpu_launch") || ts.has(findToolsName) || ts.has("nope") {
		t.Fatal("has() must know real tools only")
	}
}

func TestDirectToolSet_FindToolsLoadsMatches(t *testing.T) {
	ts := newDirectToolSet(bigToolSet(), nil)
	out := ts.findTools("blender render")
	if !strings.Contains(out, "ws__acme__blender__render_scene") {
		t.Fatalf("find_tools result missing the render tool:\n%s", out)
	}
	offered := strings.Join(names(ts.offered()), ",")
	if !strings.Contains(offered, "ws__acme__blender__render_scene") {
		t.Fatalf("render tool not offered after loading: %s", offered)
	}
	if top := ts.searchFirst("blender render"); len(top) != 1 || top[0].Function.Name != "ws__acme__blender__render_scene" {
		t.Fatalf("best match must be the render tool, got %v", names(top))
	}
	if !strings.Contains(ts.findTools("zzz-nothing"), "No unloaded tools matched") {
		t.Fatal("a miss must say so")
	}
}

// searchFirst is a test helper: the top match over all deferred tools.
func (ts *directToolSet) searchFirst(q string) []llmclient.Tool {
	all := append([]llmclient.Tool{}, ts.all...)
	return searchDirectTools(all, q, 1)
}

func TestSearchDirectTools_ExactNameWins(t *testing.T) {
	tools := bigToolSet()
	got := searchDirectTools(tools, "gpu_status", 3)
	if len(got) == 0 || got[0].Function.Name != "gpu_status" {
		t.Fatalf("exact name must rank first, got %v", names(got))
	}
	if got := searchDirectTools(tools, "  ", 3); got != nil {
		t.Fatalf("empty query must match nothing, got %v", names(got))
	}
	if got := searchDirectTools(tools, "rover", 2); len(got) != 2 {
		t.Fatalf("max must cap results, got %d", len(got))
	}
}

func TestDirectToolSet_LoadIgnoresCoreUnknownAndRepeats(t *testing.T) {
	ts := newDirectToolSet(bigToolSet(), map[string]bool{"deploy_app": true, "ghost": true})
	if !ts.loaded["deploy_app"] || ts.loaded["ghost"] {
		t.Fatalf("remembered tools must be loaded only if they exist: %v", ts.loaded)
	}
	if added := ts.load("Bash", "ghost", "deploy_app", "gpu_launch"); strings.Join(added, ",") != "gpu_launch" {
		t.Fatalf("load added %v, want only gpu_launch", added)
	}
}

func TestDeferredToolCatalog_GroupsByPrefix(t *testing.T) {
	ts := newDirectToolSet(bigToolSet(), nil)
	cat := deferredToolCatalog(ts.deferredTools())
	for _, want := range []string{
		"- gpu_*: launch, status",
		"- n0_*: tasks_list, app_status",
		"- runpod__*: create_pod",
		"- ws__acme__blender__*: render_scene, create_session",
		"- deploy_app",
	} {
		if !strings.Contains(cat, want) {
			t.Errorf("catalog missing %q:\n%s", want, cat)
		}
	}
	if strings.Contains(cat, "Bash") {
		t.Error("core tools must not be in the catalog")
	}
	if deferredToolCatalog(nil) != "" {
		t.Error("no deferred tools, no catalog")
	}
}

func TestDirectSystemPrompt_NoDuplicateSchemasAndWorkDir(t *testing.T) {
	ts := newDirectToolSet(bigToolSet(), nil)
	p := buildDefaultDirectSystemPrompt(ts, "/home/me/proj")
	if !strings.Contains(p, "Working directory: /home/me/proj") {
		t.Error("prompt must name the working directory")
	}
	if strings.Contains(p, "Launch a RunPod GPU pod") || strings.Contains(p, "Run a shell command") {
		t.Error("tool descriptions travel in the schemas, not the prompt")
	}
	if !strings.Contains(p, findToolsName) || !strings.Contains(p, "ws__acme__blender__*") {
		t.Error("prompt must list deferred tools and how to load them")
	}
	if !strings.Contains(p, "GPU tools:") {
		t.Error("GPU warning must stay while gpu_* tools exist")
	}
	if strings.Contains(p, "sandboxed") {
		t.Error("the direct path is not sandboxed; the prompt must not say so")
	}
	small := buildDefaultDirectSystemPrompt(newDirectToolSet([]llmclient.Tool{tool("Bash", "")}, nil), "")
	if strings.Contains(small, "GPU tools:") || strings.Contains(small, findToolsName) || strings.Contains(small, "Working directory") {
		t.Errorf("small set without gpu/workdir got extra sections:\n%s", small)
	}
}

func TestAgentSession_RemembersLoadedTools(t *testing.T) {
	s := &AgentSession{}
	if len(s.directLoadedToolNames()) != 0 {
		t.Fatal("new session has nothing loaded")
	}
	s.rememberDirectLoadedTools(map[string]bool{"gpu_launch": true})
	got := s.directLoadedToolNames()
	got["mutated"] = true
	if !s.directLoadedToolNames()["gpu_launch"] || s.directLoadedToolNames()["mutated"] {
		t.Fatal("loaded tools must persist and be returned as a copy")
	}
}
