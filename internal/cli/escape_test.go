package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeNonPrintable(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"esc", "a\x1b[2Jb", `a\x1b[2Jb`},
		{"cr", "a\rb", `a\rb`},
		{"tab", "a\tb", `a\tb`},
		{"c1 csi", "a\u009bb", `a\u009bb`},
		{"bidi override", "a\u202eb", `a\u202eb`},
		{"invalid utf8", "a\xffb", `a\xffb`},
		{"backslash with esc", "a\\b\x1b", `a\\b\x1b`},
		{"plain backslash unchanged", `a\b`, `a\b`},
		{"plain ascii", "hello world", "hello world"},
		{"printable non-ascii", "é", "é"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, escapeNonPrintable(tt.in))
		})
	}
}

func TestNeedsEscape(t *testing.T) {
	for _, s := range []string{"\x1b", "\r", "\n", "\u009b", "\u202e", "\xff", "a\x00"} {
		assert.True(t, needsEscape(s), "%q", s)
	}
	for _, s := range []string{"", "plain text", "é", `a\b`, "日本語"} {
		assert.False(t, needsEscape(s), "%q", s)
	}
}
