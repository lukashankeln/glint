package builtins

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The generated default must stay equivalent to the embedded rule, so that
// setting init_containers: true changes nothing.
func TestResourceRequestsExpression_MatchesEmbeddedDefault(t *testing.T) {
	data, err := os.ReadFile("../builtin/resource-limits.yaml")
	require.NoError(t, err)
	var rule struct {
		Expression string `yaml:"expression"`
	}
	require.NoError(t, yaml.Unmarshal(data, &rule))

	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	require.Equal(t, norm(rule.Expression), norm(ResourceRequestsExpression(true)))
}

func TestResourceRequestsExpression_WithoutInitContainers(t *testing.T) {
	require.NotContains(t, ResourceRequestsExpression(false), "initContainers")
}
