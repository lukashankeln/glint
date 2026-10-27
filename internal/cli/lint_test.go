package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/rules"
)

func TestRenderErrorViolation(t *testing.T) {
	tests := []struct {
		name     string
		app      discovery.DiscoveredApp
		wantPath string
	}{
		{
			name: "file path from source",
			app: discovery.DiscoveredApp{
				Name: "web", RootPath: "/repo/charts/web",
				Source: &discovery.SourceRef{Path: "/repo/apps/web.yaml"},
			},
			wantPath: "/repo/apps/web.yaml",
		},
		{
			name:     "file path from root when no source",
			app:      discovery.DiscoveredApp{Name: "web", RootPath: "/repo/charts/web"},
			wantPath: "/repo/charts/web",
		},
		{
			name: "file path from root when source path empty",
			app: discovery.DiscoveredApp{
				Name: "web", RootPath: "/repo/charts/web", Source: &discovery.SourceRef{},
			},
			wantPath: "/repo/charts/web",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := renderErrorViolation(tt.app, errors.New("boom"))
			assert.Equal(t, "glint/render-error", v.RuleID)
			assert.Equal(t, rules.SeverityError, v.Severity)
			assert.Equal(t, "render", v.Source)
			assert.Equal(t, tt.wantPath, v.FilePath)
			assert.Contains(t, v.Message, `"web"`)
			assert.Contains(t, v.Message, "boom")
		})
	}
}

func TestRenderErrorViolation_ThresholdAndFilter(t *testing.T) {
	v := renderErrorViolation(discovery.DiscoveredApp{Name: "web", RootPath: "/x"}, errors.New("boom"))
	assert.True(t, failsThreshold([]rules.Violation{v}, []string{"error"}))

	kept := filterViolations([]rules.Violation{v}, "", "glint/render-error")
	assert.Empty(t, kept)

	kept = filterViolations([]rules.Violation{v}, "", "other-rule")
	assert.Len(t, kept, 1)
}
