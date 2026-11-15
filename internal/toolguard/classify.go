// Package toolguard decides which agent tool calls are high-impact enough to
// need a human's approval in Eggify, no matter how the session is configured
// (yolo / bypassPermissions / always-allow rules do not apply here).
//
// The classifier is deliberately explicit and conservative:
//   - Eggify's own eggify-tools tools are listed by exact name.
//   - n0 tools (n0_*) and n0 workspace connector tools (<slug>__<tool>) are
//     matched on their action name: a fixed set of compute/billing actions
//     (create_pod, start_pod, ...) and a fixed set of verbs that send, sign,
//     deploy, write or delete things outside this machine.
//   - Any other n0 / workspace connector tool needs approval by default,
//     unless its action is clearly a read (list_*, get_*, read_*, search*,
//     *_status, ...) or one of n0's own read-only tools. Workspace apps can do
//     anything (move a robot, run code, generate paid media), so an unknown
//     action is treated as a side effect.
//   - Everything else (Eggify's other tools, Claude's built-in tools) passes
//     through.
//
// Rules() lists every rule so the set can be shown to users and reviewed.
package toolguard

import (
	"sort"
	"strings"
)

// Category says why a tool needs approval.
type Category string

const (
	CategorySpend       Category = "spend"           // starts or grows something that is billed
	CategoryUpload      Category = "upload"          // copies local data to a remote machine
	CategoryDeploy      Category = "deploy"          // publishes or removes a deployment
	CategoryMessage     Category = "send_message"    // sends a message / invitation to people
	CategorySign        Category = "signature"       // signs or requests a signature
	CategoryWrite       Category = "external_write"  // writes data in an external system
	CategoryDestructive Category = "destructive"     // deletes or destroys remote resources
	CategoryAction      Category = "external_action" // a workspace / n0 tool not known to be read-only
)

// Effect is the GPU spending-cap bookkeeping a tool call has.
type Effect string

const (
	EffectNone       Effect = ""
	EffectGPUStart   Effect = "gpu_start"   // starts a billed GPU pod (counts against the caps)
	EffectGPURelease Effect = "gpu_release" // stops/terminates a GPU pod (frees a slot)
)

// Decision is the classification of one tool name.
type Decision struct {
	Tool     string
	Confirm  bool     // the user must approve every call
	Category Category // why (empty when Confirm is false)
	Effect   Effect   // GPU cap bookkeeping
	Rule     string   // the rule that matched, for logs and Rules()
}

// NeedsServer reports whether the Eggify server must see this call (to ask the
// user or to keep the GPU caps up to date).
func (d Decision) NeedsServer() bool { return d.Confirm || d.Effect != EffectNone }

type rule struct {
	confirm  bool
	category Category
	effect   Effect
}

// eggifyTools are eggify-tools' own tools, matched by exact name.
var eggifyTools = map[string]rule{
	"gpu_launch":        {true, CategorySpend, EffectGPUStart},
	"gpu_resume":        {true, CategorySpend, EffectGPUStart},
	"gpu_resize":        {true, CategorySpend, EffectNone},
	"gpu_create_volume": {true, CategorySpend, EffectNone},
	"gpu_push":          {true, CategoryUpload, EffectNone},
	"gpu_delete_volume": {true, CategoryDestructive, EffectNone},
	"gpu_terminate":     {true, CategoryDestructive, EffectGPURelease},
	"gpu_stop":          {false, "", EffectGPURelease},
	"deploy_app":        {true, CategoryDeploy, EffectNone},
	"undeploy_app":      {true, CategoryDeploy, EffectNone},
}

// connectorActions are n0 / connector actions matched by their full action
// name (the part after the last "__", or after "n0_"), whatever the slug.
var connectorActions = map[string]rule{
	"create_pod":            {true, CategorySpend, EffectGPUStart},
	"start_pod":             {true, CategorySpend, EffectGPUStart},
	"resume_pod":            {true, CategorySpend, EffectGPUStart},
	"update_pod":            {true, CategorySpend, EffectNone},
	"resize_pod":            {true, CategorySpend, EffectNone},
	"create_network_volume": {true, CategorySpend, EffectNone},
	"resize_network_volume": {true, CategorySpend, EffectNone},
	"update_network_volume": {true, CategorySpend, EffectNone},
	"create_endpoint":       {true, CategorySpend, EffectNone},
	"update_endpoint":       {true, CategorySpend, EffectNone},
	"delete_pod":            {true, CategoryDestructive, EffectGPURelease},
	"terminate_pod":         {true, CategoryDestructive, EffectGPURelease},
	"stop_pod":              {false, "", EffectGPURelease},
}

// actionVerbs are words that, appearing as a word of an n0 / connector
// action name, make it need approval.
var actionVerbs = map[string]Category{
	"send":      CategoryMessage,
	"invite":    CategoryMessage,
	"post":      CategoryMessage,
	"publish":   CategoryDeploy,
	"deploy":    CategoryDeploy,
	"undeploy":  CategoryDeploy,
	"sign":      CategorySign,
	"signature": CategorySign,
	"write":     CategoryWrite,
	"delete":    CategoryDestructive,
	"destroy":   CategoryDestructive,
	"remove":    CategoryDestructive,
	"terminate": CategoryDestructive,
	"transfer":  CategorySpend,
	"pay":       CategorySpend,
	"purchase":  CategorySpend,
	"charge":    CategorySpend,
}

