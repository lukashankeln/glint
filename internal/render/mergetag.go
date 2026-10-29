package render

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// ErrAmbiguousMergeKey is wrapped by the error SplitYAML returns when a quoted
// or block "<<" mapping key may carry a "!" tag that yaml.v3 dropped and the
// source text cannot confirm it. Guessing either way could make glint lint
// something other than what Kubernetes applies, so the stream is rejected.
var ErrAmbiguousMergeKey = errors.New("ambiguous merge key")

// sourceCursor maps yaml.v3 node positions (1-based line and rune column,
// counted across the whole stream) back to the source text. It only moves
// forward, so using one cursor for every document of a stream costs
// O(stream + candidates).
type sourceCursor struct {
	raw   []byte
	src   []byte // UTF-8 text without BOM, set by prepare
	ready bool
	off   int
	line  int
	col   int
}

func newSourceCursor(data []byte) *sourceCursor {
	return &sourceCursor{raw: data}
}

// prepare decodes the stream to the UTF-8 text yaml.v3 counted positions in:
// UTF-16 (with BOM) is transcoded and a leading UTF-8 BOM is dropped. It runs
// lazily, on the first candidate.
func (c *sourceCursor) prepare() {
	if c.ready {
		return
	}
	c.ready = true
	c.line, c.col = 1, 1
	d := c.raw
	switch {
	case len(d) >= 2 && d[0] == 0xFF && d[1] == 0xFE:
		c.src = decodeUTF16(d[2:], false)
	case len(d) >= 2 && d[0] == 0xFE && d[1] == 0xFF:
		c.src = decodeUTF16(d[2:], true)
	case len(d) >= 3 && d[0] == 0xEF && d[1] == 0xBB && d[2] == 0xBF:
		c.src = d[3:]
	default:
		c.src = d
	}
}

func decodeUTF16(b []byte, bigEndian bool) []byte {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return []byte(string(utf16.Decode(u)))
}

// peek returns the rune at the cursor, or -1 at the end of the text.
func (c *sourceCursor) peek() rune {
	if c.off >= len(c.src) {
		return -1
	}
	r, _ := utf8.DecodeRune(c.src[c.off:])
	return r
}

// next consumes one rune, or one CRLF pair, keeping line and column current.
func (c *sourceCursor) next() {
	if c.off >= len(c.src) {
		return
	}
	r, n := utf8.DecodeRune(c.src[c.off:])
	c.off += n
	if r == '\r' && c.off < len(c.src) && c.src[c.off] == '\n' {
		c.off++
	}
	if isBreakRune(r) {
		c.line++
		c.col = 1
	} else {
		c.col++
	}
}

// advanceTo moves the cursor to the 1-based position (line, col).
func (c *sourceCursor) advanceTo(line, col int) error {
	c.prepare()
	if line < c.line || (line == c.line && col < c.col) {
		return fmt.Errorf("merge key at line %d, column %d is behind the source cursor (line %d, column %d): %w",
			line, col, c.line, c.col, ErrAmbiguousMergeKey)
	}
	for c.line < line {
		if c.peek() < 0 {
			return fmt.Errorf("merge key at line %d, column %d is past the end of the source: %w",
				line, col, ErrAmbiguousMergeKey)
		}
		c.next()
	}
	for c.col < col {
		if r := c.peek(); r < 0 || isBreakRune(r) {
			return fmt.Errorf("merge key at line %d, column %d is past the end of its line: %w",
				line, col, ErrAmbiguousMergeKey)
		}
		c.next()
	}
	return nil
}

// skipSeparators consumes blanks, line breaks and # comments.
func (c *sourceCursor) skipSeparators() {
	for {
		switch r := c.peek(); {
		case r == ' ' || r == '\t' || isBreakRune(r):
			c.next()
		case r == '#':
			for r := c.peek(); r >= 0 && !isBreakRune(r); r = c.peek() {
				c.next()
			}
		default:
			return
		}
	}
}

// consumeToken consumes runes up to a blank, a line break or, when flowStops
// is set, a flow indicator.
func (c *sourceCursor) consumeToken(flowStops bool) {
	for {
		r := c.peek()
		if r < 0 || r == ' ' || r == '\t' || isBreakRune(r) {
			return
		}
		if flowStops && (r == ',' || r == '[' || r == ']' || r == '{' || r == '}') {
			return
		}
		c.next()
	}
}

// isMergeTagCandidate reports whether n is a scalar key yaml.v3 may have
// stripped a bare "!" tag from: a quoted or block "<<" resolved as !!str whose
// style does not record an explicit tag. yaml.v3 sets TaggedStyle for every
// tag except an absent one and the bare "!".
func isMergeTagCandidate(n *yaml.Node) bool {
	const quoted = yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.LiteralStyle | yaml.FoldedStyle
	return n.Kind == yaml.ScalarNode && n.Value == "<<" && n.Tag == "!!str" &&
		n.Style&yaml.TaggedStyle == 0 && n.Style&quoted != 0
}

func styleIndicator(style yaml.Style) rune {
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		return '"'
	case style&yaml.SingleQuotedStyle != 0:
		return '\''
	case style&yaml.LiteralStyle != 0:
		return '|'
	default:
		return '>'
	}
}

// resolveMergeTag reads the properties written before the candidate key from
// the source and, when one of them is a tag, marks the key !!merge as go-yaml
// v2 (and so Kubernetes) does. Any disagreement between node and source is an
// error.
func resolveMergeTag(c *sourceCursor, key *yaml.Node) error {
	fail := func(why string) error {
		return fmt.Errorf("merge key at line %d, column %d: %s: %w",
			key.Line, key.Column, why, ErrAmbiguousMergeKey)
	}
	if err := c.advanceTo(key.Line, key.Column); err != nil {
		return err
	}
	ind := styleIndicator(key.Style)
	if r := c.peek(); r != '&' && r != '!' && r != ind {
		return fail("source does not match the parsed key")
	}
	var anchor, tag bool
	for {
		switch r := c.peek(); {
		case r == '&' && !anchor:
			anchor = true
			c.consumeToken(true)
		case r == '!' && !tag:
			tag = true
			c.consumeToken(false)
		default:
			if r != ind {
				return fail("unexpected text before the key scalar")
			}
			if tag {
				key.Tag = "!!merge"
			}
			return nil
		}
		c.skipSeparators()
	}
}

// mergeTagResolver is a variable only so a test can make SplitYAML's
// error path observable; tests that replace it must not run in parallel.
var mergeTagResolver = resolveMergeTags

// resolveMergeTags walks one document in document order, without following
// aliases, and resolves every candidate mapping key against the source. The
// cursor is shared by all documents of a stream.
func resolveMergeTags(c *sourceCursor, doc *yaml.Node) error {
	type item struct {
		n     *yaml.Node
		isKey bool
	}
	stack := []item{{n: doc}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := it.n
		if it.isKey && isMergeTagCandidate(n) {
			if err := resolveMergeTag(c, n); err != nil {
				return err
			}
		}
		if n.Kind == yaml.AliasNode {
			continue
		}
		for i := len(n.Content) - 1; i >= 0; i-- {
			stack = append(stack, item{n: n.Content[i], isKey: n.Kind == yaml.MappingNode && i%2 == 0})
		}
	}
	return nil
}
