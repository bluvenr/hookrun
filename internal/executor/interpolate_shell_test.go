package executor

import (
	"runtime"
	"testing"
)

// --- InterpolateShell: context-aware shell escaping of template values (D-04) ---

// interpolateTestResolver resolves {{.x}} to a fixed value for testing.
func interpolateTestResolver(val string) func(string) string {
	return func(match string) string { return val }
}

func TestInterpolateShell_SingleQuoteContext_InjectionNeutralized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	// D-04 payload: a single quote closes the operator's quoting and injects a command.
	payload := "x'; touch /tmp/hr-d04-canary; echo '"
	got := InterpolateShell("printf '%s' '{{.x}}'", interpolateTestResolver(payload))
	want := "printf '%s' 'x'\\''; touch /tmp/hr-d04-canary; echo '\\'''"
	if got != want {
		t.Errorf("single-quote context not escaped:\n got %s\nwant %s", got, want)
	}
}

func TestInterpolateShell_SingleQuoteContext_BenignUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	got := InterpolateShell("printf '%s' '{{.x}}'", interpolateTestResolver("main"))
	if got != "printf '%s' 'main'" {
		t.Errorf("benign value altered: got %s", got)
	}
}

func TestInterpolateShell_DoubleQuoteContext_MetacharsEscaped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	payload := `a"b$c` + "`d`" + `\e`
	got := InterpolateShell(`echo "{{.x}}"`, interpolateTestResolver(payload))
	want := `echo "a\"b\$c` + "\\`d\\`" + `\\e"`
	if got != want {
		t.Errorf("double-quote context not escaped:\n got %s\nwant %s", got, want)
	}
}

func TestInterpolateShell_UnquotedContext_WrappedLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	got := InterpolateShell("echo {{.x}}", interpolateTestResolver("a; touch /tmp/hr-d04-canary"))
	want := "echo 'a; touch /tmp/hr-d04-canary'"
	if got != want {
		t.Errorf("unquoted context not wrapped:\n got %s\nwant %s", got, want)
	}
}

func TestInterpolateShell_UnquotedContext_BenignUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	got := InterpolateShell("deploy {{.x}}", interpolateTestResolver("main"))
	if got != "deploy main" {
		t.Errorf("benign value altered: got %s", got)
	}
}

func TestInterpolateShell_MultipleTemplates_ContextTracked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	// First template sits in single quotes, second is unquoted; the escaped
	// replacement of the first must not leak quote state into the second.
	tmpl := "printf '%s' '{{.x}}' && echo {{.x}}"
	got := InterpolateShell(tmpl, func(match string) string {
		return "v' ; id"
	})
	want := "printf '%s' 'v'\\'' ; id' && echo 'v'\\'' ; id'"
	if got != want {
		t.Errorf("context tracking wrong:\n got %s\nwant %s", got, want)
	}
}

func TestInterpolateShell_NoTemplate_Unchanged(t *testing.T) {
	tmpl := "echo 'hello world' | grep x"
	got := InterpolateShell(tmpl, interpolateTestResolver("SHOULD-NOT-APPEAR"))
	if got != tmpl {
		t.Errorf("template-free command altered: got %s", got)
	}
}

func TestInterpolateShell_EscapedQuoteInTemplateLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	// The \' outside quotes is a literal quote, NOT a quote-state toggle;
	// the template after it is still unquoted.
	got := InterpolateShell("echo \\' {{.x}}", interpolateTestResolver("a;b"))
	want := "echo \\' 'a;b'"
	if got != want {
		t.Errorf("escaped quote mishandled:\n got %s\nwant %s", got, want)
	}
}

func TestInterpolateShell_EmptyValue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh quoting semantics")
	}
	got := InterpolateShell("echo {{.x}}", interpolateTestResolver(""))
	if got != "echo " {
		t.Errorf("empty value should render as nothing: got %q", got)
	}
}

// TestInterpolateShell_ExecutedValuePreserved proves the escaping preserves the
// value byte-for-byte when the rendered command actually runs through sh -c.
func TestInterpolateShell_ExecutedValuePreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh semantics")
	}
	payload := "x'; echo INJECTED; echo '"
	cmd := InterpolateShell("printf '%s' '{{.x}}'", interpolateTestResolver(payload))
	res := ExecuteCommand(cmd, 10, false, nil)
	if !res.Success() {
		t.Fatalf("command failed: %v stderr=%s", res.Error, res.Stderr)
	}
	if res.Stdout != payload {
		t.Errorf("value not preserved through shell:\n got %q\nwant %q", res.Stdout, payload)
	}
	// Equality above is the injection proof: printf '%s' must emit exactly the
	// payload. Had the smuggled `echo INJECTED` run, stdout would carry extra
	// output beyond the literal payload. (The payload itself contains the word
	// INJECTED, so a substring check would be a false positive.)
}