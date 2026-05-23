package config

const (
	DefaultKubernetesVersion = "1.36.0"
	DefaultLogLevel          = "info"
	DefaultOutputFormat      = "text"
	DefaultOutputColor       = "auto"
	DefaultConcurrency       = 0 // 0 means runtime.NumCPU()
)

var DefaultFailOn = []string{"error"}

var DefaultExcludePatterns = []string{
	"vendor/**",
	"**/.git/**",
	"**/node_modules/**",
}
