package cli

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// needsEscape reports whether s contains invalid UTF-8 or any rune that
// strconv.IsPrint rejects (control, bidi and other format characters).
func needsEscape(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if !strconv.IsPrint(r) {
			return true
		}
	}
	return false
}

// escapeNonPrintable makes s safe for terminals and log consumers by
// rewriting invalid bytes and non-printable runes as escape sequences.
// Strings that need no escaping are returned unchanged. Otherwise
// backslashes are doubled as well, so an escaped string can be told apart
// from its source; a string that needed no escaping may still contain a
// literal backslash sequence such as `\x1b`.
func escapeNonPrintable(s string) string {
	if !needsEscape(s) {
		return s
	}
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteString(`\x`)
			b.WriteByte(hex[s[i]>>4])
			b.WriteByte(hex[s[i]&0xf])
		case r == '\\':
			b.WriteString(`\\`)
		case !strconv.IsPrint(r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
