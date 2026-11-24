package main

// Backfill tests for the update-check cache helpers (pure path-based I/O).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateCache_RoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "update-check.json")
	want := updateCache{
		CheckedAt:     time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC),
		LatestVersion: "9.9.9",
	}
	if err := writeUpdateCache(p, want); err != nil {
		t.Fatalf("writeUpdateCache: %v", err)
	}
	got, err := readUpdateCache(p)
	if err != nil {
		t.Fatalf("readUpdateCache: %v", err)
	}
	if got.LatestVersion != want.LatestVersion || !got.CheckedAt.Equal(want.CheckedAt) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestUpdateCache_MissingFileIsError(t *testing.T) {
	if _, err := readUpdateCache(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing cache file")
	}
}

func TestUpdateCache_CorruptJSONIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "corrupt.json")
	os.WriteFile(p, []byte(`{not json`), 0644)
	if _, err := readUpdateCache(p); err == nil {
		t.Fatal("expected error for corrupt cache JSON")
	}
}

func TestUpdateCache_WriteToBadPathIsError(t *testing.T) {
	dir := t.TempDir()
	// The target path is an existing directory: WriteFile must fail.
	if err := writeUpdateCache(dir, updateCache{LatestVersion: "9.9.9"}); err == nil {
		t.Fatal("expected error writing cache onto a directory path")
	}
}
