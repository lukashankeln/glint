package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Load("")
	require.NoError(t, err)
	return cfg
}

func TestValidate_CustomSeverity(t *testing.T) {
	for _, sev := range []string{"", "error", "Warning", "INFO"} {
		cfg := validConfig(t)
		cfg.Rules.Custom = []CustomRuleDef{{ID: "r", Severity: sev}}
		assert.NoError(t, cfg.Validate(), "severity %q", sev)
	}

	cfg := validConfig(t)
	cfg.Rules.Custom = []CustomRuleDef{{ID: "r", Severity: "eror"}}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rules.custom[0] (r)")
	assert.Contains(t, err.Error(), `"eror"`)
}

func TestValidate_BuiltInSeverity(t *testing.T) {
	cfg := validConfig(t)
	cfg.Rules.BuiltIn.NoLatestTag.Severity = "Warning"
	assert.NoError(t, cfg.Validate())

	cfg.Rules.BuiltIn.ResourceRequests.Severity = "eror"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rules.built_in.resource_requests")

	cfg = validConfig(t)
	cfg.Rules.BuiltIn.DeprecatedAPIs.Severity = "bogus"
	err = cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rules.built_in.deprecated_apis")
}

func TestValidate_FailOnAndPluginSeverity(t *testing.T) {
	cfg := validConfig(t)
	cfg.FailOn = []string{"Warning"}
	assert.NoError(t, cfg.Validate())
	cfg.FailOn = []string{"bogus"}
	assert.Error(t, cfg.Validate())

	cfg = validConfig(t)
	cfg.Plugins = []PluginConfig{{Name: "p", Command: "x", Severity: "WARNING"}}
	assert.NoError(t, cfg.Validate())
	cfg.Plugins[0].Severity = "bogus"
	assert.Error(t, cfg.Validate())
}

func TestValidSeverity(t *testing.T) {
	assert.True(t, ValidSeverity("Error"))
	assert.False(t, ValidSeverity("critical"))
	assert.False(t, ValidSeverity(""))
}

func TestValidate_RenderTimeouts(t *testing.T) {
	set := map[string]func(*Config, string){
		"render.helm.timeout":      func(c *Config, v string) { c.Render.Helm.Timeout = v },
		"render.kustomize.timeout": func(c *Config, v string) { c.Render.Kustomize.Timeout = v },
	}
	for field, fn := range set {
		for _, v := range []string{"30s", ""} {
			cfg := validConfig(t)
			fn(cfg, v)
			assert.NoError(t, cfg.Validate(), "%s %q", field, v)
		}
		for _, v := range []string{"2 minutes", "0s", "-1s"} {
			cfg := validConfig(t)
			fn(cfg, v)
			err := cfg.Validate()
			require.Error(t, err, "%s %q", field, v)
			assert.Contains(t, err.Error(), field)
		}
	}
}

func TestLoad_DefaultRenderTimeouts(t *testing.T) {
	cfg := validConfig(t)
	assert.Equal(t, "120s", cfg.Render.Helm.Timeout)
	assert.Equal(t, "60s", cfg.Render.Kustomize.Timeout)
	assert.NoError(t, cfg.Validate())
}
