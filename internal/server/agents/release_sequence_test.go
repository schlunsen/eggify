package agents

import "testing"

func TestReleaseSequence(t *testing.T) {
	sm := &SessionManager{}
	s := &AgentSession{}

	// Nothing allocated after: the counter rolls back.
	s.MessageCount = 5
	sm.releaseSequence(s, 5)
	if s.MessageCount != 4 {
		t.Fatalf("want 4, got %d", s.MessageCount)
	}

	// A user prompt took 6 while assistant 5 was in flight: releasing 5 must
	// not make the next message reuse 6.
	s.MessageCount = 6
	sm.releaseSequence(s, 5)
	if s.MessageCount != 6 {
		t.Fatalf("want 6 (gap kept), got %d", s.MessageCount)
	}
}
