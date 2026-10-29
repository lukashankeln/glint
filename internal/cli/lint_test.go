package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/render"
	"github.com/lukashankeln/glint/internal/rules"
)

func TestRenderErrorViolation_AmbiguousMergeKey(t *testing.T) {
	app := discovery.DiscoveredApp{Name: "web", RootPath: "/repo/apps/web"}
	err := fmt.Errorf("splitting YAML in %q: %w", "/repo/apps/web/a.yaml",
		fmt.Errorf("resolving merge keys: %w", render.ErrAmbiguousMergeKey))

	v, ok := renderErrorViolation(renderResult{app: app, err: err})
	require.True(t, ok)
	assert.Equal(t, "glint/parse-error", v.RuleID)
	assert.Equal(t, rules.SeverityError, v.Severity)
	assert.Equal(t, "/repo/apps/web", v.FilePath)
	assert.Equal(t, "render", v.Source)
	assert.Contains(t, v.Message, "web")
	assert.True(t, failsThreshold([]rules.Violation{v}, []string{"error"}))
}

func TestRenderErrorViolation_OtherErrorsStaySkips(t *testing.T) {
	app := discovery.DiscoveredApp{Name: "web"}
	for _, err := range []error{nil, errors.New("helm template failed")} {
		_, ok := renderErrorViolation(renderResult{app: app, err: err})
		assert.False(t, ok, "err=%v", err)
	}
}
