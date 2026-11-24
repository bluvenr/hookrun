package update

// Pipeline backfill tests: CheckLatest / downloadFile / extract* / replaceBinary /
// selfUpdate end-to-end against an httptest fake release server. Everything runs
// in t.TempDir(); no real network access and no real binary is ever replaced.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- archive builders ----

func makeTarGz(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	p := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func makeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	p := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func platformBinName() string {
	n := "hookrun-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

// ---- CheckLatest ----

func TestCheckLatest_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != userAgent {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		fmt.Fprint(w, `{"tag_name":"v9.9.9","published_at":"2026-01-01T00:00:00Z","assets":[{"name":"x","browser_download_url":"https://cdn.example.com/x"}]}`)
	}))
	defer srv.Close()
	orig := latestAPI
	latestAPI = srv.URL
	defer func() { latestAPI = orig }()

	info, err := CheckLatest(2 * time.Second)
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if info.Version != "9.9.9" || info.Tag != "v9.9.9" {
		t.Errorf("Version/Tag = %q/%q, want 9.9.9/v9.9.9", info.Version, info.Tag)
	}
	if len(info.Assets) != 1 || info.Assets[0].DownloadURL != "https://cdn.example.com/x" {
		t.Errorf("Assets = %+v", info.Assets)
	}
}

func TestCheckLatest_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	orig := latestAPI
	latestAPI = srv.URL
	defer func() { latestAPI = orig }()

	_, err := CheckLatest(2 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want status 403 surfaced", err)
	}
}

func TestCheckLatest_BadJSONIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{not json`)
	}))
	defer srv.Close()
	orig := latestAPI
	latestAPI = srv.URL
	defer func() { latestAPI = orig }()

	_, err := CheckLatest(2 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "parse release info") {
		t.Fatalf("err = %v, want parse error", err)
	}
}

func TestCheckLatest_EmptyTagIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"","assets":[]}`)
	}))
	defer srv.Close()
	orig := latestAPI
	latestAPI = srv.URL
	defer func() { latestAPI = orig }()

	_, err := CheckLatest(2 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "empty tag_name") {
		t.Fatalf("err = %v, want empty-tag error", err)
	}
}

func TestCheckLatest_RespectsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	}))
	defer srv.Close()
	orig := latestAPI
	latestAPI = srv.URL
	defer func() { latestAPI = orig }()

	start := time.Now()
	_, err := CheckLatest(50 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("timeout not honored: %v", elapsed)
	}
}

// ---- downloadFile ----

func TestDownloadFile_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "BINARY-CONTENT")
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := downloadFile(srv.URL, dest); err != nil {
		t.Fatalf("downloadFile: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "BINARY-CONTENT" {
		t.Fatalf("content = %q, err = %v", got, err)
	}
}

func TestDownloadFile_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := downloadFile(srv.URL, dest)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want HTTP 404", err)
	}
}

func TestDownloadFile_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	if err := downloadFile(url, filepath.Join(t.TempDir(), "out.bin")); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestDownloadFile_TruncatedBodyIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("short"))
		panic(http.ErrAbortHandler) // sever the connection mid-body
	}))
	defer srv.Close()

	err := downloadFile(srv.URL, filepath.Join(t.TempDir(), "out.bin"))
	if err == nil {
		t.Fatal("expected unexpected-EOF error from truncated body")
	}
}

// ---- extractTarGz ----

func TestExtractTarGz_FindsNestedBinary(t *testing.T) {
	arc := makeTarGz(t, map[string]string{
		"README.md":                 "docs",
		"dist/" + platformBinName(): "NEWBIN",
	})
	dest := t.TempDir()
	if err := extractTarGz(arc, dest, platformBinName()); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, platformBinName()))
	if string(got) != "NEWBIN" {
		t.Fatalf("content = %q", got)
	}
}

func TestExtractTarGz_MissingBinaryIsError(t *testing.T) {
	arc := makeTarGz(t, map[string]string{"other-file": "x"})
	err := extractTarGz(arc, t.TempDir(), platformBinName())
	if err == nil || !strings.Contains(err.Error(), "not found in archive") {
		t.Fatalf("err = %v, want not-found error", err)
	}
}

