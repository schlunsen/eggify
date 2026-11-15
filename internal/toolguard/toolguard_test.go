package toolguard

import (
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		confirm  bool
		category Category
		effect   Effect
	}{
		// eggify-tools: spends money / uploads / deploys
		{"gpu_launch", true, CategorySpend, EffectGPUStart},
		{"gpu_resume", true, CategorySpend, EffectGPUStart},
		{"gpu_resize", true, CategorySpend, EffectNone},
		{"gpu_create_volume", true, CategorySpend, EffectNone},
		{"gpu_push", true, CategoryUpload, EffectNone},
		{"gpu_delete_volume", true, CategoryDestructive, EffectNone},
		{"gpu_terminate", true, CategoryDestructive, EffectGPURelease},
		{"deploy_app", true, CategoryDeploy, EffectNone},
		{"undeploy_app", true, CategoryDeploy, EffectNone},
		// eggify-tools: read-only or cheap → pass through
		{"gpu_stop", false, "", EffectGPURelease},
		{"gpu_status", false, "", EffectNone},
		{"gpu_pull", false, "", EffectNone},
		{"gpu_run", false, "", EffectNone},
		{"gpu_templates", false, "", EffectNone},
		{"gpu_list_volumes", false, "", EffectNone},
		{"list_deployed_apps", false, "", EffectNone},
		{"Bash", false, "", EffectNone},
		{"create_handover", false, "", EffectNone},
		{"store_memory", false, "", EffectNone},
		{"list_sessions", false, "", EffectNone},
		// RunPod connector
		{"runpod__create_pod", true, CategorySpend, EffectGPUStart},
		{"runpod__start_pod", true, CategorySpend, EffectGPUStart},
		{"runpod__update_pod", true, CategorySpend, EffectNone},
		{"runpod__resize_network_volume", true, CategorySpend, EffectNone},
		{"runpod__create_network_volume", true, CategorySpend, EffectNone},
		{"runpod__delete_pod", true, CategoryDestructive, EffectGPURelease},
		{"runpod__stop_pod", false, "", EffectGPURelease},
		{"runpod__list_pods", false, "", EffectNone},
		{"runpod__get_pod", false, "", EffectNone},
		{"runpod__pod_billing", false, "", EffectNone},
		{"runpod__list_templates", false, "", EffectNone},
		// n0's own tools
		{"n0_app_deploy", true, CategoryDeploy, EffectNone},
		{"n0_repo_write_files", true, CategoryWrite, EffectNone},
		{"n0_repo_read_file", false, "", EffectNone},
		{"n0_tasks_list", false, "", EffectNone},
		{"n0_app_status", false, "", EffectNone},
		{"n0_connection_info", false, "", EffectNone},
		// workspace connector tools
		{"ws__acme__whatsapp_relay__send_message", true, CategoryMessage, EffectNone},
		{"ws__acme__whatsapp_relay__send_whatsapp_template", true, CategoryMessage, EffectNone},
		{"ws__acme__vault__request_signature", true, CategorySign, EffectNone},
		{"ws__acme__vault__write_document_body", true, CategoryWrite, EffectNone},
		{"ws__acme__conference__delete_conference", true, CategoryDestructive, EffectNone},
		{"ws__acme__conference__invite_attendees", true, CategoryMessage, EffectNone},
		{"ws__acme__vault__list_signature_requests", true, CategorySign, EffectNone}, // conservative: "signature" is a verb word
		{"ws__acme__vault__read_document", false, "", EffectNone},
		{"ws__acme__vault__search", false, "", EffectNone},
		{"ws__acme__whatsapp_relay__relay_status", false, "", EffectNone},
		{"ws__acme__compute__create_pod", true, CategorySpend, EffectGPUStart},
		{"WS__X__Y__Send-Message", true, CategoryMessage, EffectNone},
		// "post" and "sign" are not matched inside other words; these are
		// not recognised reads either, so they fall to the default (ask).
		{"ws__acme__db__postgres_query", true, CategoryAction, EffectNone},
		{"ws__acme__ui__design_tokens", true, CategoryAction, EffectNone},
		{"ws__acme__db__query_postgres", false, "", EffectNone},
		// n0's own tools: listed reads pass, anything else asks
		{"n0_tasks_list", false, "", EffectNone},
		{"n0_board_list", false, "", EffectNone},
		{"n0_task_comments", false, "", EffectNone},
		{"n0_repo_list_files", false, "", EffectNone},
		{"n0_app_logs", false, "", EffectNone},
		{"n0_app_guide", false, "", EffectNone},
		{"n0_ci_runs", false, "", EffectNone},
		{"n0_task_comment", true, CategoryAction, EffectNone},
		{"n0_task_create", true, CategoryAction, EffectNone},
		{"n0_task_update", true, CategoryAction, EffectNone},
		{"n0_task_move", true, CategoryAction, EffectNone},
		{"n0_repo_create", true, CategoryAction, EffectNone},
		{"n0_some_future_tool", true, CategoryAction, EffectNone},
	}
	for _, tc := range cases {
		d := Classify(tc.name)
		if d.Confirm != tc.confirm || d.Category != tc.category || d.Effect != tc.effect {
			t.Errorf("Classify(%q) = confirm=%v category=%q effect=%q (rule %q); want confirm=%v category=%q effect=%q",
				tc.name, d.Confirm, d.Category, d.Effect, d.Rule, tc.confirm, tc.category, tc.effect)
		}
		if d.NeedsServer() != (tc.confirm || tc.effect != EffectNone) {
			t.Errorf("NeedsServer(%q) wrong", tc.name)
		}
	}
}

