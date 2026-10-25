package update

import (
	"testing"

	"github.com/bluvenr/hookrun/internal/version"
)

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.1.5", "1.1.4", 1},
		{"1.1.4", "1.1.5", -1},
		{"1.1.5", "1.1.5", 0},
		{"v1.2.0", "1.1.9", 1},
		{"1.2.0", "v1.2.0", 0},
		{"2.0.0", "1.99.99", 1},
		{"1.10.0", "1.9.0", 1},
		{"0.0.1", "0.0.2", -1},
		// prerelease: a release outranks its prerelease
		{"1.2.0", "1.2.0-rc1", 1},
		{"1.2.0-rc1", "1.2.0", -1},
		{"1.2.0-rc1", "1.2.0-rc2", -1},
		{"1.2.0-rc2", "1.2.0-rc1", 1},
	}
	for _, tt := range tests {
		if got := CompareSemver(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		ver, goos, goarch, want string
	}{
		{"1.1.5", "linux", "amd64", "hookrun-v1.1.5-linux-amd64.tar.gz"},
		{"v1.1.5", "darwin", "arm64", "hookrun-v1.1.5-darwin-arm64.tar.gz"},
		{"1.1.5", "windows", "amd64", "hookrun-v1.1.5-windows-amd64.zip"},
	}
	for _, tt := range tests {
		if got := AssetName(tt.ver, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("AssetName(%q, %q, %q) = %q, want %q", tt.ver, tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestResolveAssetURL(t *testing.T) {
	// Prefer the asset URL from release metadata when present.
	info := &ReleaseInfo{
		Tag: "v1.1.5",
		Assets: []Asset{
			{Name: "hookrun-v1.1.5-linux-amd64.tar.gz", DownloadURL: "https://cdn.example.com/x.tar.gz"},
		},
	}
	got := resolveAssetURL(info, "hookrun-v1.1.5-linux-amd64.tar.gz", "1.1.5")
	if got != "https://cdn.example.com/x.tar.gz" {
		t.Errorf("resolveAssetURL with metadata = %q", got)
	}

	// Fall back to the conventional download path when no metadata match.
	want := "https://github.com/bluvenr/hookrun/releases/download/v1.1.5/hookrun-v1.1.5-linux-arm64.tar.gz"
	if got := resolveAssetURL(info, "hookrun-v1.1.5-linux-arm64.tar.gz", "1.1.5"); got != want {
		t.Errorf("resolveAssetURL fallback = %q, want %q", got, want)
	}

	// Nil info also falls back.
	if got := resolveAssetURL(nil, "hookrun-v1.1.5-linux-amd64.tar.gz", "1.1.5"); got == "" {
		t.Error("resolveAssetURL with nil info returned empty")
	}
}

func TestParseSemver(t *testing.T) {
	s := parseSemver("v1.2.3-rc1")
	if s.nums != [3]int{1, 2, 3} {
		t.Errorf("parseSemver nums = %v, want [1 2 3]", s.nums)
	}
	if s.pre != "rc1" {
		t.Errorf("parseSemver pre = %q, want %q", s.pre, "rc1")
	}
}

func TestIsDevBuild(t *testing.T) {
	orig := version.BuildTime
	defer func() { version.BuildTime = orig }()

	tests := []struct {
		buildTime string
		want      bool
	}{
		{"unknown", true},
		{"", true},
		{"2026-06-11T00:00:00Z", false},
	}
	for _, tt := range tests {
		version.BuildTime = tt.buildTime
		if got := IsDevBuild(); got != tt.want {
			t.Errorf("IsDevBuild() with BuildTime=%q = %v, want %v", tt.buildTime, got, tt.want)
		}
	}
}
