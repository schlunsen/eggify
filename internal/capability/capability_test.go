package capability

import "testing"

func TestCapabilityIsBoundToPurposeAndSession(t *testing.T) {
	tok := Token(Apps, "s1")
	if !Verify(Apps, "s1", tok) {
		t.Fatal("valid capability rejected")
	}
	for _, c := range []struct{ purpose, sid, tok string }{
		{Apps, "s2", tok},              // another session
		{"n0", "s1", tok},              // another purpose
		{Apps, "s1", tok[:len(tok)-1]}, // truncated
		{Apps, "s1", ""},
		{Apps, "", tok},
		{"", "s1", tok},
	} {
		if Verify(c.purpose, c.sid, c.tok) {
			t.Errorf("Verify(%q, %q, %q) accepted", c.purpose, c.sid, c.tok)
		}
	}
}
