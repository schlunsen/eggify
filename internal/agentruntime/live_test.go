package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// TestLiveDetect runs the real status commands (read-only: --version,
// `claude auth status`, `codex login status`, codex account/read) against this
// machine's CLIs and prints the result. It never logs in or out.
//
//	EGGIFY_LIVE_RUNTIMES=1 go test ./internal/agentruntime -run TestLiveDetect -v
func TestLiveDetect(t *testing.T) {
	if os.Getenv("EGGIFY_LIVE_RUNTIMES") != "1" {
		t.Skip("set EGGIFY_LIVE_RUNTIMES=1 to check the real CLIs")
	}
	d := NewDetector(nil)
	for _, st := range d.Check(context.Background(), true) {
		b, _ := json.MarshalIndent(st, "", "  ")
		t.Logf("%s", b)
		if st.Installed && st.Version == "" {
			t.Errorf("%s: installed but no version parsed", st.ID)
		}
	}
}
