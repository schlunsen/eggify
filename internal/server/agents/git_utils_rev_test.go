package agents

import "testing"

func TestValidateGitRev(t *testing.T) {
	for _, ok := range []string{"main", "origin/main", "feature/x-1", "v1.2.3", "HEAD~2", "abc123^"} {
		if err := ValidateGitRev(ok); err != nil {
			t.Errorf("ValidateGitRev(%q) = %v, want ok", ok, err)
		}
	}
	for _, bad := range []string{"", "--output=/tmp/x", "-p", "main..evil", "a b", "main;rm", "$(id)", ":(glob)*"} {
		if err := ValidateGitRev(bad); err == nil {
			t.Errorf("ValidateGitRev(%q) accepted", bad)
		}
	}
}