// Real n0 workspace tool names: side effects ask, clear reads pass.
func TestClassifyWorkspaceToolsDefaultToAsk(t *testing.T) {
	ask := []string{
		"ws__lunarrails__terminator_rover__rover_move_forward",
		"ws__lunarrails__terminator_rover__rover_move_backward",
		"ws__lunarrails__terminator_rover__rover_speak",
		"ws__lunarrails__terminator_rover__rover_take_photo",
		"ws__lunarrails__terminator_rover__rover_camera_up",
		"ws__lunarrails__terminator_rover__rover_join_huddle",
		"ws__lunarrails__terminator_rover__rover_stop",
		"ws__lunarrails__blender__execute_blender_code",
		"ws__lunarrails__blender__render_scene",
		"ws__lunarrails__blender__save_scene",
		"ws__lunarrails__blender__download_file",
		"ws__lunarrails__atlascloud__generate_music",
		"ws__lunarrails__atlascloud__generate_speech",
		"ws__lunarrails__atlascloud__generate_video",
		"ws__lunarrails__atlascloud__chat",
		"ws__lunarrails__partnership_vault_tools__set_document_fields",
		"ws__lunarrails__partnership_vault_tools__create_document",
		"ws__lunarrails__conference_organizer__update_rsvp",
		"ws__lunarrails__conference_organizer__create_conference",
		"ws__lunarrails__pascal_editor__create_wall",
		"ws__lunarrails__propbuilder__retry_vault_sync",
		"ws__lunarrails__whatsapp_relay__whatsapp_chat_link",
		"runpod__create_pod",
		"runpod__restart_pod",
		"runpod__ssh_exec",
		// a read prefix does not cover a mutating action
		"ws__acme__crm__get_or_create_contact",
		"ws__acme__crm__check_and_update",
		"ws__acme__crm__list_then_set_fields",
	}
	for _, n := range ask {
		if d := Classify(n); !d.Confirm {
			t.Errorf("Classify(%q) does not ask (rule %q)", n, d.Rule)
		}
	}
	pass := []string{
		"ws__lunarrails__terminator_rover__rover_status",
		"ws__lunarrails__terminator_rover__rover_huddle_status",
		"ws__lunarrails__atlascloud__get_video_status",
		"ws__lunarrails__atlascloud__list_models",
		"ws__lunarrails__atlascloud__list_artifacts",
		"ws__lunarrails__blender__get_scene",
		"ws__lunarrails__blender__search_polyhaven",
		"ws__lunarrails__callie__callie_health",
		"ws__lunarrails__callie__callie_stats",
		"ws__lunarrails__conference_organizer__get_app_status",
		"ws__lunarrails__ltx_video__check_ltx_health",
		"ws__lunarrails__lunartrack_admin__org_summary",
		"ws__lunarrails__pascal_editor_inspect__describe_node",
		"ws__lunarrails__pascal_editor_inspect__find_nodes",
		"ws__lunarrails__partnership_vault_tools__read_document",
		"ws__lunarrails__partnership_vault_tools__search",
		"runpod__list_pods",
		"runpod__get_pod",
	}
	for _, n := range pass {
		if d := Classify(n); d.Confirm {
			t.Errorf("Classify(%q) asks (rule %q), want pass-through", n, d.Rule)
		}
	}
	// Claude's built-in tools and eggify-tools' own tools are unaffected.
	for _, n := range []string{"Bash", "Write", "Edit", "WebFetch", "store_memory", "invoke_skill", "gpu_run", "create_handover"} {
		if d := Classify(n); d.Confirm {
			t.Errorf("Classify(%q) asks, want pass-through", n)
		}
	}
}

