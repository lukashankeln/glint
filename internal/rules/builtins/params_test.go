package builtins

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResourceRequestsExpression_WithoutInitContainers(t *testing.T) {
	expr := ResourceRequestsExpression(false)
	require.NotContains(t, expr, "initContainers")
	require.Contains(t, expr, ".containers.all(")
}

func TestResourceRequestsExpression_WithInitContainers(t *testing.T) {
	expr := ResourceRequestsExpression(true)
	require.Contains(t, expr, "initContainers")
	require.Contains(t, expr, ".containers.all(")
}
