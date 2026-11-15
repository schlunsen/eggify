package handlers

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// optionLikeTexts are request texts that `say` would parse as options if they
// were passed as arguments.
var optionLikeTexts = []string{
	"--input-file=/Users/x/.claude/eggify/.secret",
	"--output-file=/Users/x/.zshrc",
	"--network-send=evil.example:52800",
	"--voice=?",
	"-f /etc/hosts",
	"-f",
	"-o /tmp/x.aiff hello",
	"-",
	"--",
	"-- --input-file=/etc/hosts",
	"- a list item that starts with a dash",
}

// The attack: {"backend":"macos","text":"--input-file=<file>"} made `say`
// speak a file of the server's (returned as audio), since the text was an
// argument. The text must never be among the arguments, whatever it is.
func TestRedTeam_TTS_TextIsNeverASayOption(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.aiff")
	want := []string{"-v", "Samantha", "-r", "175", "-o", out}

	for _, raw := range optionLikeTexts {
		for _, text := range []string{raw, stripMarkdownForTTS(raw)} {
			cmd, err := sayCommand(macVoiceName("samantha"), out, text)
			if err != nil {
				t.Fatalf("text %q: %v", text, err)
			}
			// cmd.Args[0] is the program name.
			if got := cmd.Args[1:]; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("text %q changed the arguments: %q, want %q", text, got, want)
			}
			if cmd.Stdin == nil {
				t.Fatalf("text %q: no standard input, say would get no text", text)
			}
			spoken, err := io.ReadAll(cmd.Stdin)
			if err != nil {
				t.Fatal(err)
			}
			if string(spoken) != text {
				t.Fatalf("standard input = %q, want the text %q unchanged", spoken, text)
			}
		}
	}

	// The fallback command (system default voice) is built the same way.
	cmd, err := sayCommand("", out, "--input-file=/etc/hosts")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(cmd.Args[1:], " "), "-r 175 -o "+out; got != want {
		t.Fatalf("fallback arguments = %q, want %q", got, want)
	}
}

// Every option `say` gets is one the server wrote: a flag from a fixed set,
// followed by a value that does not start with a dash.
func TestTTSSayArgsAreFixedFlagsWithSafeValues(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.aiff")
	for _, voice := range append([]string{""}, keysOf(macVoiceMap)...) {
		name := ""
		if voice != "" {
			name = macVoiceName(voice)
		}
		args, err := sayArgs(name, out)
		if err != nil {
			t.Fatalf("voice %q: %v", voice, err)
		}
		if len(args)%2 != 0 {
			t.Fatalf("voice %q: arguments are not flag/value pairs: %q", voice, args)
		}
		for i := 0; i < len(args); i += 2 {
			flag, value := args[i], args[i+1]
			if flag != "-v" && flag != "-r" && flag != "-o" {
				t.Fatalf("voice %q: unexpected flag %q in %q", voice, flag, args)
			}
			if value == "" || strings.HasPrefix(value, "-") {
				t.Fatalf("voice %q: value %q of %s could be read as an option", voice, value, flag)
			}
		}
	}
}

func TestRedTeam_TTS_VoiceIsNeverASayOption(t *testing.T) {
	for _, voice := range []string{
		"--input-file=/etc/hosts",
		"-f",
		"-o/tmp/x",
		"?",
		"Samantha -f /etc/hosts",
		"../../etc/hosts",
		"/etc/hosts",
		"Samantha\n-f",
		"SAMANTHA",
		"",
		"nosuchvoice",
	} {
		if got := macVoiceName(voice); got != defaultMacVoice {
			t.Fatalf("request voice %q selected %q, want the default %q", voice, got, defaultMacVoice)
		}
	}
	for key, name := range macVoiceMap {
		if got := macVoiceName(key); got != name {
			t.Fatalf("known voice %q → %q, want %q", key, got, name)
		}
		if !macVoiceNameRE.MatchString(name) {
			t.Fatalf("voice name %q does not match the allowed pattern", name)
		}
	}

	// sayArgs refuses a voice name that is not a plain name, even if a
	// caller skipped macVoiceName.
	out := filepath.Join(t.TempDir(), "out.aiff")
	for _, name := range []string{"--input-file=/etc/hosts", "-f", "?", "Sam\nantha", " Samantha", "a/b"} {
		if args, err := sayArgs(name, out); err == nil {
			t.Fatalf("voice name %q was accepted: %q", name, args)
		}
	}
	// ...and an output path that is not absolute (so it can't start with a dash).
	for _, path := range []string{"-o", "--output-file=x", "out.aiff", ""} {
		if args, err := sayArgs("Samantha", path); err == nil {
			t.Fatalf("output path %q was accepted: %q", path, args)
		}
	}
}