func TestRulesAreListedAndConsistent(t *testing.T) {
	rules := Rules()
	if len(rules) != len(eggifyTools)+len(connectorActions)+len(actionVerbs)+1 {
		t.Fatalf("Rules() lists %d rules", len(rules))
	}
	for _, r := range rules {
		var name string
		switch r.Kind {
		case "tool":
			name = r.Match
		case "action":
			name = "slug__" + r.Match
		case "verb":
			name = "slug__do_" + r.Match + "_thing"
		case "default":
			name = "slug__frobnicate"
		}
		d := Classify(name)
		if d.Confirm != r.Confirm || d.Category != r.Category || d.Effect != r.Effect {
			t.Errorf("rule %+v does not classify %q the same way: %+v", r, name, d)
		}
	}
}

func TestQuestionShowsToolArgsAndCost(t *testing.T) {
	q := Question("gpu_launch", map[string]interface{}{"tier": "pro", "template_id": "abc", "api_key": "sk-secret"})
	for _, want := range []string{"gpu_launch", "spends money", "tier: pro", "template_id: abc", "~$1.64/hr", "[redacted]"} {
		if !strings.Contains(q, want) {
			t.Errorf("question missing %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, "sk-secret") {
		t.Fatal("secret leaked into the question")
	}
	long := strings.Repeat("x", 5000)
	if s := SummarizeArgs(map[string]interface{}{"content": long}); len(s) > 400 {
		t.Fatalf("long value not truncated: %d chars", len(s))
	}
	if c := EstimateCost("gpu_create_volume", map[string]interface{}{"size_gb": float64(100)}); !strings.Contains(c, "$7.00") {
		t.Fatalf("volume cost = %q", c)
	}
	if EstimateCost("n0_repo_write_files", nil) != "" {
		t.Fatal("no cost expected for writes")
	}
	in := AskInput("deploy_app", nil)
	qs := in["questions"].([]interface{})
	opts := qs[0].(map[string]interface{})["options"].([]interface{})
	if opts[0].(map[string]interface{})["label"] != AnswerApprove || opts[1].(map[string]interface{})["label"] != AnswerDeny {
		t.Fatalf("unexpected options %+v", opts)
	}
}

func TestLimiterCaps(t *testing.T) {
	l := NewLimiter(Caps{MaxGPUPodsPerSession: 1, MaxGPULaunchesPerDay: 2})
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	l.now = func() time.Time { return now }

	if err := l.Reserve("a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Reserve("a"); err == nil || !strings.Contains(err.Error(), EnvMaxGPUPodsPerSession) {
		t.Fatalf("second concurrent pod should hit the session cap: %v", err)
	}
	l.Release("a")
	if err := l.Reserve("a"); err != nil {
		t.Fatalf("after release: %v", err)
	}
	if err := l.Reserve("b"); err == nil || !strings.Contains(err.Error(), EnvMaxGPULaunchesPerDay) {
		t.Fatalf("third launch today should hit the daily cap: %v", err)
	}
	l.Unreserve("a")
	if l.Active("a") != 0 {
		t.Fatal("unreserve should free the slot")
	}
	if err := l.Reserve("b"); err != nil {
		t.Fatalf("unreserve should give the daily launch back: %v", err)
	}
	now = now.Add(24 * time.Hour)
	if err := l.Reserve("c"); err != nil {
		t.Fatalf("new day: %v", err)
	}

	unlimited := NewLimiter(Caps{})
	for i := 0; i < 50; i++ {
		if err := unlimited.Reserve("x"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCapsFromEnv(t *testing.T) {
	t.Setenv(EnvMaxGPUPodsPerSession, "3")
	t.Setenv(EnvMaxGPULaunchesPerDay, "")
	c := CapsFromEnv()
	if c.MaxGPUPodsPerSession != 3 || c.MaxGPULaunchesPerDay != DefaultCaps().MaxGPULaunchesPerDay {
		t.Fatalf("caps = %+v", c)
	}
}
