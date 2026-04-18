package rules

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
)

func defaultRulesCfg() config.RulesConfig {
	cfg, _ := config.Load("")
	return cfg.Rules
}

// makeManifest constructs a minimal Manifest for testing.
func makeManifest(apiVersion, kind, name, namespace string, obj map[string]any) manifest.Manifest {
	if obj == nil {
		obj = map[string]any{}
	}
	obj["apiVersion"] = apiVersion
	obj["kind"] = kind
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		meta = map[string]any{}
		obj["metadata"] = meta
	}
	meta["name"] = name
	meta["namespace"] = namespace
	return manifest.Manifest{
		APIVersion: apiVersion,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		Object:     obj,
	}
}

func TestEngine_BadCELExpression(t *testing.T) {
	cfg := config.RulesConfig{
		Custom: []config.CustomRuleDef{
			{
				ID:         "bad-rule",
				Severity:   "error",
				Expression: "this is not valid CEL !!!",
				Message:    "bad",
			},
		},
	}
	cfg.BuiltIn.NoLatestTag.Enabled = false
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false
	_, err := NewEngine(cfg)
	assert.Error(t, err, "expected compile error for invalid CEL")
}

func TestEngine_NoLatestTag_Violation(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	m := makeManifest("apps/v1", "Deployment", "my-app", "default", map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "app", "image": "nginx:latest"},
					},
				},
			},
		},
	})

	violations := engine.Evaluate([]manifest.Manifest{m})
	ruleIDs := violationIDs(violations)
	assert.Contains(t, ruleIDs, "no-latest-tag")
}

func TestEngine_NoLatestTag_Compliant(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	m := makeManifest("apps/v1", "Deployment", "my-app", "default", map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "app", "image": "nginx:1.21"},
					},
				},
			},
		},
	})

	violations := engine.Evaluate([]manifest.Manifest{m})
	assert.NotContains(t, violationIDs(violations), "no-latest-tag")
}

func TestEngine_NoLatestTag_SkipsNonMatchingKind(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	// ConfigMap doesn't match no-latest-tag's kind filter.
	m := makeManifest("v1", "ConfigMap", "my-config", "default", nil)
	violations := engine.Evaluate([]manifest.Manifest{m})
	assert.Empty(t, violations)
}

func TestEngine_DeprecatedAPIs_Violation(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.NoLatestTag.Enabled = false

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	m := makeManifest("apps/v1beta1", "Deployment", "old-app", "default", nil)
	violations := engine.Evaluate([]manifest.Manifest{m})
	require.Len(t, violations, 1)
	assert.Equal(t, "deprecated-apis", violations[0].RuleID)
}

func TestEngine_DeprecatedAPIs_Compliant(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.NoLatestTag.Enabled = false

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	m := makeManifest("apps/v1", "Deployment", "my-app", "default", nil)
	violations := engine.Evaluate([]manifest.Manifest{m})
	assert.Empty(t, violations)
}

func TestEngine_ResourceRequests_Violation(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.NoLatestTag.Enabled = false
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false
	cfg.BuiltIn.ResourceRequests.Enabled = true

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	// Container without resource requests.
	m := makeManifest("apps/v1", "Deployment", "my-app", "default", map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "app", "image": "nginx:1.21"},
					},
				},
			},
		},
	})

	violations := engine.Evaluate([]manifest.Manifest{m})
	require.Len(t, violations, 1)
	assert.Equal(t, "resource-requests", violations[0].RuleID)
}

func TestEngine_ResourceRequests_Compliant(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.NoLatestTag.Enabled = false
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false
	cfg.BuiltIn.ResourceRequests.Enabled = true

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	m := makeManifest("apps/v1", "Deployment", "my-app", "default", map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"image": "nginx:1.21",
							"resources": map[string]any{
								"requests": map[string]any{
									"cpu":    "100m",
									"memory": "128Mi",
								},
							},
						},
					},
				},
			},
		},
	})

	violations := engine.Evaluate([]manifest.Manifest{m})
	assert.Empty(t, violations)
}

func TestEngine_MatchFilter_APIGroup(t *testing.T) {
	cfg := config.RulesConfig{
		Custom: []config.CustomRuleDef{
			{
				ID:       "argocd-check",
				Severity: "error",
				Match: config.MatchFilterConfig{
					Kinds:     []string{"Application"},
					APIGroups: []string{"argoproj.io"},
				},
				Expression: `name != ""`,
				Message:    "test",
			},
		},
	}
	cfg.BuiltIn.NoLatestTag.Enabled = false
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false

	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	argoApp := makeManifest("argoproj.io/v1alpha1", "Application", "my-app", "argocd", nil)
	k8sApp := makeManifest("apps/v1", "Application", "my-app", "default", nil)

	vs := engine.Evaluate([]manifest.Manifest{argoApp})
	assert.Empty(t, vs, "argocd Application with non-empty name should pass")

	vs = engine.Evaluate([]manifest.Manifest{k8sApp})
	assert.Empty(t, vs, "apps/v1 Application should be skipped by api_groups filter")
}

