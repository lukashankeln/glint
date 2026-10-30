package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveColor(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		flag       bool
		noColorEnv bool
		want       bool
	}{
		{"auto buffer", "auto", false, false, false},
		{"empty buffer", "", false, false, false},
		{"never", "never", false, false, false},
		{"always", "always", false, false, true},
		{"always with NO_COLOR", "always", false, true, true},
		{"no-color flag beats always", "always", true, false, false},
		{"no-color flag with auto", "auto", true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.noColorEnv {
				t.Setenv("NO_COLOR", "1")
			}
			assert.Equal(t, tt.want, resolveColor(tt.mode, tt.flag, &bytes.Buffer{}))
		})
	}
}
