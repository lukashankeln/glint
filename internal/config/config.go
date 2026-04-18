package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config is the top-level configuration structure populated from glint.yaml.
type Config struct {
	Version   string          `mapstructure:"version"`
	Discovery DiscoveryConfig `mapstructure:"discovery"`
	Render    RenderConfig    `mapstructure:"render"`
	Rules     RulesConfig     `mapstructure:"rules"`
	Output    OutputConfig    `mapstructure:"output"`
	FailOn    []string        `mapstructure:"fail_on"`
}

type DiscoveryConfig struct {
	Paths     []string            `mapstructure:"paths"`
	Exclude   []string            `mapstructure:"exclude"`
	Overrides []DiscoveryOverride `mapstructure:"overrides"`
}

type DiscoveryOverride struct {
	Path     string       `mapstructure:"path"`
	Renderer string       `mapstructure:"renderer"`
	Helm     HelmOverride `mapstructure:"helm"`
}

type HelmOverride struct {
	ReleaseName string            `mapstructure:"release_name"`
	Namespace   string            `mapstructure:"namespace"`
	ValuesFiles []string          `mapstructure:"values_files"`
	Set         map[string]string `mapstructure:"set"`
}

type RenderConfig struct {
	Helm               HelmRenderConfig      `mapstructure:"helm"`
	Kustomize          KustomizeRenderConfig `mapstructure:"kustomize"`
	SubprocessFallback bool                  `mapstructure:"subprocess_fallback"`
}

type HelmRenderConfig struct {
	KubernetesVersion string   `mapstructure:"kubernetes_version"`
	IncludeCRDs       bool     `mapstructure:"include_crds"`
	APIVersions       []string `mapstructure:"api_versions"`
	Timeout           string   `mapstructure:"timeout"`
}

type KustomizeRenderConfig struct {
	EnableHelm     bool   `mapstructure:"enable_helm"`
	LoadRestrictor string `mapstructure:"load_restrictor"`
	Timeout        string `mapstructure:"timeout"`
}

type RulesConfig struct {
	BuiltIn    BuiltInRulesConfig `mapstructure:"built_in"`
	Custom     []CustomRuleDef    `mapstructure:"custom"`
	RuleFiles  []string           `mapstructure:"rule_files"`
	Exceptions []ExceptionConfig  `mapstructure:"exceptions"`
}

type ExceptionConfig struct {
	Rule      string                      `mapstructure:"rule"`
	Resources []ExceptionResourceSelector `mapstructure:"resources"`
}

type ExceptionResourceSelector struct {
	Kind      string `mapstructure:"kind"`      // exact match; empty = any
	Name      string `mapstructure:"name"`      // glob; empty = any
	Namespace string `mapstructure:"namespace"` // glob; empty = any
	Reason    string `mapstructure:"reason"`    // documentation only
}

type BuiltInRulesConfig struct {
	NoLatestTag      BuiltInRuleConfig `mapstructure:"no_latest_tag"`
	ResourceRequests BuiltInRuleConfig `mapstructure:"resource_requests"`
	DeprecatedAPIs   BuiltInRuleConfig `mapstructure:"deprecated_apis"`
}

type BuiltInRuleConfig struct {
	Enabled  bool           `mapstructure:"enabled"`
	Severity string         `mapstructure:"severity"`
	Params   map[string]any `mapstructure:"params"`
}

type CustomRuleDef struct {
	ID          string            `mapstructure:"id"`
	Description string            `mapstructure:"description"`
	Severity    string            `mapstructure:"severity"`
	Match       MatchFilterConfig `mapstructure:"match"`
	Expression  string            `mapstructure:"expression"`
	Message     string            `mapstructure:"message"`
}

type MatchFilterConfig struct {
	Kinds             []string          `mapstructure:"kinds"`
	APIGroups         []string          `mapstructure:"api_groups"`
	Namespaces        []string          `mapstructure:"namespaces"`
	ExcludeNamespaces []string          `mapstructure:"exclude_namespaces"`
	Labels            map[string]string `mapstructure:"labels"`
}

type OutputConfig struct {
	Format      string `mapstructure:"format"`
	Color       string `mapstructure:"color"`
	OutputFile  string `mapstructure:"output_file"`
	GroupBy     string `mapstructure:"group_by"`
	ShowSkipped bool   `mapstructure:"show_skipped"`
	Summary     bool   `mapstructure:"summary"`
}

// Load reads and unmarshals the config file found via viper's search path.
// If configFile is non-empty, it is used directly; otherwise viper searches
// for glint.yaml / .glint.yaml / .glint/config.yaml in the working directory.
func Load(configFile string) (*Config, error) {
	v := viper.New()

	setDefaults(v)

	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("glint")
		v.AddConfigPath(".")
		v.SetConfigName(".glint")
		v.AddConfigPath(".")
		v.AddConfigPath(".glint")
	}

	if err := v.ReadInConfig(); err != nil {
		// Config file is optional — only error if one was explicitly provided
		if configFile != "" {
			return nil, fmt.Errorf("reading config file %q: %w", configFile, err)
		}
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// File was found but couldn't be parsed
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
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

func setDefaults(v *viper.Viper) {
	v.SetDefault("version", "v1alpha1")
	v.SetDefault("discovery.paths", []string{"."})
	v.SetDefault("discovery.exclude", DefaultExcludePatterns)
	v.SetDefault("render.helm.kubernetes_version", DefaultKubernetesVersion)
	v.SetDefault("render.helm.include_crds", true)
	v.SetDefault("render.helm.timeout", "120s")
	v.SetDefault("render.kustomize.enable_helm", false)
	v.SetDefault("render.kustomize.load_restrictor", "rootOnly")
	v.SetDefault("render.kustomize.timeout", "60s")
	v.SetDefault("render.subprocess_fallback", false)
	v.SetDefault("rules.built_in.no_latest_tag.enabled", true)
	v.SetDefault("rules.built_in.no_latest_tag.severity", "error")
	v.SetDefault("rules.built_in.resource_requests.enabled", false)
	v.SetDefault("rules.built_in.resource_requests.severity", "warning")
	v.SetDefault("rules.built_in.deprecated_apis.enabled", true)
	v.SetDefault("rules.built_in.deprecated_apis.severity", "error")
	v.SetDefault("output.format", DefaultOutputFormat)
	v.SetDefault("output.color", DefaultOutputColor)
	v.SetDefault("output.group_by", "source")
	v.SetDefault("output.summary", true)
	v.SetDefault("fail_on", DefaultFailOn)
}
