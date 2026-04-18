package config

const (
	DefaultKubernetesVersion = "1.29.0"
	DefaultLogLevel          = "info"
	DefaultOutputFormat      = "text"
	DefaultOutputColor       = "auto"
	DefaultConcurrency       = 0 // 0 means runtime.NumCPU()
)

var DefaultFailOn = []string{"error"}

var DefaultSchemaLocations = []string{
	"https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/{{ .NormalizedKubernetesVersion }}-standalone{{ .StrictSuffix }}/{{ .ResourceKind }}{{ .KindSuffix }}.json",
	"https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
}

var DefaultExcludePatterns = []string{
	"vendor/**",
	"**/.git/**",
	"**/node_modules/**",
}
