package version

// These variables are injected at build time via -ldflags.
var (
	Version = "0.1.9" // x-release-please-version
	Commit  = "none"
	Date    = "unknown"
)