func TestExtractTarGz_CorruptGzipIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.tar.gz")
	os.WriteFile(p, []byte("this is not gzip"), 0644)
	if err := extractTarGz(p, t.TempDir(), platformBinName()); err == nil {
		t.Fatal("expected gzip error")
	}
}

func TestExtractTarGz_SkipsNonRegularEntries(t *testing.T) {
	// A symlink and a directory entry carrying the wanted base name must be
	// ignored; only the regular file is extracted.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := platformBinName()
	tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	tw.WriteHeader(&tar.Header{Name: "d" + name, Typeflag: tar.TypeDir, Mode: 0755})
	tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 4, Mode: 0755})
	tw.Write([]byte("REAL"))
	tw.Close()
	gz.Close()
	arc := filepath.Join(t.TempDir(), "a.tar.gz")
	os.WriteFile(arc, buf.Bytes(), 0644)

	dest := t.TempDir()
	if err := extractTarGz(arc, dest, name); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, name))
	if string(got) != "REAL" {
		t.Fatalf("content = %q, want REAL (symlink/dir entries must be skipped)", got)
	}
}

func TestExtractTarGz_TraversalNameStaysInDest(t *testing.T) {
	// Even a hostile entry name resolves to destDir/wantName: extraction uses
	// wantName for the output path, never the archive-provided path.
	name := platformBinName()
	arc := makeTarGz(t, map[string]string{"../../" + name: "ESCAPE?"})
	dest := filepath.Join(t.TempDir(), "out")
	os.Mkdir(dest, 0755)
	if err := extractTarGz(arc, dest, name); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
		t.Fatalf("wanted file missing in dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), name)); err == nil {
		t.Fatal("file escaped destDir via traversal entry name")
	}
}

// ---- extractZip ----

func TestExtractZip_FindsNestedBinary(t *testing.T) {
	arc := makeZip(t, map[string]string{
		"README.md":                 "docs",
		"dist/" + platformBinName(): "NEWBIN",
	})
	dest := t.TempDir()
	if err := extractZip(arc, dest, platformBinName()); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, platformBinName()))
	if string(got) != "NEWBIN" {
		t.Fatalf("content = %q", got)
	}
}

func TestExtractZip_MissingBinaryIsError(t *testing.T) {
	arc := makeZip(t, map[string]string{"other-file": "x"})
	err := extractZip(arc, t.TempDir(), platformBinName())
	if err == nil || !strings.Contains(err.Error(), "not found in archive") {
		t.Fatalf("err = %v, want not-found error", err)
	}
}

func TestExtractZip_CorruptArchiveIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.zip")
	os.WriteFile(p, []byte("this is not a zip"), 0644)
	if err := extractZip(p, t.TempDir(), platformBinName()); err == nil {
		t.Fatal("expected zip error")
	}
}

func TestExtractZip_SkipsDirEntries(t *testing.T) {
	name := platformBinName()
	arc := makeZip(t, map[string]string{
		name + "/":  "", // directory entry with the wanted base name
		"x/" + name: "REAL",
	})
	dest := t.TempDir()
	if err := extractZip(arc, dest, name); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, name))
	if string(got) != "REAL" {
		t.Fatalf("content = %q, want REAL", got)
	}
}

func TestExtractZip_TraversalNameStaysInDest(t *testing.T) {
	name := platformBinName()
	arc := makeZip(t, map[string]string{"../../" + name: "ESCAPE?"})
	dest := filepath.Join(t.TempDir(), "out")
	os.Mkdir(dest, 0755)
	if err := extractZip(arc, dest, name); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
		t.Fatalf("wanted file missing in dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), name)); err == nil {
		t.Fatal("file escaped destDir via traversal entry name")
	}
}

// ---- moveFile / copyFile ----

func TestMoveFile_Rename(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("DATA"), 0644)
	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src still exists after move")
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "DATA" {
		t.Errorf("dst content = %q", got)
	}
}

