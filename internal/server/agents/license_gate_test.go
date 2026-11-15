package agents

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// While Eggify is not activated no turn starts, whatever path sends it
// (defence in depth behind the HTTP middleware).
func TestPromptsRefusedWhileUnlicensed(t *testing.T) {
	errUnlicensed := errors.New("Activate Eggify with your serial number")
	SetLicenseGate(func() error { return errUnlicensed })
	t.Cleanup(func() { SetLicenseGate(nil) })

	sm := &SessionManager{}
	id := uuid.New()
	if err := sm.SendPrompt(id, "hello", nil); !errors.Is(err, errUnlicensed) {
		t.Fatalf("SendPrompt: %v", err)
	}
	if err := sm.SendPromptSilent(id, "continue"); !errors.Is(err, errUnlicensed) {
		t.Fatalf("SendPromptSilent: %v", err)
	}
	if err := sm.SendPromptWithContent(id, nil, nil); !errors.Is(err, errUnlicensed) {
		t.Fatalf("SendPromptWithContent: %v", err)
	}

	SetLicenseGate(nil)
	if err := checkLicense(); err != nil {
		t.Fatalf("no gate: %v", err)
	}
}
