package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCRDAPIVersions(t *testing.T) {
	docs := []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: servicemonitors.monitoring.coreos.com
spec:
  group: monitoring.coreos.com
  names:
    kind: ServiceMonitor
  versions:
    - name: v1
      served: true
    - name: v1alpha1
      served: false
---
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  version: v1beta1
  names:
    kind: Widget
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: not-a-crd
`)
	manifests, err := SplitYAML(docs, "app", "crds.yaml", false)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"example.com/v1beta1",
		"example.com/v1beta1/Widget",
		"monitoring.coreos.com/v1",
		"monitoring.coreos.com/v1/ServiceMonitor",
	}, CRDAPIVersions(manifests))
}