func TestCopyFile_ContentAndMissingSrc(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("DATA"), 0600)
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "DATA" {
		t.Errorf("dst content = %q", got)
	}
	if err := copyFile(filepath.Join(dir, "missing"), dst); err == nil {
		t.Error("expected error for missing src")
	}
}

// ---- replaceBinary ----

func TestReplaceBinary_Success(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "hookrun")
	nw := filepath.Join(dir, "new.bin")
	os.WriteFile(cur, []byte("OLD"), 0755)
	os.WriteFile(nw, []byte("NEW"), 0644)

	if err := replaceBinary(cur, nw); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "NEW" {
		t.Fatalf("current content = %q, want NEW", got)
	}
	if runtime.GOOS == "windows" {
		old, err := os.ReadFile(cur + ".old")
		if err != nil || string(old) != "OLD" {
			t.Errorf(".old = %q err=%v, want OLD", old, err)
		}
		if _, err := os.Stat(cur + ".new"); !os.IsNotExist(err) {
			t.Error(".new staging file left behind")
		}
	} else {
		if _, err := os.Stat(filepath.Join(dir, ".hookrun.update.tmp")); !os.IsNotExist(err) {
			t.Error("unix staging file left behind")
		}
		fi, _ := os.Stat(cur)
		if fi.Mode().Perm() != 0755 {
			t.Errorf("mode = %v, want 0755", fi.Mode().Perm())
		}
	}
}

func TestReplaceBinary_WindowsStaleOldIsReplaced(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only staging semantics")
	}
	dir := t.TempDir()
	cur := filepath.Join(dir, "hookrun.exe")
	os.WriteFile(cur, []byte("OLD"), 0755)
	os.WriteFile(cur+".old", []byte("STALE"), 0644)
	nw := filepath.Join(dir, "new.bin")
	os.WriteFile(nw, []byte("NEW"), 0644)

	if err := replaceBinary(cur, nw); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	old, _ := os.ReadFile(cur + ".old")
	if string(old) != "OLD" {
		t.Errorf(".old = %q, want OLD (stale must be replaced)", old)
	}
}

func TestReplaceBinary_WindowsFirstRenameFailureCleansStaged(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only staging semantics")
	}
	dir := t.TempDir()
	cur := filepath.Join(dir, "hookrun.exe")
	os.WriteFile(cur, []byte("OLD"), 0755)
	// Block the current->.old rename: .old exists as a NON-EMPTY directory,
	// so both os.Remove(.old) and the rename fail deterministically.
	os.Mkdir(cur+".old", 0755)
	os.WriteFile(filepath.Join(cur+".old", "blocker"), []byte("x"), 0644)
	nw := filepath.Join(dir, "new.bin")
	os.WriteFile(nw, []byte("NEW"), 0644)

	if err := replaceBinary(cur, nw); err == nil {
		t.Fatal("expected rename failure")
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "OLD" {
		t.Errorf("current must stay intact, got %q", got)
	}
	if _, err := os.Stat(cur + ".new"); !os.IsNotExist(err) {
		t.Error("staged .new must be cleaned up on failure")
	}
}

func TestReplaceBinary_WindowsStageFailureKeepsCurrent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only staging semantics")
	}
	dir := t.TempDir()
	cur := filepath.Join(dir, "hookrun.exe")
	os.WriteFile(cur, []byte("OLD"), 0755)
	// Block staging: .new exists as a non-empty directory, so both the
	// rename and the copy fallback fail.
	os.Mkdir(cur+".new", 0755)
	os.WriteFile(filepath.Join(cur+".new", "blocker"), []byte("x"), 0644)
	nw := filepath.Join(dir, "new.bin")
	os.WriteFile(nw, []byte("NEW"), 0644)

	if err := replaceBinary(cur, nw); err == nil {
		t.Fatal("expected stage failure")
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "OLD" {
		t.Errorf("current must stay intact, got %q", got)
	}
}

