package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bluvenr/hookrun/internal/config"
)

// --- D-04: request-derived template values must be shell-escaped in command
// cmds and script paths, but NOT in webhook URLs/headers/bodies or script args
// (args are already argv-quoted by the executor). ---

// d04Payload is the SEC10 injection: one single quote closes the operator's
// quoting and smuggles a real command into sh -c.
const d04Payload = "x'; touch CANARY; echo '"

func d04Request() *RequestData {
	return &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{"x": d04Payload},
	}
}

func TestResolveActionTemplates_NoShellEscape_WebhookVerbatim(t *testing.T) {
	e := newTestEngine(t)
	// Webhook bodies/URLs/headers are not shell commands: values must stay verbatim.
	got := e.resolveActionTemplates(`{"ref":"{{.body.x}}"}`, d04Request(), nil, false)
	want := `{"ref":"x'; touch CANARY; echo '"}`
	if got != want {
		t.Errorf("webhook template must not be shell-escaped:\n got %s\nwant %s", got, want)
	}
}

// TestExecuteSingleAction_CommandInjectionCanaryAbsent reproduces SEC10 end to
// end: the rendered command runs through sh -c, the injected touch must NOT
// execute, and the output file must contain the payload byte-for-byte.
func TestExecuteSingleAction_CommandInjectionCanaryAbsent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh semantics")
	}
	e := newTestEngine(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	canary := filepath.Join(dir, "canary-inject")
	payload := "x'; touch " + canary + "; echo '"
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{"x": payload},
	}
	action := &config.Action{
		Type: "command",
		Cmd:  "printf '%s' '{{.body.x}}' > " + out,
	}
	res := e.executeSingleAction(action, req, "cfg", "r1", e.logger)
	if !res.Success() {
		t.Fatalf("action failed: %v stderr=%s", res.Error, res.Stderr)
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Errorf("D-04 NOT FIXED: injected command executed, canary exists (stat err=%v)", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != payload {
		t.Errorf("payload not preserved:\n got %q\nwant %q", string(data), payload)
	}
}

// TestExecuteSingleAction_CommandInjectionUnquoted covers the unquoted template
// context: metacharacters must not split into extra commands either.
func TestExecuteSingleAction_CommandInjectionUnquoted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh semantics")
	}
	e := newTestEngine(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	canary := filepath.Join(dir, "canary-inject")
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{"x": "a; touch " + canary},
	}
	action := &config.Action{
		Type: "command",
		Cmd:  "printf '%s' {{.body.x}} > " + out,
	}
	res := e.executeSingleAction(action, req, "cfg", "r1", e.logger)
	if !res.Success() {
		t.Fatalf("action failed: %v stderr=%s", res.Error, res.Stderr)
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Errorf("D-04 NOT FIXED: unquoted injection executed (stat err=%v)", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "a; touch "+canary {
		t.Errorf("payload not preserved: got %q", string(data))
	}
}