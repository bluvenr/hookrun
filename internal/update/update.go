package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bluvenr/hookrun/internal/version"
)

const (
	repo         = "bluvenr/hookrun"
	latestAPI    = "https://api.github.com/repos/" + repo + "/releases/latest"
	downloadBase = "https://github.com/" + repo + "/releases/download"
	userAgent    = "hookrun"
)

// Asset represents a single downloadable release asset.
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

// ReleaseInfo holds parsed GitHub release metadata.
type ReleaseInfo struct {
	Tag         string  `json:"tag_name"`
	PublishedAt string  `json:"published_at"`
	Assets      []Asset `json:"assets"`
	Version     string  `json:"-"` // tag without leading 'v'
}

// CheckLatest queries the GitHub API for the latest release.
func CheckLatest(timeout time.Duration) (*ReleaseInfo, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, latestAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var info ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("parse release info: %w", err)
	}
	info.Version = strings.TrimPrefix(info.Tag, "v")
	if info.Version == "" {
		return nil, fmt.Errorf("empty tag_name in release response")
	}
	return &info, nil
}

// AssetName builds the release asset filename for the given platform.
func AssetName(ver, goos, goarch string) string {
	ver = strings.TrimPrefix(ver, "v")
	if goos == "windows" {
		return fmt.Sprintf("hookrun-v%s-%s-%s.zip", ver, goos, goarch)
	}
	return fmt.Sprintf("hookrun-v%s-%s-%s.tar.gz", ver, goos, goarch)
}

// IsDevBuild reports whether this binary was built without release metadata.
func IsDevBuild() bool {
	return version.BuildTime == "unknown" || version.BuildTime == ""
}

// InContainerOrCI reports whether HookRun is running inside a container or CI.
func InContainerOrCI() bool {
	if os.Getenv("CI") != "" || os.Getenv("HOOKRUN_IN_DOCKER") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// SelfUpdate downloads the target version and replaces the current binary.
// An empty targetVersion resolves to the latest release. It returns the
// version that was installed.
func SelfUpdate(targetVersion string) (string, error) {
	var info *ReleaseInfo
	if targetVersion == "" {
		var err error
		info, err = CheckLatest(15 * time.Second)
		if err != nil {
			return "", fmt.Errorf("check latest version: %w", err)
		}
		targetVersion = info.Version
	}
	targetVersion = strings.TrimPrefix(targetVersion, "v")

	goos, goarch := runtime.GOOS, runtime.GOARCH
	assetName := AssetName(targetVersion, goos, goarch)
	url := resolveAssetURL(info, assetName, targetVersion)

	tmpDir, err := os.MkdirTemp("", "hookrun-update-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	if err := downloadFile(url, archivePath); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}

	binName := "hookrun-" + goos + "-" + goarch
	if goos == "windows" {
		binName += ".exe"
	}
	newBinPath := filepath.Join(tmpDir, binName)
	if goos == "windows" {
		err = extractZip(archivePath, tmpDir, binName)
	} else {
		err = extractTarGz(archivePath, tmpDir, binName)
	}
	if err != nil {
		return "", fmt.Errorf("extract archive: %w", err)
	}

	currentExe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(currentExe); err == nil {
		currentExe = resolved
	}
	if err := replaceBinary(currentExe, newBinPath); err != nil {
		return "", err
	}
	return targetVersion, nil
}

// resolveAssetURL finds the download URL for an asset, falling back to the
// conventional GitHub release download path.
func resolveAssetURL(info *ReleaseInfo, assetName, ver string) string {
	if info != nil {
		for _, a := range info.Assets {
			if a.Name == assetName && a.DownloadURL != "" {
				return a.DownloadURL
			}
		}
	}
	ver = strings.TrimPrefix(ver, "v")
	return fmt.Sprintf("%s/v%s/%s", downloadBase, ver, assetName)
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return f.Sync()
}

func extractTarGz(archivePath, destDir, wantName string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if path.Base(hdr.Name) != wantName {
			continue
		}
		outPath := filepath.Join(destDir, wantName)
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		_, cerr := io.Copy(out, tr)
		out.Close()
		if cerr != nil {
			return cerr
		}
		return nil
	}
	return fmt.Errorf("binary %s not found in archive", wantName)
}

func extractZip(archivePath, destDir, wantName string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if path.Base(f.Name) != wantName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outPath := filepath.Join(destDir, wantName)
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return err
		}
		_, cerr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cerr != nil {
			return cerr
		}
		return nil
	}
	return fmt.Errorf("binary %s not found in archive", wantName)
}

// replaceBinary swaps the current executable with the newly downloaded binary.
func replaceBinary(currentExe, newBinPath string) error {
	dir := filepath.Dir(currentExe)

	if runtime.GOOS == "windows" {
		// Windows cannot overwrite a running executable; stage then rename.
		staged := filepath.Join(dir, filepath.Base(currentExe)+".new")
		if err := moveFile(newBinPath, staged); err != nil {
			return permissionErr(err)
		}
		oldPath := currentExe + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(currentExe, oldPath); err != nil {
			_ = os.Remove(staged)
			return permissionErr(err)
		}
		if err := os.Rename(staged, currentExe); err != nil {
			_ = os.Rename(oldPath, currentExe) // rollback
			_ = os.Remove(staged)
			return permissionErr(err)
		}
		return nil
	}

	// Unix: stage in the same directory, then atomically rename over current.
	tmp := filepath.Join(dir, ".hookrun.update.tmp")
	if err := moveFile(newBinPath, tmp); err != nil {
		return permissionErr(err)
	}
	if err := os.Chmod(tmp, 0755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, currentExe); err != nil {
		_ = os.Remove(tmp)
		return permissionErr(err)
	}
	return nil
}

// moveFile renames src to dst, falling back to a copy across devices.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func permissionErr(err error) error {
	if os.IsPermission(err) {
		if runtime.GOOS == "windows" {
			return fmt.Errorf("permission denied replacing binary; try running as Administrator: %w", err)
		}
		return fmt.Errorf("permission denied replacing binary; try running with sudo: %w", err)
	}
	return err
}

// ---- semver comparison ----

type semverT struct {
	nums [3]int
	pre  string
}

func parseSemver(v string) semverT {
	v = strings.TrimPrefix(v, "v")
	var s semverT
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		s.pre = v[idx+1:]
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		s.nums[i] = n
	}
	return s
}

// CompareSemver compares two semantic versions and returns -1, 0, or 1.
func CompareSemver(a, b string) int {
	ap, bp := parseSemver(a), parseSemver(b)
	for i := 0; i < 3; i++ {
		if ap.nums[i] < bp.nums[i] {
			return -1
		}
		if ap.nums[i] > bp.nums[i] {
			return 1
		}
	}
	// Equal core version: a release (no prerelease) outranks a prerelease.
	if ap.pre == "" && bp.pre == "" {
		return 0
	}
	if ap.pre == "" {
		return 1
	}
	if bp.pre == "" {
		return -1
	}
	return strings.Compare(ap.pre, bp.pre)
}
