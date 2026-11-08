package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/render"
)

const (
	cisFixtureDir  = "testdata/catalogs/cis"
	expectLabel    = "glint.test/expect"
	expectViolates = "violation"
	expectComplies = "compliant"
	cisRuleCount   = 29
)

var cisDescRE = regexp.MustCompile(`^CIS (5\.\d+\.\d+): `)

type cisFixtureDoc struct {
	m      manifest.Manifest
	expect string
}

func loadCISFixture(t *testing.T, id string) []cisFixtureDoc {
	t.Helper()
	path := filepath.Join(cisFixtureDir, id+".yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "fixture for %s", id)
	ms, err := render.SplitYAML(data, "cis-fixture", path, false)
	require.NoError(t, err, path)
	var docs []cisFixtureDoc
	for _, m := range ms {
		md, _ := m.Object["metadata"].(map[string]any)
		labels, _ := md["labels"].(map[string]any)
		exp, _ := labels[expectLabel].(string)
		require.Contains(t, []string{expectViolates, expectComplies}, exp, "%s: %s/%s has no valid %s label", path, m.Kind, m.Name, expectLabel)
		docs = append(docs, cisFixtureDoc{m: m, expect: exp})
	}
	return docs
}

func cisEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := NewEngine(catalogCfg(config.CatalogConfig{Enabled: true}))
	require.NoError(t, err)
	return engine
}

func TestCISCatalog_Fixtures(t *testing.T) {
	engine := cisEngine(t)
	ids, err := CatalogRuleIDs("cis")
	require.NoError(t, err)
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			for _, d := range loadCISFixture(t, id) {
				var n int
				for _, v := range engine.Evaluate([]manifest.Manifest{d.m}) {
					if v.RuleID == id {
						n++
					}
				}
				want := 0
				if d.expect == expectViolates {
					want = 1
				}
				assert.Equal(t, want, n, "%s/%s (%s) expected %s", d.m.Kind, d.m.Name, d.m.APIVersion, d.expect)
			}
		})
	}
}

func TestCISCatalog_NoEvalErrors(t *testing.T) {
	engine := cisEngine(t)
	ids, err := CatalogRuleIDs("cis")
	require.NoError(t, err)
	for _, id := range ids {
		for _, d := range loadCISFixture(t, id) {
			for _, v := range engine.Evaluate([]manifest.Manifest{d.m}) {
				assert.False(t, strings.HasPrefix(v.Message, "CEL evaluation error"),
					"fixture %s doc %s/%s: rule %s: %s", id, d.m.Kind, d.m.Name, v.RuleID, v.Message)
			}
		}
	}
}

func TestCISCatalog_Coverage(t *testing.T) {
	ids, err := CatalogRuleIDs("cis")
	require.NoError(t, err)
	assert.Len(t, ids, cisRuleCount)

	defs, err := LoadCatalogs(catalogCfg(config.CatalogConfig{Enabled: true}))
	require.NoError(t, err)
	require.Len(t, defs, cisRuleCount)

	for _, d := range defs {
		t.Run(d.ID, func(t *testing.T) {
			require.True(t, strings.HasPrefix(d.ID, "cis-"), "id prefix")
			m := cisDescRE.FindStringSubmatch(d.Description)
			require.NotNil(t, m, "description %q", d.Description)
			parts := strings.SplitN(d.ID, "-", 3)
			require.Len(t, parts, 3)
			assert.Equal(t, parts[1], m[1], "control number in id vs description")
			assert.True(t, config.ValidSeverity(string(d.Severity)), "severity %q", d.Severity)
			if d.ID != cisDefaultNS {
				assert.NotEmpty(t, d.Match.Kinds, "match.kinds")
			}
			assert.NotEmpty(t, d.Message)

			docs := loadCISFixture(t, d.ID)
			seen := map[string]bool{}
			for _, fd := range docs {
				seen[fd.expect] = true
			}
			assert.True(t, seen[expectViolates], "fixture needs a violating doc")
			assert.True(t, seen[expectComplies], "fixture needs a compliant doc")
		})
	}
}
