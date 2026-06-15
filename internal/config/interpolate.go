package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// interpolatePattern matches ${env:KEY} and ${file:path} patterns in YAML content.
var interpolatePattern = regexp.MustCompile(`\$\{(env|file):([^}]+)\}`)

// interpolate resolves ${env:KEY} and ${file:path} references in raw YAML content.
//
// Supported patterns:
//   - ${env:VAR_NAME}  — replaced with the value of environment variable VAR_NAME
//   - ${file:/path}    — replaced with the trimmed content of the file at /path
//
// Missing environment variables and unreadable files produce errors (fail-safe).
func interpolate(content string) (string, error) {
	var firstErr error
	result := interpolatePattern.ReplaceAllStringFunc(content, func(match string) string {
		if firstErr != nil {
			return match
		}
		parts := interpolatePattern.FindStringSubmatch(match)
		if parts == nil {
			return match
		}
		kind := parts[1]
		ref := parts[2]
		switch kind {
		case "env":
			val, ok := os.LookupEnv(ref)
			if !ok {
				firstErr = fmt.Errorf("environment variable '%s' is not set (referenced by ${env:%s})", ref, ref)
				return match
			}
			return val
		case "file":
			data, err := os.ReadFile(ref)
			if err != nil {
				firstErr = fmt.Errorf("cannot read file '%s' (referenced by ${file:%s}): %w", ref, ref, err)
				return match
			}
			return strings.TrimRight(string(data), "\n\r\t ")
		}
		return match
	})
	return result, firstErr
}
