package cli

import (
	"bytes"
	"encoding/json"
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
