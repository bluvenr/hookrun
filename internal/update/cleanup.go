package update

import (
	"os"
	"path/filepath"
)

// CleanupOldBinary removes the leftover ".old" binary from a previous
// Windows self-update. It is best-effort and never returns an error.
func CleanupOldBinary() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	_ = os.Remove(exe + ".old")
	_ = os.Remove(filepath.Join(filepath.Dir(exe), filepath.Base(exe)+".new"))
}
