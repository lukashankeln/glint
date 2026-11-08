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

const cisDefaultNS = "cis-5.7.4-default-namespace"

func catalogCfg(cc config.CatalogConfig) config.RulesConfig {
	cfg := defaultRulesCfg()
	cfg.Catalogs = map[string]config.CatalogConfig{"cis": cc}
	return cfg
}

func TestLoadCatalogs_NoConfigLoadsNothing(t *testing.T) {
	defs, err := LoadCatalogs(defaultRulesCfg())
	require.NoError(t, err)
	assert.Empty(t, defs)
}

func TestLoadCatalogs_Disabled(t *testing.T) {
	defs, err := LoadCatalogs(catalogCfg(config.CatalogConfig{Enabled: false}))
	require.NoError(t, err)
	assert.Empty(t, defs)
}

func TestLoadCatalogs_Enabled(t *testing.T) {
	defs, err := LoadCatalogs(catalogCfg(config.CatalogConfig{Enabled: true}))
	require.NoError(t, err)
	var found *RuleDef
	for i := range defs {
		if defs[i].ID == cisDefaultNS {
			found = &defs[i]
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "catalog:cis", found.Source)
	assert.True(t, found.Enabled)
	assert.Equal(t, SeverityWarning, found.Severity)
}

func TestLoadCatalogs_SeverityOverride(t *testing.T) {
	defs, err := LoadCatalogs(catalogCfg(config.CatalogConfig{Enabled: true, Severity: "ERROR"}))
	require.NoError(t, err)
	require.NotEmpty(t, defs)
	for _, d := range defs {
		assert.Equal(t, SeverityError, d.Severity, d.ID)
	}
}

func TestLoadCatalogs_UnknownCatalog(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.Catalogs = map[string]config.CatalogConfig{"nope": {Enabled: true}}
	_, err := LoadCatalogs(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `rules.catalogs: unknown catalog "nope" (available: cis)`)
}

func TestLoadCatalogs_UnknownExclude(t *testing.T) {
	_, err := LoadCatalogs(catalogCfg(config.CatalogConfig{Enabled: true, ExcludeRules: []string{"bogus"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `rules.catalogs.cis.exclude_rules: unknown rule "bogus"`)
}

func TestLoadCatalogs_Excluded(t *testing.T) {
	defs, err := LoadCatalogs(catalogCfg(config.CatalogConfig{Enabled: true, ExcludeRules: []string{cisDefaultNS}}))
	require.NoError(t, err)
	for _, d := range defs {
		if d.ID == cisDefaultNS {
			assert.False(t, d.Enabled)
			return
		}
	}
	t.Fatal("excluded rule not listed")
}

func TestCatalogRuleIDs(t *testing.T) {
	ids, err := CatalogRuleIDs("cis")
	require.NoError(t, err)
	assert.Contains(t, ids, cisDefaultNS)
	_, err = CatalogRuleIDs("nope")
	require.Error(t, err)
}

func TestEngine_CatalogDefaultNamespace(t *testing.T) {
	engine, err := NewEngine(catalogCfg(config.CatalogConfig{Enabled: true}))
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

func TestEngine_CatalogOffByDefault(t *testing.T) {
	engine, err := NewEngine(defaultRulesCfg())
	require.NoError(t, err)
	vs := engine.Evaluate([]manifest.Manifest{makeManifest("v1", "ConfigMap", "cm", "default", nil)})
	for _, v := range vs {
		assert.NotEqual(t, cisDefaultNS, v.RuleID)
	}
}

func TestEngine_CatalogCollisionWithCustom(t *testing.T) {
	cfg := catalogCfg(config.CatalogConfig{Enabled: true})
	cfg.Custom = []config.CustomRuleDef{{ID: cisDefaultNS, Expression: "true", Message: "m"}}
	_, err := NewEngine(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides with rule from glint.yaml")
	assert.Contains(t, err.Error(), "catalog:cis")
}

func TestEngine_ExcludedCatalogRuleCanBeReplaced(t *testing.T) {
	cfg := catalogCfg(config.CatalogConfig{Enabled: true, ExcludeRules: []string{cisDefaultNS}})
	cfg.Custom = []config.CustomRuleDef{{ID: cisDefaultNS, Expression: "false", Message: "custom fired"}}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	// Namespace "default" would trip the excluded catalog rule if it were evaluated.
	vs := engine.Evaluate([]manifest.Manifest{makeManifest("v1", "ConfigMap", "cm", "default", nil)})
	var msgs []string
	for _, v := range vs {
		if v.RuleID == cisDefaultNS {
			msgs = append(msgs, v.Message)
		}
	}
	assert.Equal(t, []string{"custom fired"}, msgs)
}

func TestEngine_CatalogCollisionWithRuleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule.yaml")
	require.NoError(t, os.WriteFile(path, []byte("id: "+cisDefaultNS+"\nexpression: \"true\"\nmessage: m\n"), 0o644))
	cfg := catalogCfg(config.CatalogConfig{Enabled: true})
	cfg.RuleFiles = []string{path}
	_, err := NewEngine(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading rule catalogs:")
	assert.Contains(t, err.Error(), "collides with rule from "+path)
}
