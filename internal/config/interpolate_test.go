package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInterpolate_EnvVar(t *testing.T) {
	os.Setenv("HOOKRUN_TEST_TOKEN", "my-secret-123")
	defer os.Unsetenv("HOOKRUN_TEST_TOKEN")

	input := `auth:
  token:
    value: "${env:HOOKRUN_TEST_TOKEN}"`

	result, err := interpolate(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `auth:
  token:
    value: "my-secret-123"`
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

func TestInterpolate_FileRef(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "token")
	if err := os.WriteFile(secretFile, []byte("file-secret-456\n"), 0600); err != nil {
		t.Fatal(err)
	}

	input := `auth:
  hmac:
    secret: "${file:` + secretFile + `}"`

	result, err := interpolate(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `auth:
  hmac:
    secret: "file-secret-456"`
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

func TestInterpolate_MissingEnvVar(t *testing.T) {
	os.Unsetenv("HOOKRUN_NONEXISTENT")

	input := `value: "${env:HOOKRUN_NONEXISTENT}"`

	_, err := interpolate(input)
	if err == nil {
		t.Fatal("expected error for missing env var, got nil")
	}
}

func TestInterpolate_MissingFile(t *testing.T) {
	input := `value: "${file:/nonexistent/path/secret}"`

	_, err := interpolate(input)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestInterpolate_NoReferences(t *testing.T) {
	input := `server:
  port: 9000
  route: "/webhook"`

	result, err := interpolate(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != input {
		t.Errorf("content changed unexpectedly:\nexpected:\n%s\ngot:\n%s", input, result)
	}
}

func TestInterpolate_MultipleRefs(t *testing.T) {
	os.Setenv("HOOKRUN_A", "alpha")
	os.Setenv("HOOKRUN_B", "beta")
	defer os.Unsetenv("HOOKRUN_A")
	defer os.Unsetenv("HOOKRUN_B")

	input := `token: "${env:HOOKRUN_A}"
secret: "${env:HOOKRUN_B}"`

	result, err := interpolate(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `token: "alpha"
secret: "beta"`
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

func TestInterpolate_MixedEnvAndFile(t *testing.T) {
	os.Setenv("HOOKRUN_MIX", "env-value")
	defer os.Unsetenv("HOOKRUN_MIX")

	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretFile, []byte("file-value"), 0600); err != nil {
		t.Fatal(err)
	}

	input := `a: "${env:HOOKRUN_MIX}"
b: "${file:` + secretFile + `}"`

	result, err := interpolate(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `a: "env-value"
b: "file-value"`
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

func TestInterpolate_FileTrimsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "token")
	// Docker Secrets files typically end with \n
	if err := os.WriteFile(secretFile, []byte("docker-secret\r\n"), 0600); err != nil {
		t.Fatal(err)
	}

	input := `value: "${file:` + secretFile + `}"`

	result, err := interpolate(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `value: "docker-secret"`
	if result != expected {
		t.Errorf("expected trailing whitespace trimmed, got:\n%s", result)
	}
}
