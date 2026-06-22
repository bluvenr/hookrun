package version

// Version is the application version string.
// It can be overridden at build time via:
//
//	-ldflags "-X github.com/bluvenr/hookrun/internal/version.Version=x.y.z"
var Version = "1.1.3"

// BuildTime is the build timestamp, set via ldflags.
var BuildTime = "unknown"
