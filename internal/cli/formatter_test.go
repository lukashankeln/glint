package cli

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/rules"
)

func decodeSARIFRun(t *testing.T, violations []rules.Violation) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, writeSARIF(&buf, violations))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc))
	assert.Equal(t, "2.1.0", doc["version"])

	runs, ok := doc["runs"].([]any)
	require.True(t, ok, "runs must be an array")
	require.Len(t, runs, 1)
	run, ok := runs[0].(map[string]any)
	require.True(t, ok)
	return run
}

func TestWriteSARIF_NoViolations_EmitsEmptyArrays(t *testing.T) {
	run := decodeSARIFRun(t, nil)

	results, ok := run["results"].([]any)
	require.True(t, ok, "results must be a JSON array, got %#v", run["results"])
	assert.Empty(t, results)

	driver := run["tool"].(map[string]any)["driver"].(map[string]any)
	ruleList, ok := driver["rules"].([]any)
	require.True(t, ok, "rules must be a JSON array, got %#v", driver["rules"])
	assert.Empty(t, ruleList)
}

func TestWriteSARIF_WithViolation(t *testing.T) {
	run := decodeSARIFRun(t, []rules.Violation{{
		RuleID:   "no-latest-tag",
		Severity: rules.SeverityError,
		Message:  "image uses latest tag",
		FilePath: "apps/web/deployment.yaml",
	}})

	results := run["results"].([]any)
	require.Len(t, results, 1)
	result := results[0].(map[string]any)
	assert.Equal(t, "no-latest-tag", result["ruleId"])
	assert.Equal(t, "error", result["level"])
	loc := result["locations"].([]any)[0].(map[string]any)
	uri := loc["physicalLocation"].(map[string]any)["artifactLocation"].(map[string]any)["uri"]
	assert.Equal(t, "apps/web/deployment.yaml", uri)

	driver := run["tool"].(map[string]any)["driver"].(map[string]any)
	ruleList := driver["rules"].([]any)
	require.Len(t, ruleList, 1)
	assert.Equal(t, "no-latest-tag", ruleList[0].(map[string]any)["id"])
}

func hostileViolations(t *testing.T) []rules.Violation {
	t.Helper()
	raw := `[{"rule_id":"r\u001b[31m1","message":"line1\nline2 \u202eevil \u009b","file":"dir/\u001b]0;x\u0007.yaml","kind":"Pod\u202e","name":"n\r\nx","namespace":"ns\u001b","api_version":"v1\u009b","source":"cel\u001b"},
{"rule_id":"x::y","message":"m","file":"a%0Ab:c,d"}]`
	var in []struct {
		RuleID     string `json:"rule_id"`
		Message    string `json:"message"`
		File       string `json:"file"`
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		Namespace  string `json:"namespace"`
		APIVersion string `json:"api_version"`
		Source     string `json:"source"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &in))
	var out []rules.Violation
	for _, p := range in {
		out = append(out, rules.Violation{
			RuleID: p.RuleID, Severity: rules.SeverityError, Message: p.Message,
			Source: p.Source, APIVersion: p.APIVersion, ResourceKind: p.Kind,
			ResourceName: p.Name, ResourceNS: p.Namespace, FilePath: p.File,
		})
	}
	return out
}

func assertNoRawControl(t *testing.T, out string) {
	t.Helper()
	for _, r := range strings.TrimSuffix(out, "\n") {
		if r == '\n' {
			continue
		}
		assert.True(t, strconv.IsPrint(r), "raw non-printable %U in %q", r, out)
	}
}

func TestWriteTextEscapesHostileFields(t *testing.T) {
	vs := hostileViolations(t)
	var buf bytes.Buffer
	require.NoError(t, writeText(&buf, vs))
	out := buf.String()
	assert.Equal(t, len(vs), strings.Count(out, "\n"))
	assertNoRawControl(t, out)
	assert.Contains(t, out, `line1\nline2`)
}

func TestWriteGitHubActionsEscapesHostileFields(t *testing.T) {
	vs := hostileViolations(t)
	var buf bytes.Buffer
	require.NoError(t, writeGitHubActions(&buf, vs))
	out := buf.String()
	assert.Equal(t, len(vs), strings.Count(out, "\n"))
	assertNoRawControl(t, out)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[1], "file=a%250Ab%3Ac%2Cd,")
	assert.Contains(t, lines[1], "x%3A%3Ay")
}

func TestWriteTextAndGitHubActionsPlainFormatUnchanged(t *testing.T) {
	v := []rules.Violation{{
		RuleID: "r1", Severity: rules.SeverityWarning, Message: "bad thing",
		ResourceKind: "Pod", ResourceName: "p", ResourceNS: "ns", APIVersion: "v1",
		FilePath: "a/b.yaml",
	}}
	var buf bytes.Buffer
	require.NoError(t, writeText(&buf, v))
	assert.Equal(t, "[WARNING] cel  Pod/ns/p (v1): bad thing  [r1]\n", buf.String())

	buf.Reset()
	require.NoError(t, writeGitHubActions(&buf, v))
	assert.Equal(t, "::warning file=a/b.yaml,title=Pod/ns/p (v1) [r1]::Pod/ns/p (v1): bad thing  [r1]\n", buf.String())
}
