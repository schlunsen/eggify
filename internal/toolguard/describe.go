package toolguard

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Answers offered in the approval question.
const (
	AnswerApprove = "Approve"
	AnswerDeny    = "Deny"
)

// Header is the short chip shown above the approval question.
const Header = "Approval"

const (
	maxArgs        = 12
	maxValueChars  = 160
	maxArgsSummary = 1500
)

var secretKeyWords = []string{"token", "secret", "password", "passwd", "api_key", "apikey", "authorization", "credential", "private_key"}

func isSecretKey(k string) bool {
	k = strings.ToLower(k)
	for _, w := range secretKeyWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// SummarizeArgs renders the key arguments of a call for a human: sorted keys,
// long values truncated, secret-looking values redacted.
func SummarizeArgs(args map[string]interface{}) string {
	if len(args) == 0 {
		return "(no arguments)"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i == maxArgs {
			fmt.Fprintf(&b, "… and %d more argument(s)\n", len(keys)-maxArgs)
			break
		}
		var v string
		if isSecretKey(k) {
			v = "[redacted]"
		} else if s, ok := args[k].(string); ok {
			v = s
		} else {
			raw, _ := json.Marshal(args[k])
			v = string(raw)
		}
		v = strings.ReplaceAll(v, "\n", " ")
		if r := []rune(v); len(r) > maxValueChars {
			v = string(r[:maxValueChars]) + "…"
		}
		fmt.Fprintf(&b, "• %s: %s\n", k, v)
		if b.Len() > maxArgsSummary {
			b.WriteString("…\n")
			break
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

var gpuTierCost = map[string]string{
	"starter": "~$0.39/hr (RTX 4090) while running",
	"pro":     "~$1.64/hr (A100 80GB) while running",
	"beast":   "~$3.49/hr (H100 80GB) while running",
}

func num(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// EstimateCost returns a human cost estimate when one is known, else "".
func EstimateCost(tool string, args map[string]interface{}) string {
	d := Classify(tool)
	switch tool {
	case "gpu_launch":
		tier, _ := args["tier"].(string)
		if c, ok := gpuTierCost[strings.ToLower(tier)]; ok {
			return c + ", plus storage"
		}
		return "$0.39–$3.49/hr depending on the tier, plus storage"
	case "gpu_resume":
		return "GPU billing restarts at the pod's hourly rate"
	case "gpu_create_volume":
		if gb, ok := num(args["size_gb"]); ok {
			return fmt.Sprintf("~$%.2f/month (%.0f GB at ~$0.07/GB/month)", gb*0.07, gb)
		}
		return "~$0.07/GB/month"
	case "gpu_resize":
		return "larger storage is billed per GB for as long as it exists"
	}
	if d.Category == CategorySpend && d.Effect == EffectGPUStart {
		return "billed per hour by the provider for the chosen GPU while it runs"
	}
	if d.Category == CategorySpend {
		return "may add charges with the provider"
	}
	return ""
}

var categoryText = map[Category]string{
	CategorySpend:       "spends money",
	CategoryUpload:      "uploads local files to a remote machine",
	CategoryDeploy:      "deploys or removes a deployment",
	CategoryMessage:     "sends a message to people",
	CategorySign:        "signs or requests a signature",
	CategoryWrite:       "writes data in an external system",
	CategoryDestructive: "deletes or destroys remote resources",
	CategoryAction:      "acts in an external system (not known to be read-only)",
}

// Question builds the approval question for a tool call.
func Question(tool string, args map[string]interface{}) string {
	d := Classify(tool)
	why := categoryText[d.Category]
	if why == "" {
		why = "is a high-impact action"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The agent wants to run %s, which %s. Allow it?\n\n", tool, why)
	b.WriteString(SummarizeArgs(args))
	if c := EstimateCost(tool, args); c != "" {
		fmt.Fprintf(&b, "\n\nEstimated cost: %s", c)
	}
	return b.String()
}

// AskInput is the AskUserQuestion-shaped input for the approval question.
func AskInput(tool string, args map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{
			"question": Question(tool, args),
			"header":   Header,
			"options": []interface{}{
				map[string]interface{}{"label": AnswerApprove, "description": "Run " + tool + " now"},
				map[string]interface{}{"label": AnswerDeny, "description": "Block this call; the agent is told you declined"},
			},
		}},
	}
}

// DeniedMessage is the tool error the agent gets when the user declines.
func DeniedMessage(tool, detail string) string {
	msg := fmt.Sprintf("The user did not approve %s, so it was not run. Do not retry it unless the user asks you to; tell the user what you wanted to do instead.", tool)
	if detail != "" {
		msg += " (" + detail + ")"
	}
	return msg
}