// readPrefixes are first words of an n0 / connector action that mark it as a
// read. "fetch" is deliberately not here: fetching can mean pulling something
// into a remote system.
var readPrefixes = map[string]bool{
	"list": true, "get": true, "read": true, "search": true, "find": true,
	"describe": true, "check": true, "query": true, "count": true, "view": true,
	"show": true,
}

// readSuffixes are last words of an n0 / connector action that mark it as a
// read (rover_status, callie_health, org_summary, ...).
var readSuffixes = map[string]bool{
	"status": true, "stats": true, "health": true, "info": true, "summary": true,
}

// mutatingWords override a read prefix/suffix: check_and_update or
// get_or_create still need approval.
var mutatingWords = map[string]bool{
	"create": true, "update": true, "set": true, "add": true, "edit": true,
	"modify": true, "put": true, "patch": true, "insert": true, "upsert": true,
	"move": true, "run": true, "exec": true, "execute": true, "generate": true,
	"start": true, "stop": true, "restart": true, "reset": true, "upload": true,
	"apply": true, "trigger": true, "approve": true, "reject": true,
	"cancel": true, "rename": true, "save": true, "import": true, "sync": true,
	"retry": true, "speak": true, "join": true, "leave": true, "submit": true,
	"enable": true, "disable": true, "grant": true, "revoke": true, "or": true,
	"and": true,
}

// n0ReadOnly are n0's own tools (without the n0_ prefix) that only read.
var n0ReadOnly = map[string]bool{
	"app_guide": true, "app_logs": true, "app_status": true, "board_list": true,
	"ci_runs": true, "connection_info": true, "repo_list_files": true,
	"repo_read_file": true, "task_comments": true, "tasks_list": true,
}

// readActions are connector actions known to only read, matched exactly
// (RunPod's billing reports).
var readActions = map[string]bool{
	"pod_billing": true, "network_volume_billing": true, "endpoint_billing": true,
}

// isReadAction reports whether an n0 / connector action is clearly a read.
func isReadAction(words []string) bool {
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if mutatingWords[w] {
			return false
		}
	}
	return readPrefixes[words[0]] || readSuffixes[words[len(words)-1]]
}

// IsProxiedName mirrors n0.IsProxiedTool without importing it: n0's own
// tools and workspace connector tools (<slug>__<tool>).
func isProxiedName(name string) bool {
	return strings.HasPrefix(name, "n0_") || strings.Contains(name, "__")
}

// actionOf returns the action part of an n0 / connector tool name.
func actionOf(name string) string {
	if i := strings.LastIndex(name, "__"); i >= 0 {
		return name[i+2:]
	}
	return strings.TrimPrefix(name, "n0_")
}

// Classify decides whether a tool call needs the user's approval.
func Classify(name string) Decision {
	d := Decision{Tool: name}
	if r, ok := eggifyTools[name]; ok {
		d.Confirm, d.Category, d.Effect, d.Rule = r.confirm, r.category, r.effect, "tool:"+name
		return d
	}
	if !isProxiedName(name) {
		return d
	}
	action := strings.ToLower(actionOf(name))
	if r, ok := connectorActions[action]; ok {
		d.Confirm, d.Category, d.Effect, d.Rule = r.confirm, r.category, r.effect, "action:"+action
		return d
	}
	words := strings.FieldsFunc(action, func(r rune) bool { return r == '_' || r == '-' })
	for _, word := range words {
		if c, ok := actionVerbs[word]; ok {
			d.Confirm, d.Category, d.Rule = true, c, "verb:"+word
			return d
		}
	}
	if strings.HasPrefix(name, "n0_") && n0ReadOnly[action] {
		d.Rule = "read:n0_" + action
		return d
	}
	if readActions[action] || isReadAction(words) {
		d.Rule = "read:" + action
		return d
	}
	// Not known to be read-only: ask.
	d.Confirm, d.Category, d.Rule = true, CategoryAction, "default:proxied"
	return d
}

// RuleInfo describes one classifier rule.
type RuleInfo struct {
	Kind     string   `json:"kind"`  // "tool" (exact eggify-tools name), "action" (exact n0/connector action), "verb" (word in an n0/connector action), "default" (any other n0/connector action not recognised as a read)
	Match    string   `json:"match"` // the name, action or verb
	Confirm  bool     `json:"confirm"`
	Category Category `json:"category,omitempty"`
	Effect   Effect   `json:"effect,omitempty"`
}

// Rules lists every rule, sorted by kind then match.
func Rules() []RuleInfo {
	out := make([]RuleInfo, 0, len(eggifyTools)+len(connectorActions)+len(actionVerbs)+1)
	for n, r := range eggifyTools {
		out = append(out, RuleInfo{Kind: "tool", Match: n, Confirm: r.confirm, Category: r.category, Effect: r.effect})
	}
	for n, r := range connectorActions {
		out = append(out, RuleInfo{Kind: "action", Match: n, Confirm: r.confirm, Category: r.category, Effect: r.effect})
	}
	for v, c := range actionVerbs {
		out = append(out, RuleInfo{Kind: "verb", Match: v, Confirm: true, Category: c})
	}
	out = append(out, RuleInfo{Kind: "default", Match: "n0 / workspace connector tools that are not clearly read-only", Confirm: true, Category: CategoryAction})
	order := map[string]int{"tool": 0, "action": 1, "verb": 2, "default": 3}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		return out[i].Match < out[j].Match
	})
	return out
}