func TestReplaceBinary_UnixRenameFailureCleansTmp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix atomic-rename semantics")
	}
	dir := t.TempDir()
	// currentExe is a NON-EMPTY directory: rename(file, dir) fails with
	// ENOTEMPTY/EISDIR, exercising the cleanup path.
	cur := filepath.Join(dir, "hookrun")
	os.Mkdir(cur, 0755)
	os.WriteFile(filepath.Join(cur, "blocker"), []byte("x"), 0644)
	nw := filepath.Join(dir, "new.bin")
	os.WriteFile(nw, []byte("NEW"), 0644)

	if err := replaceBinary(cur, nw); err == nil {
		t.Fatal("expected rename failure")
	}
	if _, err := os.Stat(filepath.Join(dir, ".hookrun.update.tmp")); !os.IsNotExist(err) {
		t.Error("unix staging file must be cleaned up on failure")
	}
	if _, err := os.Stat(filepath.Join(cur, "blocker")); err != nil {
		t.Error("target directory must stay intact")
	}
}

func TestReplaceBinary_MissingNewBinIsError(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "hookrun")
	os.WriteFile(cur, []byte("OLD"), 0755)
	if err := replaceBinary(cur, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error for missing new binary")
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "OLD" {
		t.Errorf("current must stay intact, got %q", got)
	}
}

func TestPermissionErr(t *testing.T) {
	wrapped := permissionErr(os.ErrPermission)
	if wrapped == nil || !strings.Contains(wrapped.Error(), "permission denied replacing binary") {
		t.Fatalf("wrapped = %v", wrapped)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(wrapped.Error(), "Administrator") {
			t.Errorf("windows hint missing: %v", wrapped)
		}
	} else if !strings.Contains(wrapped.Error(), "sudo") {
		t.Errorf("unix hint missing: %v", wrapped)
	}
	plain := errors.New("boom")
	if got := permissionErr(plain); got != plain {
		t.Errorf("non-permission error must pass through, got %v", got)
	}
}

// ---- selfUpdate end-to-end (fake release server, temp target file) ----

