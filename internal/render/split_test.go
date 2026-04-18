package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitYAML_MultiDoc(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: default
---
apiVersion: v1
kind: Service
metadata:
  name: svc1
  namespace: default
`)
	manifests, err := SplitYAML(data, "test-app", "test.yaml", false)
	require.NoError(t, err)
	assert.Len(t, manifests, 2)
	assert.Equal(t, "ConfigMap", manifests[0].Kind)
	assert.Equal(t, "cm1", manifests[0].Name)
	assert.Equal(t, "default", manifests[0].Namespace)
	assert.Equal(t, "Service", manifests[1].Kind)
	assert.Equal(t, "svc1", manifests[1].Name)
	assert.Equal(t, "test-app", manifests[0].SourceApp)
	assert.Equal(t, "test.yaml", manifests[0].SourcePath)
	assert.False(t, manifests[0].Rendered)
}

func TestSplitYAML_RenderedFlag(t *testing.T) {
	data := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: deploy1
`)
	manifests, err := SplitYAML(data, "app", "<rendered>", true)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.True(t, manifests[0].Rendered)
	assert.Equal(t, "<rendered>", manifests[0].SourcePath)
}

func TestSplitYAML_EmptyDocs(t *testing.T) {
	data := []byte(`
---
---
apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
---
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	assert.Len(t, manifests, 1)
	assert.Equal(t, "Namespace", manifests[0].Kind)
}

func TestSplitYAML_CommentOnly(t *testing.T) {
	data := []byte(`
# This is just a comment
# No actual YAML here
---
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	// Comment-only documents should be skipped
	assert.Len(t, manifests, 1)
	assert.Equal(t, "Pod", manifests[0].Kind)
}

func TestSplitYAML_MissingKind(t *testing.T) {
	data := []byte(`
apiVersion: v1
metadata:
  name: no-kind
---
apiVersion: v1
kind: Secret
metadata:
  name: real-secret
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	// The doc without kind should be skipped
	assert.Len(t, manifests, 1)
	assert.Equal(t, "Secret", manifests[0].Kind)
}

func TestSplitYAML_MissingAPIVersion(t *testing.T) {
	data := []byte(`
kind: Deployment
metadata:
  name: no-apiversion
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: valid-deploy
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	assert.Len(t, manifests, 1)
	assert.Equal(t, "valid-deploy", manifests[0].Name)
}

func TestSplitYAML_ObjectPopulated(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-cm
  namespace: prod
data:
  key: value
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.NotNil(t, manifests[0].Object)
	assert.Equal(t, "v1", manifests[0].Object["apiVersion"])
	assert.Equal(t, "ConfigMap", manifests[0].Object["kind"])
}

func TestSplitYAML_RawBytes(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-cm
`)
	manifests, err := SplitYAML(data, "app", "file.yaml", false)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	assert.NotEmpty(t, manifests[0].Raw)
}

func TestMergeValues(t *testing.T) {
	dst := map[string]any{
		"a": "orig",
		"b": map[string]any{"x": 1},
	}
	src := map[string]any{
		"a": "new",
		"b": map[string]any{"y": 2},
	}
	result := mergeValues(dst, src)
	assert.Equal(t, "new", result["a"])
	nested := result["b"].(map[string]any)
	assert.Equal(t, 1, nested["x"])
	assert.Equal(t, 2, nested["y"])
}

func TestHelmSetToValues(t *testing.T) {
	set := map[string]string{
		"image.tag":        "v1.0",
		"replicaCount":     "3",
		"service.port":     "8080",
	}
	vals := helmSetToValues(set)
	imgMap, ok := vals["image"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v1.0", imgMap["tag"])
	assert.Equal(t, "3", vals["replicaCount"])
	svcMap, ok := vals["service"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "8080", svcMap["port"])
}
