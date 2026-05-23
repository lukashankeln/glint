package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration structure populated from glint.yaml.
type Config struct {
	Version   string          `yaml:"version"`
	Discovery DiscoveryConfig `yaml:"discovery"`
	Render    RenderConfig    `yaml:"render"`
	Rules     RulesConfig     `yaml:"rules"`
	Output    OutputConfig    `yaml:"output"`
	FailOn    []string        `yaml:"fail_on"`
}

type DiscoveryConfig struct {
	Paths     []string            `yaml:"paths"`
	Exclude   []string            `yaml:"exclude"`
	Overrides []DiscoveryOverride `yaml:"overrides"`
}

type DiscoveryOverride struct {
	Path     string       `yaml:"path"`
	Renderer string       `yaml:"renderer"`
	Helm     HelmOverride `yaml:"helm"`
}

type HelmOverride struct {
	ReleaseName string            `yaml:"release_name"`
	Namespace   string            `yaml:"namespace"`
	ValuesFiles []string          `yaml:"values_files"`
	Set         map[string]string `yaml:"set"`
}

type RenderConfig struct {
	Helm               HelmRenderConfig      `yaml:"helm"`
	Kustomize          KustomizeRenderConfig `yaml:"kustomize"`
	SubprocessFallback bool                  `yaml:"subprocess_fallback"`
}

type HelmRenderConfig struct {
	KubernetesVersion string   `yaml:"kubernetes_version"`
	IncludeCRDs       bool     `yaml:"include_crds"`
	APIVersions       []string `yaml:"api_versions"`
	Timeout           string   `yaml:"timeout"`
}

type KustomizeRenderConfig struct {
	EnableHelm     bool   `yaml:"enable_helm"`
	LoadRestrictor string `yaml:"load_restrictor"`
	Timeout        string `yaml:"timeout"`
}

type RulesConfig struct {
	BuiltIn    BuiltInRulesConfig `yaml:"built_in"`
	Custom     []CustomRuleDef    `yaml:"custom"`
	RuleFiles  []string           `yaml:"rule_files"`
	Exceptions []ExceptionConfig  `yaml:"exceptions"`
}

type ExceptionConfig struct {
	Rule      string                      `yaml:"rule"`
	Resources []ExceptionResourceSelector `yaml:"resources"`
}

type ExceptionResourceSelector struct {
	Kind      string `yaml:"kind"`      // exact match; empty = any
	Name      string `yaml:"name"`      // glob; empty = any
	Namespace string `yaml:"namespace"` // glob; empty = any
	Reason    string `yaml:"reason"`    // documentation only
}

type BuiltInRulesConfig struct {
	NoLatestTag      BuiltInRuleConfig `yaml:"no_latest_tag"`
	ResourceRequests BuiltInRuleConfig `yaml:"resource_requests"`
	DeprecatedAPIs   BuiltInRuleConfig `yaml:"deprecated_apis"`
}

type BuiltInRuleConfig struct {
	Enabled  bool           `yaml:"enabled"`
	Severity string         `yaml:"severity"`
	Params   map[string]any `yaml:"params"`
}

type CustomRuleDef struct {
	ID          string            `yaml:"id"`
	Description string            `yaml:"description"`
	Severity    string            `yaml:"severity"`
	Match       MatchFilterConfig `yaml:"match"`
	Expression  string            `yaml:"expression"`
	Message     string            `yaml:"message"`
}

type MatchFilterConfig struct {
	Kinds             []string          `yaml:"kinds"`
	APIGroups         []string          `yaml:"api_groups"`
	Namespaces        []string          `yaml:"namespaces"`
	ExcludeNamespaces []string          `yaml:"exclude_namespaces"`
	Labels            map[string]string `yaml:"labels"`
}

type OutputConfig struct {
	Format      string `yaml:"format"`
	Color       string `yaml:"color"`
	OutputFile  string `yaml:"output_file"`
	GroupBy     string `yaml:"group_by"`
	ShowSkipped bool   `yaml:"show_skipped"`
	Summary     bool   `yaml:"summary"`
}

// Load reads and unmarshals the config file. The config file is optional;
// if none is found, defaults are used. An explicit configFile path is an
// error if the file does not exist.
func Load(configFile string) (*Config, error) {
	cfg := defaultConfig()

	path, err := findConfigFile(configFile)
	if err != nil {
		return nil, err
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file %q: %w", path, err)
		}
		// yaml.Unmarshal only sets fields present in the document;
		// fields absent from the YAML retain their pre-populated defaults.
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %q: %w", path, err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// findConfigFile returns the path of the config file to use. If explicit is
// non-empty it is returned directly (error if it doesn't exist). Otherwise the
// standard search paths are tried and the first match is returned.
func findConfigFile(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("reading config file %q: %w", explicit, err)
		}
		return explicit, nil
	}
	for _, candidate := range []string{
		"glint.yaml", "glint.yml",
		".glint.yaml", ".glint.yml",
		".glint/config.yaml", ".glint/config.yml",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil // config is optional
}

// defaultConfig returns a Config pre-populated with all default values.
func defaultConfig() *Config {
	return &Config{
		Version: "v1alpha1",
		Discovery: DiscoveryConfig{
			Paths:   []string{"."},
			Exclude: DefaultExcludePatterns,
		},
		Render: RenderConfig{
			Helm: HelmRenderConfig{
				KubernetesVersion: DefaultKubernetesVersion,
				IncludeCRDs:       true,
				Timeout:           "120s",
			},
			Kustomize: KustomizeRenderConfig{
				EnableHelm:     false,
				LoadRestrictor: "rootOnly",
				Timeout:        "60s",
			},
			SubprocessFallback: false,
		},
		Rules: RulesConfig{
			BuiltIn: BuiltInRulesConfig{
				NoLatestTag:      BuiltInRuleConfig{Enabled: true, Severity: "error"},
				ResourceRequests: BuiltInRuleConfig{Enabled: false, Severity: "warning"},
				DeprecatedAPIs:   BuiltInRuleConfig{Enabled: true, Severity: "error"},
			},
		},
		Output: OutputConfig{
			Format:  DefaultOutputFormat,
			Color:   DefaultOutputColor,
			GroupBy: "source",
			Summary: true,
		},
		FailOn: DefaultFailOn,
	}
}

// Validate checks the config for semantic errors.
func (c *Config) Validate() error {
	for _, s := range c.FailOn {
		s = strings.ToLower(s)
		if s != "error" && s != "warning" && s != "info" {
			return fmt.Errorf("fail_on: unknown severity %q (must be error, warning, or info)", s)
		}
	}

	validFormats := map[string]bool{
		"text": true, "json": true, "sarif": true,
		"junit": true, "github-actions": true,
	}
	if !validFormats[c.Output.Format] {
		return fmt.Errorf("output.format: unknown format %q", c.Output.Format)
	}

	for i, exc := range c.Rules.Exceptions {
		if exc.Rule == "" {
			return fmt.Errorf("rules.exceptions[%d]: missing required 'rule' field", i)
		}
		if len(exc.Resources) == 0 {
			return fmt.Errorf("rules.exceptions[%s]: 'resources' list must not be empty", exc.Rule)
		}
	}

	return nil
}
