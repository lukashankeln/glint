package version

// These variables are injected at build time via -ldflags.
var (
	Version = "0.1.0"
	Commit  = "none"
	Date    = "unknown"
)
