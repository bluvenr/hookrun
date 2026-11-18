package executor

import (
	"regexp"
	"strings"
)

// shellPlaceholderRe matches a template placeholder such as {{.body.x}} or
// {{.raw_body}}. It is intentionally permissive: the caller's resolve callback
// decides how to extract the value for each matched placeholder.
var shellPlaceholderRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// InterpolateShell replaces every {{...}} placeholder in tmpl with the value
// returned by resolve, escaping each value for the shell quoting context it
// appears in. This neutralizes OS command injection (CWE-78, D-04) when a
// rendered command is handed to sh -c: a request-derived value can no longer
// break out of the operator's quoting and smuggle in extra commands.
//
// Context handling (POSIX sh):
//   - inside single quotes: ' is rewritten to '\'' so the value stays literal;
//   - inside double quotes: \ " $ ` are backslash-escaped;
//   - unquoted: the value is wrapped in single quotes (safe values pass through
//     unchanged for readability).
//
// The escaped replacement is quote-balanced, so it never alters the quoting
// state seen by subsequent placeholders. Literal template text is emitted
// verbatim, preserving the operator's own quoting and structure.
//
// On Windows (cmd.exe) the same single-quote wrapping is not meaningful; callers
// that render shell commands target POSIX sh. The function still escapes quote
// characters defensively so a stray value cannot trivially break cmd quoting.
func InterpolateShell(tmpl string, resolve func(match string) string) string {
	locs := shellPlaceholderRe.FindAllStringIndex(tmpl, -1)
	if len(locs) == 0 {
		return tmpl
	}

	var b strings.Builder
	state := shellQuoteNone
	pos := 0
	for _, loc := range locs {
		// Emit the literal gap verbatim and advance quote state across it.
		gap := tmpl[pos:loc[0]]
		b.WriteString(gap)
		state = advanceShellQuoteState(gap, state)

		// Resolve and escape the placeholder for the context it sits in.
		match := tmpl[loc[0]:loc[1]]
		b.WriteString(escapeShellValue(resolve(match), state))
		pos = loc[1]
	}
	b.WriteString(tmpl[pos:])
	return b.String()
}

// Shell quoting states.
const (
	shellQuoteNone = iota
	shellQuoteSingle
	shellQuoteDouble
)

// advanceShellQuoteState walks literal shell text and returns the quoting state
// at its end. Backslash escapes are honoured so an escaped quote does not toggle
// the state.
func advanceShellQuoteState(s string, state int) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case shellQuoteNone:
			switch c {
			case '\'':
				state = shellQuoteSingle
			case '"':
				state = shellQuoteDouble
			case '\\':
				i++ // skip the escaped character
			}
		case shellQuoteSingle:
			if c == '\'' {
				state = shellQuoteNone
			}
		case shellQuoteDouble:
			switch c {
			case '"':
				state = shellQuoteNone
			case '\\':
				// Inside double quotes a backslash only escapes $ ` " \ newline.
				if i+1 < len(s) {
					switch s[i+1] {
					case '$', '`', '"', '\\', '\n':
						i++
					}
				}
			}
		}
	}
	return state
}

// escapeShellValue escapes val for the given quoting state so it is interpreted
// as a single literal value by the shell.
func escapeShellValue(val string, state int) string {
	switch state {
	case shellQuoteSingle:
		// Close the quote, insert an escaped quote, reopen: ' -> '\''
		return strings.ReplaceAll(val, "'", "'\\''")
	case shellQuoteDouble:
		var b strings.Builder
		for _, r := range val {
			switch r {
			case '\\', '"', '$', '`':
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		return b.String()
	default: // unquoted
		if val == "" {
			return ""
		}
		if isShellSafe(val) {
			return val
		}
		return "'" + strings.ReplaceAll(val, "'", "'\\''") + "'"
	}
}