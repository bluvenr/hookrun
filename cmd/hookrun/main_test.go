package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- D-05: resolveReloadPort ---

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveReloadPort_StatusPortWins(t *testing.T) {
	path := writeTempConfig(t, "server:\n  port: 8080\n")
	readStatus := func() (string, error) { return `{"pid":1,"port":9123}`, nil }
	port, err := resolveReloadPort(path, readStatus)
	if err != nil {
		t.Fatalf("should resolve port: %v", err)
	}
	if port != 9123 {
		t.Errorf("running instance port 9123 should win over config 8080, got %d", port)
	}
}

func TestResolveReloadPort_FallsBackToConfig(t *testing.T) {
	path := writeTempConfig(t, "server:\n  port: 8080\n")
	readStatus := func() (string, error) { return "", os.ErrNotExist }
	port, err := resolveReloadPort(path, readStatus)
	if err != nil {
		t.Fatalf("should fall back to config port: %v", err)
	}
	if port != 8080 {
		t.Errorf("expected config port 8080, got %d", port)
	}
}

func TestResolveReloadPort_InvalidStatusFallsBack(t *testing.T) {
	path := writeTempConfig(t, "server:\n  port: 8080\n")
	readStatus := func() (string, error) { return "not json", nil }
	port, err := resolveReloadPort(path, readStatus)
	if err != nil {
		t.Fatalf("invalid status.json should fall back to config: %v", err)
	}
	if port != 8080 {
		t.Errorf("expected config port 8080, got %d", port)
	}
}

func TestResolveReloadPort_BadConfigSurfacesError(t *testing.T) {
	path := writeTempConfig(t, "server:\n  port: 99999\n")
	readStatus := func() (string, error) { return "", os.ErrNotExist }
	port, err := resolveReloadPort(path, readStatus)
	if err == nil {
		t.Fatalf("broken config must surface an error, not guess a port (got %d)", port)
	}
	if port != 0 {
		t.Errorf("port should be unset on error, got %d", port)
	}
}

// --- D-05: postReload ---

func TestPostReload_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	body, err := postReload(&http.Client{Timeout: 2 * time.Second}, srv.URL+"/_reload")
	if err != nil {
		t.Fatalf("200 response should succeed: %v", err)
	}
	if !strings.Contains(body, "ok") {
		t.Errorf("expected response body to be returned, got %q", body)
	}
}

func TestPostReload_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"reload failed: bad yaml"}`))
	}))
	defer srv.Close()
	_, err := postReload(&http.Client{Timeout: 2 * time.Second}, srv.URL+"/_reload")
	if err == nil {
		t.Fatal("HTTP 500 must be reported as an error, not success")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "bad yaml") {
		t.Errorf("error should include status code and body, got: %v", err)
	}
}

func TestPostReload_NetworkError(t *testing.T) {
	// Bind then close to obtain a port with no listener.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := postReload(&http.Client{Timeout: 2 * time.Second}, url+"/_reload")
	if err == nil {
		t.Fatal("connection failure must return an error (caller decides SIGHUP fallback)")
	}
}

func TestPostReload_RespectsClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	_, err := postReload(&http.Client{Timeout: 50 * time.Millisecond}, srv.URL+"/_reload")
	if err == nil {
		t.Fatal("slow server must trigger the client timeout, not hang forever")
	}
}

// --- D-06: waitDaemonReady ---

// TestHelperProcess is not a real test; it is invoked as a child process by
// the waitDaemonReady tests (GO_WANT_HELPER_PROCESS=1).
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("HELPER_MODE") == "sleep" {
		time.Sleep(30 * time.Second)
	}
	if os.Getenv("HELPER_MODE") == "late-exit" {
		// Die shortly after startup, like a daemon whose bind fails.
		time.Sleep(150 * time.Millisecond)
	}
	os.Exit(1)
}

func helperCmd(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_MODE="+mode)
	return cmd
}

// closedPortURL returns a URL whose port has no listener.
func closedPortURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

func TestWaitDaemonReady_ChildExitsEarly(t *testing.T) {
	cmd := helperCmd("exit")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	err := waitDaemonReady(cmd, closedPortURL(t)+"/health", 3*time.Second)
	if err == nil {
		t.Fatal("a child that exited during startup must not be reported as started")
	}
}

func TestWaitDaemonReady_SquatterAnswers200ButChildDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	cmd := helperCmd("late-exit")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	err := waitDaemonReady(cmd, srv.URL+"/health", 3*time.Second)
	if err == nil {
		t.Fatal("health 200 from a squatter must not mask the child's death")
	}
}

func TestWaitDaemonReady_HealthyChild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	cmd := helperCmd("sleep")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	if err := waitDaemonReady(cmd, srv.URL+"/health", 3*time.Second); err != nil {
		t.Fatalf("alive child + health 200 should be ready: %v", err)
	}
}

func TestWaitDaemonReady_Timeout(t *testing.T) {
	cmd := helperCmd("sleep")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	start := time.Now()
	err := waitDaemonReady(cmd, closedPortURL(t)+"/health", 500*time.Millisecond)
	if err == nil {
		t.Fatal("no health endpoint within the deadline must be an error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("timeout not honored, took %s", elapsed)
	}
}
