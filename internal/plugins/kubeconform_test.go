package plugins

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/rules"
)

// fixture mirrors real kubeconform -output json output (v0.8.0+).
const kubeconformFixture = `{
  "resources": [
    {
      "filename": "stdin",
      "kind": "Deployment",
      "name": "api",
      "namespace": "production",
      "version": "apps/v1",
      "status": "statusInvalid",
      "msg": "problem validating schema",
      "validationErrors": [
        {"path": "spec.template.spec.containers[0].resources", "msg": "missing property 'resources'"},
        {"path": "spec.template.spec.containers[0].image", "msg": "must not be empty"}
      ]
    },
    {
      "filename": "stdin",
      "kind": "Service",
      "name": "api",
      "namespace": "production",
      "version": "v1",
      "status": "statusValid",
      "msg": ""
    },
    {
      "filename": "stdin",
      "kind": "ConfigMap",
      "name": "config",
      "namespace": "default",
      "version": "v1",
      "status": "statusSkipped",
      "msg": "could not find schema"
    },
    {
      "filename": "stdin",
      "kind": "Ingress",
      "name": "api",
      "namespace": "production",
      "version": "networking.k8s.io/v1",
      "status": "statusError",
      "msg": "failed to download schema",
      "validationErrors": []
    }
  ]
}`

func TestParseKubeconform_FiltersStatuses(t *testing.T) {
	vs, err := parseKubeconform([]byte(kubeconformFixture), "")
	require.NoError(t, err)
	// Deployment has 2 validation errors -> 2 violations; Ingress has no validationErrors -> 1 via msg
	require.Len(t, vs, 3)
}

func TestParseKubeconform_ValidationErrorsPerField(t *testing.T) {
	vs, err := parseKubeconform([]byte(kubeconformFixture), "")
	require.NoError(t, err)

	assert.Equal(t, "Deployment", vs[0].ResourceKind)
	assert.Equal(t, "api", vs[0].ResourceName)
	assert.Equal(t, "production", vs[0].ResourceNS)
	assert.Contains(t, vs[0].Message, "missing property 'resources'")
	assert.Contains(t, vs[0].Message, "spec.template.spec.containers[0].resources")

	assert.Equal(t, "Deployment", vs[1].ResourceKind)
	assert.Contains(t, vs[1].Message, "must not be empty")

	assert.Equal(t, "Ingress", vs[2].ResourceKind)
	assert.Equal(t, "failed to download schema", vs[2].Message)
}

func TestParseKubeconform_SeverityOverride(t *testing.T) {
	vs, err := parseKubeconform([]byte(kubeconformFixture), rules.SeverityWarning)
	require.NoError(t, err)
	for _, v := range vs {
		assert.Equal(t, rules.SeverityWarning, v.Severity)
	}
}

func TestParseKubeconform_DefaultSeverityIsError(t *testing.T) {
	vs, err := parseKubeconform([]byte(kubeconformFixture), "")
	require.NoError(t, err)
	for _, v := range vs {
		assert.Equal(t, rules.SeverityError, v.Severity)
	}
}

func TestParseKubeconform_EmptyResources(t *testing.T) {
	vs, err := parseKubeconform([]byte(`{"resources":[]}`), "")
	require.NoError(t, err)
	assert.Empty(t, vs)
}

func TestParseKubeconform_OnlyValidAndSkipped(t *testing.T) {
	input := `{"resources":[
		{"kind":"Service","name":"svc","status":"statusValid","msg":""},
		{"kind":"ConfigMap","name":"cm","status":"statusSkipped","msg":""}
	]}`
	vs, err := parseKubeconform([]byte(input), "")
	require.NoError(t, err)
	assert.Empty(t, vs)
}

func TestParseKubeconform_MalformedJSON(t *testing.T) {
	_, err := parseKubeconform([]byte("not json"), "")
	assert.Error(t, err)
}

func TestParseKubeconform_RegisteredAsAdapter(t *testing.T) {
	_, ok := adapters["kubeconform"]
	assert.True(t, ok, "kubeconform adapter must be registered")
}