// Text with nothing left to speak never reaches `say` (which would then wait
// for input).
func TestTTSMacOSRefusesEmptyText(t *testing.T) {
	app := ttsApp(&TTSHandler{})
	code, out := postTTS(t, app, `{"text":"---","backend":"macos"}`)
	if code != 400 {
		t.Fatalf("want 400, got %d (%s)", code, out)
	}
}

// Really runs `say` (silently, into a file) through the handler with
// --input-file=<marker file> as the text, and checks that what was spoken does
// not depend on the marker file's content.
func TestRedTeam_TTS_SayDoesNotSpeakTheNamedFile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	if _, err := exec.LookPath("say"); err != nil {
		t.Skip("say not found")
	}
	if testing.Short() {
		t.Skip("runs say; skipped with -short")
	}

	// stripMarkdownForTTS turns "_" into a space, so the marker's path must not
	// contain one (t.TempDir's does): otherwise the attack text would name no
	// file and the test would prove nothing.
	dir, err := os.MkdirTemp("/tmp", "eggifytts")
	if err != nil {
		t.Skipf("no temp folder in /tmp: %v", err)
	}
	defer os.RemoveAll(dir)
	marker := filepath.Join(dir, "marker.txt")
	attack := "--input-file=" + marker
	if stripMarkdownForTTS(attack) != attack {
		t.Skipf("marker path %q does not survive markdown stripping", marker)
	}
	long := strings.Repeat("zebra giraffe elephant hippopotamus crocodile ", 12)
	if err := os.WriteFile(marker, []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}

	app := ttsApp(&TTSHandler{})
	speak := func(text string) []byte {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"backend": "macos", "text": text})
		code, out := postTTS(t, app, string(body))
		if code != 200 {
			t.Skipf("say did not produce audio here (%d: %s)", code, out)
		}
		var resp struct {
			AudioB64 string `json:"audio_b64"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatal(err)
		}
		audio, err := base64.StdEncoding.DecodeString(resp.AudioB64)
		if err != nil || len(audio) == 0 {
			t.Fatalf("no audio in the response (%v)", err)
		}
		return audio
	}

	withLongFile := speak(attack)
	if err := os.WriteFile(marker, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	withShortFile := speak(attack)
	if len(withLongFile) != len(withShortFile) {
		t.Fatalf("the audio depends on the named file's content (%d bytes vs %d): say read the file",
			len(withLongFile), len(withShortFile))
	}

	// Control: the same text as an argument (the old command) does speak the
	// file, so the comparison above can tell the difference on this machine.
	spokenFile := func(content string) int64 {
		t.Helper()
		if err := os.WriteFile(marker, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "control.aiff")
		if err := exec.Command("say", "-v", "Samantha", "-r", "175", "-o", out, attack).Run(); err != nil {
			t.Skipf("control say failed: %v", err)
		}
		info, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	if a, b := spokenFile(long), spokenFile("no"); a == b {
		t.Logf("control: say no longer reads --input-file from an argument here (%d bytes both times)", a)
	} else {
		t.Logf("control: as an argument the same text speaks the file (%d vs %d bytes); on standard input it does not", a, b)
	}

	// Ordinary prose that starts with a dash is still spoken.
	if audio := speak("- hello there, this is a test"); len(audio) < 1000 {
		t.Fatalf("dash-led prose produced only %d bytes of audio", len(audio))
	}
}

func keysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