// fakeRelease serves release metadata at /latest and the platform archive at
// /v9.9.9/<asset>. Returns the server; caller overrides latestAPI/downloadBase.
func fakeRelease(t *testing.T, archiveContent string) *httptest.Server {
	t.Helper()
	asset := AssetName("9.9.9", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"browser_download_url":%q}]}`,
			asset, srvURL+"/asset")
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(archiveContent))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, asset) {
			w.Write([]byte(archiveContent))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	return srv
}

func platformArchive(t *testing.T, content string) string {
	t.Helper()
	files := map[string]string{platformBinName(): content}
	if runtime.GOOS == "windows" {
		return makeZip(t, files)
	}
	return makeTarGz(t, files)
}

func overrideEndpoints(t *testing.T, srv *httptest.Server) {
	t.Helper()
	origAPI, origBase := latestAPI, downloadBase
	latestAPI, downloadBase = srv.URL+"/latest", srv.URL
	t.Cleanup(func() { latestAPI, downloadBase = origAPI, origBase })
}

func TestSelfUpdate_ExplicitVersionReplacesTarget(t *testing.T) {
	arcBytes, err := os.ReadFile(platformArchive(t, "NEWBIN"))
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeRelease(t, string(arcBytes))
	defer srv.Close()
	overrideEndpoints(t, srv)

	cur := filepath.Join(t.TempDir(), "hookrun-current")
	os.WriteFile(cur, []byte("OLD"), 0755)

	installed, err := selfUpdate("v9.9.9", cur)
	if err != nil {
		t.Fatalf("selfUpdate: %v", err)
	}
	if installed != "9.9.9" {
		t.Errorf("installed = %q, want 9.9.9 (v prefix trimmed)", installed)
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "NEWBIN" {
		t.Errorf("target content = %q, want NEWBIN", got)
	}
}

func TestSelfUpdate_LatestResolutionUsesAssetURL(t *testing.T) {
	arcBytes, err := os.ReadFile(platformArchive(t, "LATESTBIN"))
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeRelease(t, string(arcBytes))
	defer srv.Close()
	overrideEndpoints(t, srv)

	cur := filepath.Join(t.TempDir(), "hookrun-current")
	os.WriteFile(cur, []byte("OLD"), 0755)

	installed, err := selfUpdate("", cur)
	if err != nil {
		t.Fatalf("selfUpdate latest: %v", err)
	}
	if installed != "9.9.9" {
		t.Errorf("installed = %q", installed)
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "LATESTBIN" {
		t.Errorf("target content = %q, want LATESTBIN", got)
	}
}

func TestSelfUpdate_Download404KeepsTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	overrideEndpoints(t, srv)

	cur := filepath.Join(t.TempDir(), "hookrun-current")
	os.WriteFile(cur, []byte("OLD"), 0755)

	_, err := selfUpdate("9.9.9", cur)
	if err == nil || !strings.Contains(err.Error(), "download") {
		t.Fatalf("err = %v, want download error", err)
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "OLD" {
		t.Errorf("target must stay intact on download failure, got %q", got)
	}
}

func TestSelfUpdate_ArchiveWithoutBinaryKeepsTarget(t *testing.T) {
	var arc string
	if runtime.GOOS == "windows" {
		arc = makeZip(t, map[string]string{"unrelated.txt": "x"})
	} else {
		arc = makeTarGz(t, map[string]string{"unrelated.txt": "x"})
	}
	arcBytes, _ := os.ReadFile(arc)
	srv := fakeRelease(t, string(arcBytes))
	defer srv.Close()
	overrideEndpoints(t, srv)

	cur := filepath.Join(t.TempDir(), "hookrun-current")
	os.WriteFile(cur, []byte("OLD"), 0755)

	_, err := selfUpdate("9.9.9", cur)
	if err == nil || !strings.Contains(err.Error(), "extract archive") {
		t.Fatalf("err = %v, want extract error", err)
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "OLD" {
		t.Errorf("target must stay intact on extract failure, got %q", got)
	}
}

func TestSelfUpdate_CorruptArchiveKeepsTarget(t *testing.T) {
	srv := fakeRelease(t, "definitely not an archive")
	defer srv.Close()
	overrideEndpoints(t, srv)

	cur := filepath.Join(t.TempDir(), "hookrun-current")
	os.WriteFile(cur, []byte("OLD"), 0755)

	if _, err := selfUpdate("9.9.9", cur); err == nil {
		t.Fatal("expected extract error for corrupt archive")
	}
	got, _ := os.ReadFile(cur)
	if string(got) != "OLD" {
		t.Errorf("target must stay intact, got %q", got)
	}
}

// ---- CleanupOldBinary ----

func TestCleanupOldBinaryAt_RemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hookrun")
	os.WriteFile(exe, []byte("CURRENT"), 0755)
	os.WriteFile(exe+".old", []byte("OLD"), 0644)
	os.WriteFile(exe+".new", []byte("STAGED"), 0644)

	cleanupOldBinaryAt(exe)

	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Error(".old leftover must be removed")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Error(".new leftover must be removed")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "CURRENT" {
		t.Errorf("current binary must stay intact, got %q", got)
	}
}

func TestCleanupOldBinaryAt_NoLeftoversIsNoop(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hookrun")
	os.WriteFile(exe, []byte("CURRENT"), 0755)
	cleanupOldBinaryAt(exe) // must not panic or touch the binary
	got, _ := os.ReadFile(exe)
	if string(got) != "CURRENT" {
		t.Errorf("current binary must stay intact, got %q", got)
	}
}

// ---- InContainerOrCI ----

func TestInContainerOrCI_EnvGates(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("HOOKRUN_IN_DOCKER", "")
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("running inside a real container; baseline cannot be asserted")
	}
	if InContainerOrCI() {
		t.Fatal("baseline: want false")
	}
	t.Setenv("CI", "true")
	if !InContainerOrCI() {
		t.Error("CI env must gate true")
	}
	t.Setenv("CI", "")
	t.Setenv("HOOKRUN_IN_DOCKER", "1")
	if !InContainerOrCI() {
		t.Error("HOOKRUN_IN_DOCKER env must gate true")
	}
}
