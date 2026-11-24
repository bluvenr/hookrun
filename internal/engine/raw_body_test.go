package engine

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bluvenr/hookrun/internal/config"
)

// Regression for D-02: ParseRequest filled BodyBytes (for HMAC) but never
// BodyRaw, so HOOKRUN_RAW_BODY and {{.raw_body}} were always empty for real
// requests. Existing unit tests hand-filled BodyRaw literals and missed this.
// These tests drive the real ParseRequest -> buildEnvVars / template path.

func TestParseRequest_PopulatesBodyRaw(t *testing.T) {
	body := `{"ref":"refs/heads/main","n":42}`
	r := httptest.NewRequest("POST", "/webhook/deploy", strings.NewReader(body))

	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}
	if data.BodyRaw != body {
		t.Errorf("BodyRaw = %q, want the exact raw body %q", data.BodyRaw, body)
	}
	if string(data.BodyBytes) != body {
		t.Errorf("BodyBytes = %q, want %q", data.BodyBytes, body)
	}
}

func TestParseRequest_BodyRawNonJSON(t *testing.T) {
	// raw_body must carry non-JSON payloads too (Body stays nil).
	body := "plain text payload"
	r := httptest.NewRequest("POST", "/webhook/deploy", strings.NewReader(body))

	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}
	if data.BodyRaw != body {
		t.Errorf("BodyRaw = %q, want %q", data.BodyRaw, body)
	}
	if data.Body != nil {
		t.Errorf("Body should stay nil for non-JSON payload, got %v", data.Body)
	}
}

func TestBuildEnvVars_RawBodyFromRealRequest(t *testing.T) {
	e := newTestEngine(t)
	body := `{"ref":"refs/heads/main"}`
	r := httptest.NewRequest("POST", "/webhook/deploy", strings.NewReader(body))
	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}

	action := &config.Action{Type: "command", Cmd: "echo"}
	envVars := e.buildEnvVars(action, data)

	want := "HOOKRUN_RAW_BODY=" + body
	found := false
	for _, ev := range envVars {
		if ev == want {
			found = true
		}
	}
	if !found {
		t.Errorf("HOOKRUN_RAW_BODY not carried from real request; env=%v", envVars)
	}
}

func TestResolveActionTemplates_RawBodyCommandFromRealRequest(t *testing.T) {
	e := newTestEngine(t)
	// Metacharacters in the body prove both value preservation and the
	// D-04 shell escaping on the command path (option b: template kept).
	body := `{"msg":"hi; touch pwned"}`
	r := httptest.NewRequest("POST", "/webhook/deploy", strings.NewReader(body))
	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}

	got := e.resolveActionTemplates("echo {{.raw_body}}", data, nil, true)
	// Unquoted template context: the value must arrive as a single-quoted
	// literal so the shell cannot interpret the embedded "; touch pwned".
	want := "echo '" + body + "'"
	if got != want {
		t.Errorf("resolved command = %q, want %q", got, want)
	}
}
