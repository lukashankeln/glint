package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
)

const (
	cisDefaultNS    = "cis-5.7.4-default-namespace"
	cisClusterAdmin = "cis-5.1.1-cluster-admin-binding"
)

func boolp(b bool) *bool { return &b }

func catalogCfg(cc config.CatalogConfig) config.RulesConfig {
	cfg := defaultRulesCfg()
	cfg.Catalogs = map[string]config.CatalogConfig{"cis": cc}
	return cfg
}

func cisOnCfg() config.RulesConfig { return catalogCfg(config.CatalogConfig{Enabled: true}) }

func withOverrides(cfg config.RulesConfig, o map[string]config.BuiltInRuleOverride) config.RulesConfig {
	cfg.BuiltIn.Rules = o
	return cfg
}

func findDef(t *testing.T, defs []RuleDef, id string) RuleDef {
	t.Helper()
	for _, d := range defs {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("rule %s not loaded", id)
	return RuleDef{}
}

func TestLoadBuiltIns_CISOffByDefault(t *testing.T) {
	defs, err := LoadBuiltIns(defaultRulesCfg())
	require.NoError(t, err)
	var n int
	for _, d := range defs {
		if d.Source == "built-in:cis" {
			n++
			assert.False(t, d.Enabled, d.ID)
		}
	}
	assert.Equal(t, cisRuleCount, n)
}

func TestLoadBuiltIns_ShorthandEnablesAll(t *testing.T) {
	defs, err := LoadBuiltIns(cisOnCfg())
	require.NoError(t, err)
	var n int
	for _, d := range defs {
		if d.Source == "built-in:cis" {
			n++
			assert.True(t, d.Enabled, d.ID)
		}
	}
	assert.Equal(t, cisRuleCount, n)
	d := findDef(t, defs, cisDefaultNS)
	assert.Equal(t, SeverityWarning, d.Severity)
}

func TestLoadBuiltIns_TopLevelUnaffected(t *testing.T) {
	defs, err := LoadBuiltIns(cisOnCfg())
	require.NoError(t, err)
	for _, id := range []string{"no-latest-tag", "resource-requests", "deprecated-apis"} {
		d := findDef(t, defs, id)
		assert.Equal(t, "built-in", d.Source)
		assert.True(t, d.Enabled, id)
	}
}

func TestLoadBuiltIns_PerRuleDisableUnderShorthand(t *testing.T) {
	for _, key := range []string{cisDefaultNS, "cis_5.7.4_default_namespace"} {
		cfg := withOverrides(cisOnCfg(), map[string]config.BuiltInRuleOverride{key: {Enabled: boolp(false)}})
		defs, err := LoadBuiltIns(cfg)
		require.NoError(t, err, key)
		assert.False(t, findDef(t, defs, cisDefaultNS).Enabled, key)
		assert.True(t, findDef(t, defs, cisClusterAdmin).Enabled, key)
	}
}

func TestLoadBuiltIns_PerRuleEnableWithoutShorthand(t *testing.T) {
	for _, key := range []string{cisDefaultNS, "cis_5.7.4_default_namespace"} {
		cfg := withOverrides(defaultRulesCfg(), map[string]config.BuiltInRuleOverride{key: {Enabled: boolp(true)}})
		defs, err := LoadBuiltIns(cfg)
		require.NoError(t, err, key)
		assert.True(t, findDef(t, defs, cisDefaultNS).Enabled, key)
		assert.False(t, findDef(t, defs, cisClusterAdmin).Enabled, key)
	}
}

func TestLoadBuiltIns_SeverityPrecedence(t *testing.T) {
	// File severity.
	defs, err := LoadBuiltIns(cisOnCfg())
	require.NoError(t, err)
	assert.Equal(t, SeverityWarning, findDef(t, defs, cisDefaultNS).Severity)

	// Category beats file.
	defs, err = LoadBuiltIns(catalogCfg(config.CatalogConfig{Enabled: true, Severity: "ERROR"}))
	require.NoError(t, err)
	for _, d := range defs {
		if d.Source == "built-in:cis" {
			assert.Equal(t, SeverityError, d.Severity, d.ID)
		}
	}

	// Rule beats category; other rules keep the category severity.
	cfg := withOverrides(catalogCfg(config.CatalogConfig{Enabled: true, Severity: "error"}),
		map[string]config.BuiltInRuleOverride{cisDefaultNS: {Severity: "INFO"}})
	defs, err = LoadBuiltIns(cfg)
	require.NoError(t, err)
	assert.Equal(t, SeverityInfo, findDef(t, defs, cisDefaultNS).Severity)
	assert.Equal(t, SeverityError, findDef(t, defs, cisClusterAdmin).Severity)
	assert.True(t, findDef(t, defs, cisDefaultNS).Enabled)
}

func TestLoadBuiltIns_ConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.RulesConfig
		want string
	}{
		{"unknown catalog", func() config.RulesConfig {
			c := defaultRulesCfg()
			c.Catalogs = map[string]config.CatalogConfig{"nope": {Enabled: true}}
			return c
		}(), `rules.catalogs: unknown catalog "nope" (available: cis)`},
		{"unknown key", withOverrides(defaultRulesCfg(), map[string]config.BuiltInRuleOverride{"bogus": {Enabled: boolp(true)}}),
			"rules.built_in.bogus: unknown built-in rule"},
		{"named alias", withOverrides(defaultRulesCfg(), map[string]config.BuiltInRuleOverride{"no-latest-tag": {Enabled: boolp(false)}}),
			"rules.built_in.no-latest-tag: use rules.built_in.no_latest_tag"},
		{"duplicate", withOverrides(defaultRulesCfg(), map[string]config.BuiltInRuleOverride{
			cisDefaultNS:                  {Enabled: boolp(true)},
			"cis_5.7.4_default_namespace": {Enabled: boolp(false)},
		}), "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBuiltIns(tt.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestBuiltInCategoryRuleIDs(t *testing.T) {
	ids, err := BuiltInCategoryRuleIDs("cis")
	require.NoError(t, err)
	assert.Contains(t, ids, cisDefaultNS)
	_, err = BuiltInCategoryRuleIDs("nope")
	require.Error(t, err)
}

func TestEngine_CISDefaultNamespace(t *testing.T) {
	engine, err := NewEngine(cisOnCfg())
	require.NoError(t, err)

	bad := makeManifest("v1", "ConfigMap", "cm", "default", nil)
	good := makeManifest("v1", "ConfigMap", "cm", "prod", nil)
	vs := engine.Evaluate([]manifest.Manifest{bad, good})
	var n int
	for _, v := range vs {
		if v.RuleID == cisDefaultNS {
			n++
			assert.Contains(t, v.Message, "ConfigMap/cm is deployed to the default namespace")
		}
	}
	assert.Equal(t, 1, n)
}

func TestEngine_CISOffByDefault(t *testing.T) {
	engine, err := NewEngine(defaultRulesCfg())
	require.NoError(t, err)
	vs := engine.Evaluate([]manifest.Manifest{makeManifest("v1", "ConfigMap", "cm", "default", nil)})
	for _, v := range vs {
		assert.NotEqual(t, cisDefaultNS, v.RuleID)
	}
}

func TestEngine_CISCollisionWithCustom(t *testing.T) {
	cfg := cisOnCfg()
	cfg.Custom = []config.CustomRuleDef{{ID: cisDefaultNS, Expression: "true", Message: "m"}}
	_, err := NewEngine(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides with rule from glint.yaml")
	assert.Contains(t, err.Error(), "built-in:cis")
}

func TestEngine_DisabledCISRuleCanBeReplaced(t *testing.T) {
	cfg := withOverrides(cisOnCfg(), map[string]config.BuiltInRuleOverride{cisDefaultNS: {Enabled: boolp(false)}})
	cfg.Custom = []config.CustomRuleDef{{ID: cisDefaultNS, Expression: "false", Message: "custom fired"}}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	// Namespace "default" would trip the disabled CIS rule if it were evaluated.
	vs := engine.Evaluate([]manifest.Manifest{makeManifest("v1", "ConfigMap", "cm", "default", nil)})
	var msgs []string
	for _, v := range vs {
		if v.RuleID == cisDefaultNS {
			msgs = append(msgs, v.Message)
		}
	}
	assert.Equal(t, []string{"custom fired"}, msgs)
}

func TestEngine_CISCollisionWithRuleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule.yaml")
	require.NoError(t, os.WriteFile(path, []byte("id: "+cisDefaultNS+"\nexpression: \"true\"\nmessage: m\n"), 0o644))
	cfg := cisOnCfg()
	cfg.RuleFiles = []string{path}
	_, err := NewEngine(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading built-in rules:")
	assert.Contains(t, err.Error(), "collides with rule from "+path)
}