func latestDeployment(name, namespace string) manifest.Manifest {
	return makeManifest("apps/v1", "Deployment", name, namespace, map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "app", "image": "nginx:latest"},
					},
				},
			},
		},
	})
}

func noLatestTagOnlyCfg() config.RulesConfig {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false
	cfg.BuiltIn.ResourceRequests.Enabled = false
	return cfg
}

func TestEngine_Exception_SkipsMatchedResource(t *testing.T) {
	cfg := noLatestTagOnlyCfg()
	cfg.Exceptions = []config.ExceptionConfig{
		{Rule: "no-latest-tag", Resources: []config.ExceptionResourceSelector{
			{Kind: "Deployment", Name: "my-app", Namespace: "default"},
		}},
	}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	violations := engine.Evaluate([]manifest.Manifest{latestDeployment("my-app", "default")})
	assert.NotContains(t, violationIDs(violations), "no-latest-tag")
}

func TestEngine_Exception_DoesNotSkipNonMatchingResource(t *testing.T) {
	cfg := noLatestTagOnlyCfg()
	cfg.Exceptions = []config.ExceptionConfig{
		{Rule: "no-latest-tag", Resources: []config.ExceptionResourceSelector{
			{Name: "my-app", Namespace: "default"},
		}},
	}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	violations := engine.Evaluate([]manifest.Manifest{latestDeployment("other-app", "default")})
	assert.Contains(t, violationIDs(violations), "no-latest-tag")
}

func TestEngine_Exception_GlobName(t *testing.T) {
	cfg := noLatestTagOnlyCfg()
	cfg.Exceptions = []config.ExceptionConfig{
		{Rule: "no-latest-tag", Resources: []config.ExceptionResourceSelector{
			{Name: "postgres-*", Namespace: "postgres"},
		}},
	}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	matched := latestDeployment("postgres-primary", "postgres")
	notMatched := latestDeployment("redis-primary", "postgres")

	assert.NotContains(t, violationIDs(engine.Evaluate([]manifest.Manifest{matched})), "no-latest-tag")
	assert.Contains(t, violationIDs(engine.Evaluate([]manifest.Manifest{notMatched})), "no-latest-tag")
}

func TestEngine_Exception_EmptyNamespaceMatchesAny(t *testing.T) {
	cfg := noLatestTagOnlyCfg()
	cfg.Exceptions = []config.ExceptionConfig{
		{Rule: "no-latest-tag", Resources: []config.ExceptionResourceSelector{
			{Name: "my-app"},
		}},
	}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	violations := engine.Evaluate([]manifest.Manifest{latestDeployment("my-app", "staging")})
	assert.NotContains(t, violationIDs(violations), "no-latest-tag")
}

func TestEngine_Exception_MultipleRules(t *testing.T) {
	cfg := defaultRulesCfg()
	cfg.BuiltIn.DeprecatedAPIs.Enabled = false
	cfg.BuiltIn.ResourceRequests.Enabled = true
	cfg.Exceptions = []config.ExceptionConfig{
		{Rule: "no-latest-tag", Resources: []config.ExceptionResourceSelector{{Name: "my-app"}}},
		{Rule: "resource-requests", Resources: []config.ExceptionResourceSelector{{Name: "my-app"}}},
	}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	excepted := makeManifest("apps/v1", "Deployment", "my-app", "default", map[string]any{
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"name": "app", "image": "nginx:latest"}},
		}}},
	})
	other := makeManifest("apps/v1", "Deployment", "other-app", "default", map[string]any{
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"name": "app", "image": "nginx:latest"}},
		}}},
	})

	assert.Empty(t, violationIDs(engine.Evaluate([]manifest.Manifest{excepted})))

	ids := violationIDs(engine.Evaluate([]manifest.Manifest{other}))
	assert.Contains(t, ids, "no-latest-tag")
	assert.Contains(t, ids, "resource-requests")
}

func TestEngine_Exception_UnknownRuleIDIsHarmless(t *testing.T) {
	cfg := noLatestTagOnlyCfg()
	cfg.Exceptions = []config.ExceptionConfig{
		{Rule: "does-not-exist", Resources: []config.ExceptionResourceSelector{{Name: "any"}}},
	}
	engine, err := NewEngine(cfg)
	require.NoError(t, err)

	violations := engine.Evaluate([]manifest.Manifest{latestDeployment("my-app", "default")})
	assert.Contains(t, violationIDs(violations), "no-latest-tag")
}

func violationIDs(vs []Violation) []string {
	ids := make([]string, len(vs))
	for i, v := range vs {
		ids[i] = v.RuleID
	}
	return ids
}
