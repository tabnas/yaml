package tabnasyaml

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	jsonic "github.com/tabnas/jsonic/go"
	tabnas "github.com/tabnas/parser/go"
)

// VERSION is this module's version. It MUST equal ts/package.json
// "version": the release orchestrator rewrites both, and
// TestVersionMatchesPackageJSON fails the build if they drift.
const VERSION = "0.5.21"

// YamlOptions configures the YAML parser plugin.
// Currently empty — reserved for future extension.
type YamlOptions struct {
	// When true, Parse returns a struct {Meta, Content} instead of bare
	// content. Mirrors the TypeScript `meta` option.
	Meta bool
}

// DocMeta holds per-document metadata captured by the stream rule.
type DocMeta struct {
	Directives []string `json:"directives"`
	Explicit   bool     `json:"explicit"`
	Ended      bool     `json:"ended"`
}

// MetaResult is the return shape when YamlOptions.Meta is true.
// Meta is either a *DocMeta (single doc) or []*DocMeta (multi-doc).
// Content is either the doc value (single) or []any (multi-doc).
type MetaResult struct {
	Meta    any `json:"meta"`
	Content any `json:"content"`
}

// Hoisted regex constants — compiling these at package init avoids
// recompiling them inside per-token hot paths.
//
// Each `\s` and `\S` the canonical regexps carry is JAVASCRIPT's class,
// which is not RE2's: RE2's `\s` is the five ASCII blanks, where the
// canonical one is the WhiteSpace and LineTerminator productions. The
// difference decided parses (`%TAG<U+FEFF>!! ...` registers a handle
// canonically and did not here), so the class is written out once, as
// jsSpaceClass, and every pattern uses it. `\b` and `\w` are ASCII in
// both engines and need no help. The canonical `.` stops at a line
// terminator, which RE2's does not, so the explicit-key pattern spells
// that out too.
var (
	structTagPrefixRe  = regexp.MustCompile(`^!!(seq|map|omap|set|pairs|binary|ordered|python/[^` + jsSpaceClass + `]*)\b`)
	yamlTagDirectiveRe = regexp.MustCompile(`^%TAG[` + jsSpaceClass + `]+([^` + jsSpaceClass + `]+)[` + jsSpaceClass + `]+([^` + jsSpaceClass + `]+)`)
	// !!type tag prefix on an explicit (`? `) key (mirrors src/yaml.ts:1457).
	explicitKeyTagRe = regexp.MustCompile(`^!!(\w+)[` + jsSpaceClass + `]+([^\n\r\x{2028}\x{2029}]*)$`)
	// Block scalar indicator used as an explicit key (mirrors src/yaml.ts:1482).
	explicitKeyBlockScalarRe = regexp.MustCompile(`^([|>])([+-]?)([0-9]?)$`)
)

// flowScanState caches incremental flow-collection depth and quote state
// across lex calls so the plain-scalar / newline branches don't rescan
// the source from index 0 on every token (which would be O(n²) overall).
//
// Mirrors the _flowDepth / _flowScanPos / _inDoubleQuote / _inSingleQuote
// cache in src/yaml.ts.
type flowScanState struct {
	depth         int
	pos           int
	upTo          int
	cr            int8 // 0 not yet looked, 1 the source has a \r, 2 it has none
	inDoubleQuote bool
	inSingleQuote bool
}

// reset clears flow scan state at the start of a new parse.

// advanceCol moves the lex point over the first n BYTES of fwd.
//
// SI is a byte offset and CI is a COLUMN, and a column counts characters.
// These used to be the same statement twice — `pnt.SI += n; pnt.CI += n`
// — which charged a 2-byte `é` two columns and a 3-byte `€` three, so
// every diagnostic after a non-ASCII character reported a column past
// where the problem was.
//
// The TypeScript half writes the same expression over UTF-16 indices,
// where it DOES count characters. The two lines look identical, which is
// why this survived a port review: a transliteration is not a port when
// the two languages index strings differently.
//
// utf8.RuneCountInString is what the engine's own matchers use, so the
// two agree by construction. It also matches the engine on malformed
// UTF-8, counting each invalid byte as one rune — a local "is this a
// continuation byte" test does not, and gets a stray 0x80 wrong.
func advanceCol(pnt *tabnas.Point, fwd string, n int) {
	pnt.SI += n
	pnt.CI += utf8.RuneCountInString(fwd[:n])
}

func (s *flowScanState) reset() {
	s.depth = 0
	s.pos = 0
	s.upTo = 0
	s.cr = 0
	s.inDoubleQuote = false
	s.inSingleQuote = false
}

// stripCommentLines removes leading-#-comment lines from src; used by the
// matcher to detect a comments-only source.
func stripCommentLines(src string) string {
	out := src
	for {
		end := 0
		// Skip leading whitespace on this line.
		for end < len(out) && (out[end] == ' ' || out[end] == '\t') {
			end++
		}
		if end >= len(out) || out[end] != '#' {
			break
		}
		// Find end of line.
		for end < len(out) && out[end] != '\n' && out[end] != '\r' {
			end++
		}
		if end < len(out) && out[end] == '\r' {
			end++
		}
		if end < len(out) && out[end] == '\n' {
			end++
		}
		out = out[end:]
	}
	return out
}

// advance scans src forward from the cached position to target, updating
// flow-collection depth and quote state. If target is behind the last
// target the cache is reset (cleanSource may have replaced lex.Src and
// shortened the cursor).
//
// The scan reads the source the way the lexer does, or text is counted as
// syntax (tabnas/yaml#89; mirrors updateFlowState in src/yaml.ts). A quoted
// region is skipped, and so are the regions where a bracket or a quote is
// ordinary text: a comment, a block scalar's content lines, and, in block
// context, any `{` `[` `"` `'` that does not begin a node. The scan may run
// past target (to the end of a comment or a block scalar), since the lexer
// never stops inside either.
func (s *flowScanState) advance(src string, target int) {
	if target < s.upTo {
		s.depth = 0
		s.pos = 0
		s.cr = 0
		s.inDoubleQuote = false
		s.inSingleQuote = false
	}
	s.upTo = target
	fi := s.pos
	for ; fi < target; fi++ {
		fc := src[fi]
		// Fast path: only ten bytes matter in any state.
		if !flowScanBytes[fc] {
			continue
		}
		if s.inDoubleQuote {
			if fc == '\\' {
				fi++
			} else if fc == '"' {
				s.inDoubleQuote = false
			}
			continue
		}
		if s.inSingleQuote {
			if fc == '\'' {
				if fi+1 < len(src) && src[fi+1] == '\'' {
					fi++ // escaped ''
				} else {
					s.inSingleQuote = false
				}
			}
			continue
		}
		if fc == '#' && (fi == 0 || isYamlSpaceByte(src[fi-1])) {
			for fi+1 < len(src) && src[fi+1] != '\n' && src[fi+1] != '\r' {
				fi++
			}
			continue
		}
		if s.depth > 0 {
			switch fc {
			case '{', '[':
				s.depth++
			case '}', ']':
				s.depth--
			case '"':
				s.inDoubleQuote = true
			case '\'':
				// Apostrophes preceded by a word char are not quote openers.
				if fi > 0 {
					pc := src[fi-1]
					if (pc >= 'A' && pc <= 'Z') || (pc >= 'a' && pc <= 'z') || (pc >= '0' && pc <= '9') {
						continue
					}
				}
				s.inSingleQuote = true
			}
			continue
		}
		// Block context: only a node's first character opens anything.
		switch fc {
		case '{', '[', '"', '\'', '|', '>':
			if !startsYamlNode(src, fi) {
				// Inside a plain scalar, which nothing can open until it
				// ends at `: `, ` #` or the end of the line: skip to there.
				fi = plainScalarRest(src, fi) - 1
				continue
			}
			switch fc {
			case '{', '[':
				s.depth++
			case '"':
				s.inDoubleQuote = true
			case '\'':
				s.inSingleQuote = true
			default:
				// A lone \r ends a line too, but most sources have none:
				// look once per parse, not once per line.
				if s.cr == 0 {
					s.cr = 2
					if strings.IndexByte(src, '\r') >= 0 {
						s.cr = 1
					}
				}
				if end := blockScalarEnd(src, fi, s.cr == 1); end > fi {
					fi = end - 1
				}
			}
		}
	}
	s.pos = fi
}

// flowScanBytes marks the bytes advance acts on: quotes, the escape
// backslash, the comment mark, flow brackets, and block scalar indicators.
// Every other byte leaves its state unchanged. Mirrors FLOW_SCAN_CHARS in
// src/yaml.ts.
var flowScanBytes = func() (t [256]bool) {
	for _, c := range []byte("\"'\\#{}[]|>") {
		t[c] = true
	}
	return
}()

// bomText is U+FEFF, the byte order mark, in UTF-8.
const bomText = "\uFEFF"

// atLineStart reports whether offset i begins a line: the source start,
// just after a line break, or just after the byte order mark that opens
// the stream. Mirrors atLineStart in src/yaml.ts.
func atLineStart(src string, i int) bool {
	return i == 0 || src[i-1] == '\n' || src[i-1] == '\r' ||
		(i == len(bomText) && strings.HasPrefix(src, bomText))
}

func isYamlSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// startsYamlNode reports whether the byte at i (block context) is the first
// character of a node: first on its line, or after a `: ` / `- ` / `? `
// indicator, a `---` marker, or a node property (&anchor, !tag) that itself
// starts a node. Anything else is inside a plain scalar. Mirrors
// startsYamlNode in src/yaml.ts.
func startsYamlNode(src string, i int) bool {
	p := i - 1
	for p >= 0 && (src[p] == ' ' || src[p] == '\t') {
		p--
	}
	if p < 0 || src[p] == '\n' || src[p] == '\r' || (p == len(bomText)-1 && strings.HasPrefix(src, bomText)) {
		return true
	}
	if p == i-1 {
		return false
	}
	pc := src[p]
	if pc == ':' {
		return true
	}
	if pc == '-' || pc == '?' {
		if pc == '-' && p >= 2 && src[p-1] == '-' && src[p-2] == '-' &&
			(p-3 < 0 || src[p-3] == '\n' || src[p-3] == '\r') {
			return true
		}
		return startsYamlNode(src, p)
	}
	w := p
	for w > 0 && !isYamlSpaceByte(src[w-1]) {
		w--
	}
	if src[w] == '&' || src[w] == '!' {
		return startsYamlNode(src, w)
	}
	return false
}

// plainScalarRest returns, from i inside a block-context plain scalar, the
// offset where it can end: the next line break, a `:` followed by
// whitespace or the end, or a `#` after whitespace. Mirrors plainScalarRest
// in src/yaml.ts.
func plainScalarRest(src string, i int) int {
	j := i + 1
	for ; j < len(src); j++ {
		switch src[j] {
		case '\n', '\r':
			return j
		case ':':
			if j+1 >= len(src) || isYamlSpaceByte(src[j+1]) {
				return j
			}
		case '#':
			if src[j-1] == ' ' || src[j-1] == '\t' {
				return j
			}
		}
	}
	return j
}

// blockScalarEnd returns, for a block scalar indicator (| or >, optional
// chomping/indentation indicators, then only a comment) at i, the offset
// just past its content lines, or i when it is not one. Mirrors
// blockScalarEnd in src/yaml.ts.
func blockScalarEnd(src string, i int, hasCR bool) int {
	j := i + 1
	explicit := 0
	for k := 0; k < 2; k++ {
		c := byteAt(src, j)
		if c == '+' || c == '-' {
			j++
		} else if c >= '1' && c <= '9' {
			explicit = int(c - '0')
			j++
		}
	}
	for byteAt(src, j) == ' ' || byteAt(src, j) == '\t' {
		j++
	}
	if byteAt(src, j) == '#' {
		for j < len(src) && src[j] != '\n' && src[j] != '\r' {
			j++
		}
	}
	if j < len(src) && src[j] != '\n' && src[j] != '\r' {
		return i
	}
	ls := i
	for ls > 0 && src[ls-1] != '\n' && src[ls-1] != '\r' {
		ls--
	}
	if ls == 0 && strings.HasPrefix(src, bomText) {
		ls = len(bomText)
	}
	lineIndent := 0
	for byteAt(src, ls+lineIndent) == ' ' {
		lineIndent++
	}
	isRoot := ls+lineIndent == i || strings.HasPrefix(src[ls:], "---")
	parentIndent := lineIndent
	if isRoot && lineIndent == 0 {
		parentIndent = -1
	}
	pos := j
	if byteAt(src, pos) == '\r' {
		pos++
	}
	if byteAt(src, pos) == '\n' {
		pos++
	}
	blockIndent := -1
	if explicit > 0 {
		// As the block scalar handler does: after a key on the same line
		// (`- a: |2`), the indent the indicator counts from includes each
		// `- ` before the key, not only the line's leading spaces.
		base := parentIndent
		if base < 0 {
			base = 0
		}
		hasColon := false
		for ci := ls + lineIndent; ci < i; ci++ {
			if src[ci] == ':' && (byteAt(src, ci+1) == ' ' || byteAt(src, ci+1) == '\t') {
				hasColon = true
				break
			}
		}
		if hasColon {
			si := ls + lineIndent
			for si < i && src[si] == '-' && (byteAt(src, si+1) == ' ' || byteAt(src, si+1) == '\t') {
				base += 2
				si += 2
				for si < i && src[si] == ' ' {
					base++
					si++
				}
			}
		}
		blockIndent = base + explicit
	}
	end := pos
	for pos < len(src) {
		n := 0
		for pos+n < len(src) && src[pos+n] == ' ' {
			n++
		}
		c := byteAt(src, pos+n)
		// Content lines are most of a block scalar: find the line end with
		// IndexByte (assembly) rather than byte by byte, then look for a
		// lone \r before it.
		lineEnd := len(src)
		if k := strings.IndexByte(src[pos+n:], '\n'); k >= 0 {
			lineEnd = pos + n + k
		}
		if hasCR {
			if k := strings.IndexByte(src[pos+n:lineEnd], '\r'); k >= 0 {
				lineEnd = pos + n + k
			}
		}
		next := lineEnd
		if next < len(src) && src[next] == '\r' {
			next++
		}
		if next < len(src) && src[next] == '\n' {
			next++
		}
		if pos+n >= len(src) || c == '\n' || c == '\r' {
			pos = next
			continue
		}
		if blockIndent < 0 {
			if n <= parentIndent {
				break
			}
			blockIndent = n
		}
		if n < blockIndent {
			break
		}
		if n == 0 && (strings.HasPrefix(src[pos:], "---") || strings.HasPrefix(src[pos:], "...")) {
			break
		}
		end = lineEnd
		pos = next
	}
	return end
}

// byteAt is src[k], or 0 past the end.
func byteAt(src string, k int) byte {
	if k < len(src) {
		return src[k]
	}
	return 0
}

// quotedKeyAt reports whether a quoted scalar opens at i and is a block
// mapping key: its closing quote is followed, on the same line, by `:` and
// then whitespace or the end of the line. Mirrors quotedKeyAt in
// src/yaml.ts.
func quotedKeyAt(src string, i int) bool {
	q := src[i]
	j := i + 1
	for j < len(src) && src[j] != '\n' && src[j] != '\r' {
		if q == '"' && src[j] == '\\' {
			j += 2
			continue
		}
		if src[j] == q {
			if q == '\'' && j+1 < len(src) && src[j+1] == '\'' {
				j += 2
				continue
			}
			break
		}
		j++
	}
	if j >= len(src) || src[j] != q {
		return false
	}
	j++
	for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
		j++
	}
	return j < len(src) && src[j] == ':' && (j+1 >= len(src) || isYamlSpaceByte(src[j+1]))
}

// unquoteWholeScalar returns text with its quotes removed when the whole of
// it is one quoted scalar on one line; otherwise text unchanged. Double
// quotes take the common escapes; single quotes take a doubled quote.
// Mirrors unquoteWholeScalar in src/yaml.ts.
func unquoteWholeScalar(text string) string {
	if len(text) < 2 {
		return text
	}
	q := text[0]
	if (q != '"' && q != '\'') || text[len(text)-1] != q {
		return text
	}
	var b strings.Builder
	last := len(text) - 1
	for j := 1; j < last; j++ {
		c := text[j]
		switch {
		case q == '\'' && c == '\'':
			if text[j+1] != '\'' || j+1 == last {
				return text
			}
			b.WriteByte('\'')
			j++
		case q == '"' && c == '"':
			return text
		case q == '"' && c == '\\':
			j++
			if j >= last {
				return text
			}
			switch text[j] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
			case '"', '\\', '/', ' ':
				b.WriteByte(text[j])
			default:
				return text
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Parse parses a YAML string and returns the resulting Go value.
// The returned value can be:
//   - *tabnas.OrderedMap for mappings (insertion-ordered: keys are exposed
//     in source order; use its Get/Has/Len methods, or its exported Keys /
//     Vals fields, to read entries — Vals is the underlying map[string]any
//     when order does not matter)
//   - []any for sequences
//   - float64 for numbers
//   - string for strings
//   - bool for booleans
//   - nil for null or empty input
//
// defaultParser is a lazily-created instance reused by Parse, so repeated
// calls don't rebuild the engine and grammar each time (building the YAML
// grammar dominates a parse — see perf_test.go). Parsing builds a fresh
// context per call and only reads instance state, so the shared instance
// is safe for concurrent use. Mirrors @tabnas/json's Parse.
var (
	defaultOnce   sync.Once
	defaultParser *tabnas.Tabnas
)

func Parse(src string) (any, error) {
	defaultOnce.Do(func() { defaultParser = MakeJsonic() })
	return defaultParser.Parse(src)
}

// MakeJsonic creates a jsonic instance configured for YAML parsing.
// If a YamlOptions is passed, its fields are propagated to the plugin.
func MakeJsonic(opts ...YamlOptions) *tabnas.Tabnas {
	yo := YamlOptions{}
	if len(opts) > 0 {
		yo = opts[0]
	}
	j := jsonic.Make(tabnas.Options{
		String: &tabnas.StringOptions{
			Chars: "`", // Remove single quote from string chars; we handle YAML strings in yamlMatcher
		},
		Lex: &tabnas.LexOptions{
			EmptyResult: nil,
		},
	})

	pluginOpts := map[string]any{"meta": yo.Meta}
	j.Use(Yaml, pluginOpts)
	return j
}

// yamlValueMap maps YAML value keywords to their Go values.
var yamlValueMap = map[string]any{
	"true": true, "True": true, "TRUE": true,
	"false": false, "False": false, "FALSE": false,
	"null": nil, "Null": nil, "NULL": nil,
	"~":   nil,
	"yes": true, "Yes": true, "YES": true,
	"no": false, "No": false, "NO": false,
	"on": true, "On": true, "ON": true,
	"off": false, "Off": false, "OFF": false,
	".inf": math.Inf(1), ".Inf": math.Inf(1), ".INF": math.Inf(1),
	"+.inf": math.Inf(1), "+.Inf": math.Inf(1), "+.INF": math.Inf(1),
	"-.inf": math.Inf(-1), "-.Inf": math.Inf(-1), "-.INF": math.Inf(-1),
	".nan": math.NaN(), ".NaN": math.NaN(), ".NAN": math.NaN(),
}

// isYamlValue checks if text is a YAML value keyword and returns the value.
func isYamlValue(text string) (any, bool) {
	val, ok := yamlValueMap[text]
	return val, ok
}

// parseYamlNumber is the canonical `+text`, which is `Number(text)`: the
// whole trimmed text has to be a StrNumericLiteral. That is narrower than
// strconv in some places (`inf`, `nan` and `1_000` are not numbers to
// JavaScript) and wider in one (leading and trailing JavaScript
// whitespace is skipped, U+FEFF included). Returns the number and true,
// or 0 and false where `Number` gives NaN.
func parseYamlNumber(text string) (float64, bool) {
	t := jsTrim(text)
	if t == "" {
		return 0, true
	}
	if len(t) > 2 && t[0] == '0' {
		radix := 0
		switch t[1] {
		case 'x', 'X':
			radix = 16
		case 'o', 'O':
			radix = 8
		case 'b', 'B':
			radix = 2
		}
		if radix != 0 {
			n, err := strconv.ParseUint(t[2:], radix, 64)
			if err != nil || strings.ContainsAny(t[2:], "_+-") {
				return 0, false
			}
			return float64(n), true
		}
	}
	sign := 1.0
	body := t
	if body[0] == '-' {
		sign = -1
		body = body[1:]
	} else if body[0] == '+' {
		body = body[1:]
	}
	if body == "Infinity" {
		return math.Inf(int(sign)), true
	}
	if !isJSDecimalLiteral(body) {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(body, "."), 64)
	if err != nil {
		// Only an overflow can fail here, and JavaScript answers Infinity.
		if strings.Contains(err.Error(), "range") {
			return sign * math.Inf(1), true
		}
		return 0, false
	}
	return sign * n, true
}

// isJSDecimalLiteral is ECMAScript's StrUnsignedDecimalLiteral: digits
// with an optional fraction and exponent, or a fraction alone. No
// separators, no `inf`, no `nan`, and an exponent needs a digit.
func isJSDecimalLiteral(body string) bool {
	i := 0
	integral := 0
	for i < len(body) && body[i] >= '0' && body[i] <= '9' {
		i++
		integral++
	}
	fractional := 0
	if i < len(body) && body[i] == '.' {
		i++
		for i < len(body) && body[i] >= '0' && body[i] <= '9' {
			i++
			fractional++
		}
	}
	if integral == 0 && fractional == 0 {
		return false
	}
	if i < len(body) && (body[i] == 'e' || body[i] == 'E') {
		i++
		if i < len(body) && (body[i] == '+' || body[i] == '-') {
			i++
		}
		exponent := 0
		for i < len(body) && body[i] >= '0' && body[i] <= '9' {
			i++
			exponent++
		}
		if exponent == 0 {
			return false
		}
	}
	return i == len(body)
}

// deepCopy performs a structural deep copy of a value, preserving both the
// concrete container type and, for insertion-ordered *tabnas.OrderedMap
// objects, their source key order. Anchors deep-copy their value so a later
// merge or mutation of one alias cannot leak into another. (A JSON
// round-trip would flatten *OrderedMap back to an alphabetical
// map[string]any, silently dropping source order — hence the manual walk.)
func deepCopy(v any) any {
	switch val := v.(type) {
	case *tabnas.OrderedMap:
		out := tabnas.NewOrderedMap()
		out.Sorted = val.Sorted
		for _, k := range val.Keys {
			out.Set(k, deepCopy(val.Vals[k]))
		}
		return out
	case tabnas.OrderedMap:
		return deepCopy(&val)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = deepCopy(item)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = deepCopy(item)
		}
		return out
	default:
		return v
	}
}

// asStringMap views a parsed object value as a plain map[string]any.
//
// The shared jsonic engine now returns parsed JSON objects as an
// insertion-ordered *tabnas.OrderedMap instead of a bare map[string]any.
// Grammar text is parsed with the stock jsonic parser, so the values this
// module reads back from that parse (the embedded grammar spec) are
// *OrderedMap. This helper unwraps either shape to the underlying map for
// value-only access; callers that reconstruct typed specs from it do not
// depend on key order.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case *tabnas.OrderedMap:
		return m.Vals, true
	case tabnas.OrderedMap:
		return m.Vals, true
	case map[string]any:
		return m, true
	}
	return nil, false
}

// orderedEntries returns the ordered (key, value) entries of a parsed
// object value. Every mapping this plugin builds, block or flow, is a
// *tabnas.OrderedMap, so its keys come in source order. A plain
// map[string]any is handled defensively; it has no order, so its keys come
// in Go's map order.
func orderedEntries(v any) ([]string, map[string]any) {
	switch m := v.(type) {
	case *tabnas.OrderedMap:
		return m.Keys, m.Vals
	case tabnas.OrderedMap:
		return m.Keys, m.Vals
	case map[string]any:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return keys, m
	}
	return nil, nil
}

// setNodeKey assigns key→val on a parsed mapping node, whether it is an
// insertion-ordered *OrderedMap (source-order block/flow map) or a plain
// map[string]any (defensive fallback).
func setNodeKey(node any, key string, val any) {
	switch m := node.(type) {
	case *tabnas.OrderedMap:
		m.Set(key, val)
	case map[string]any:
		m[key] = val
	}
}

// applyMergeKeys resolves the YAML `<<` merge key on a parsed mapping node
// in place, the way the canonical TypeScript does: it deletes the `<<`
// entry, then appends each key a merge source holds that the mapping does
// not, source by source and in each source's own order. So the mapping's
// own keys come first, in source order, wherever the `<<` stood among
// them, and the merged keys follow:
//
//	d:
//	  <<: *b      # b is {x: 1}
//	  y: 2        # d is {y: 2, x: 1}, not {x: 1, y: 2}
//
// An explicit key always wins over a merged one, and an earlier source
// over a later one. The node is an insertion-ordered *OrderedMap, block or
// flow, or, defensively, a plain map[string]any, which has no order to
// keep.
func applyMergeKeys(node any) {
	om, ok := node.(*tabnas.OrderedMap)
	if !ok {
		// Plain map: no order to keep; merge by value.
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		mergeVal, hasMerge := m["<<"]
		if !hasMerge {
			return
		}
		delete(m, "<<")
		for _, mm := range mergeSources(mergeVal) {
			mkeys, mvals := orderedEntries(mm)
			for _, k := range mkeys {
				if _, exists := m[k]; !exists {
					m[k] = mvals[k]
				}
			}
		}
		return
	}
	mergeVal, hasMerge := om.Get("<<")
	if !hasMerge {
		return
	}
	// Delete, then append: Set adds a key it has not seen at the END, and
	// the explicit keys are already in place, so they keep their order and
	// lead. This port once spliced the merged keys in where the `<<` stood
	// instead, which gave the same members in a different order.
	om.Delete("<<")
	for _, mm := range mergeSources(mergeVal) {
		mkeys, mvals := orderedEntries(mm)
		for _, mk := range mkeys {
			if !om.Has(mk) {
				om.Set(mk, mvals[mk])
			}
		}
	}
}

// mergeSources normalizes a `<<` merge value into the list of source
// mappings to merge, in order. A single mapping merges itself; a sequence
// merges each element (earlier entries take precedence in YAML merge
// semantics, matching first-wins insertion below).
func mergeSources(mergeVal any) []any {
	switch mv := mergeVal.(type) {
	case []any:
		return mv
	default:
		return []any{mergeVal}
	}
}

// extractKey extracts a key value from a token, resolving aliases.
func extractKey(o0 *tabnas.Token, anchors map[string]any) any {
	if o0.Tin == tabnas.TinVL {
		if m, ok := o0.Val.(map[string]any); ok {
			if alias, ok := m["__yamlAlias"].(string); ok {
				if val, exists := anchors[alias]; exists {
					return val
				}
				return "*" + alias
			}
		}
	}
	if o0.Tin == tabnas.TinST || o0.Tin == tabnas.TinTX {
		if s, ok := o0.Val.(string); ok {
			return s
		}
	}
	return o0.Src
}

// anchorInfo holds anchor metadata during parsing.
type anchorInfo struct {
	name   string
	inline bool
}

// isDocMarker checks if the string at position i starts with --- or ...
// followed by a space, tab, newline, or end of string.
func isDocMarker(s string, i int) bool {
	if i+3 > len(s) {
		return false
	}
	marker := s[i : i+3]
	if marker != "---" && marker != "..." {
		return false
	}
	if i+3 >= len(s) {
		return true
	}
	next := s[i+3]
	return next == '\n' || next == '\r' || next == ' ' || next == '\t'
}

// isDocMarkerNoTab is the document-marker test the canonical writes for
// the line that ENDS A BLOCK SCALAR (src/yaml.ts:576): `---` or `...` at
// indent 0 followed by a newline, a space or the end of the source. It
// is the one of the four marker tests that leaves the tab out, so a
// `---<TAB>` line stays inside the scalar here and ends the document
// everywhere else, which is what isDocMarker says.
func isDocMarkerNoTab(s string, i int) bool {
	if !isDocMarkerRun(s, i) {
		return false
	}
	if i+3 >= len(s) {
		return true
	}
	next := s[i+3]
	return next == '\n' || next == '\r' || next == ' '
}

// isDocMarkerRun is three of `-` or three of `.` at i, with nothing said
// about what follows: the shape the canonical tests when it decides
// whether a block scalar's final newline is followed by a document
// marker (src/yaml.ts:674).
func isDocMarkerRun(s string, i int) bool {
	if i+3 > len(s) {
		return false
	}
	marker := s[i : i+3]
	return marker == "---" || marker == "..."
}

// isJSSpace reports whether r is whitespace to JAVASCRIPT: the WhiteSpace
// and LineTerminator productions, which is the set `\s`,
// `String.prototype.trim`, `Number`, `parseInt` and `parseFloat` all skip
// in the canonical plugin. It is not unicode.IsSpace, and the two differ
// in both directions: U+0085 is a space to Go and not to JavaScript, and
// U+FEFF is a space to JavaScript and not to Go. Both decide scalars.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return 0x2000 <= r && r <= 0x200A
}

// jsSpaceClass is the same set as the body of a regexp character class,
// for the patterns that stand in for a canonical `\s` or `\S`.
const jsSpaceClass = `\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`

// trimRight is the canonical `.replace(/\s+$/, "")`, the trailing-space
// strip every scalar handler ends with: it strips JavaScript's whitespace,
// not Go's and not only the two ASCII blanks.
func trimRight(s string) string {
	return strings.TrimRightFunc(s, isJSSpace)
}

// jsTrim is `String.prototype.trim`, over the same set.
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// utf16Window is `text.substring(from, from+count)` where `count` is a
// number of UTF-16 CODE UNITS, which is what the canonical fixed-width
// escape window is counted in, and `end` is where the scan resumes: the
// byte offset `count` units past `from`.
//
// Counting bytes or runes reads the same window only while every
// character in it is one code unit. An astral character is TWO units
// and one rune, so a window holding one takes too much text and leaves
// the cursor too far along; a window whose LAST unit falls inside one
// cuts the character in half. The canonical keeps the HIGH surrogate in
// the window, where it is no hexadecimal digit and so ends `parseInt`'s
// prefix exactly where stopping short of the character does, and leaves
// the LOW surrogate for the next round of the scan, which appends it as
// a character of its own. `splitLow` hands that unit back, because a
// byte offset cannot point between two surrogates; it is -1 when the
// window ends on a character boundary.
func utf16Window(text string, from, count int) (digits string, end int, splitLow int64) {
	if from > len(text) {
		from = len(text)
	}
	units := 0
	offset := 0
	for from+offset < len(text) {
		r, size := utf8.DecodeRuneInString(text[from+offset:])
		width := 1
		if r >= 0x10000 {
			width = 2
		}
		if units+width > count {
			if width == 2 && units < count {
				return text[from : from+offset], from + offset, int64(0xDC00 + ((r - 0x10000) & 0x3FF))
			}
			return text[from : from+offset], from + offset, -1
		}
		units += width
		offset += size
		if units == count {
			break
		}
	}
	return text[from : from+offset], from + offset, -1
}

// jsParseInt16 is `parseInt(s, 16)`: skip JavaScript's whitespace, take
// an optional sign and an optional `0x`, then the longest run of
// hexadecimal digits, and answer NaN when there is no digit at all.
func jsParseInt16(s string) float64 {
	t := strings.TrimLeftFunc(s, isJSSpace)
	i := 0
	negative := false
	if i < len(t) && (t[i] == '+' || t[i] == '-') {
		negative = t[i] == '-'
		i++
	}
	if i+1 < len(t) && t[i] == '0' && (t[i+1] == 'x' || t[i+1] == 'X') {
		i += 2
	}
	start := i
	value := 0.0
	for i < len(t) {
		d := hexDigitValue(t[i])
		if d < 0 {
			break
		}
		value = value*16 + float64(d)
		i++
	}
	if i == start {
		return math.NaN()
	}
	if negative {
		return -value
	}
	return value
}

func hexDigitValue(b byte) int {
	switch {
	case '0' <= b && b <= '9':
		return int(b - '0')
	case 'a' <= b && b <= 'f':
		return int(b-'a') + 10
	case 'A' <= b && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// jsToUint16 is the `ToUint16` that `String.fromCharCode` applies to its
// argument: NaN and the infinities become zero, and anything else is
// truncated and taken modulo 65536. It never fails, so an unreadable
// window is a NUL and not a refusal.
func jsToUint16(n float64) int64 {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	t := math.Trunc(n)
	m := math.Mod(t, 65536)
	if m < 0 {
		m += 65536
	}
	return int64(m)
}

// jsFromCodePoint is the range check `String.fromCodePoint` makes: it
// THROWS a RangeError for anything that is not a code point, and the
// canonical matcher does not catch it, so the token is never produced
// and the document is refused. `ok` false stands for the throw.
func jsFromCodePoint(n float64) (code int64, ok bool) {
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
		return 0, false
	}
	if n < 0 || n > 0x10FFFF {
		return 0, false
	}
	return int64(n), true
}

// jsSubstringLessOneUnit is `text.substring(from, to - 1)` where `to` is
// a byte offset standing in for a UTF-16 code-unit index, so the step
// back is ONE CODE UNIT. `substring` clamps after the subtraction and
// swaps a reversed pair, and both decide values: a typed tag's quoted
// value scan that overran the end by one leaves `to - 1` at the end,
// and an empty value leaves the pair the wrong way round, where the
// canonical yields the opening quote.
//
// One unit back from just past an astral character lands BETWEEN its two
// surrogates, and the canonical keeps the high one. A Go string has no
// place for an unpaired surrogate, so this folds it to the replacement
// character, as `pushCodeUnit` folds an escape naming one. A character
// inside the Basic Multilingual Plane is one unit and one rune, so
// stepping back drops the whole of it in both runtimes. Slicing at
// `to-1` directly did neither: it cut a multibyte character and left
// invalid UTF-8 in the value, and it panicked on the reversed pair.
func jsSubstringLessOneUnit(text string, from, to int) string {
	unit := to - 1
	if unit < 0 {
		unit = 0
	}
	end := runeFloor(text, unit)
	splitAstral := false
	if unit < len(text) && end < unit {
		_, size := utf8.DecodeRuneInString(text[end:])
		splitAstral = size == 4
	}
	from = runeFloor(text, from)
	lo, hi := from, end
	if lo > hi {
		lo, hi = hi, lo
	}
	out := text[lo:hi]
	if splitAstral && from <= end {
		out += "\uFFFD"
	}
	return out
}

// runeFloor is `i` clamped into `text` and moved back to the start of
// the character it falls inside.
func runeFloor(text string, i int) int {
	if i >= len(text) {
		return len(text)
	}
	for i > 0 && !utf8.RuneStart(text[i]) {
		i--
	}
	return i
}

// jsParseInt is `parseInt(s, 10)`: skip JavaScript's whitespace, take an
// optional sign and the longest run of decimal digits, and answer NaN
// when there is no digit. The tag has still been applied when it
// answers NaN, which is why `!!int xyz` is a number and not the text.
func jsParseInt(s string) float64 {
	t := strings.TrimLeftFunc(s, isJSSpace)
	i := 0
	if i < len(t) && (t[i] == '+' || t[i] == '-') {
		i++
	}
	start := i
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == start {
		return math.NaN()
	}
	n, err := strconv.ParseFloat(t[:i], 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

// jsParseFloat is `parseFloat(s)`: the same skip, then the longest
// prefix that is a StrDecimalLiteral, `Infinity` included, and NaN when
// nothing at all reads as one.
func jsParseFloat(s string) float64 {
	t := strings.TrimLeftFunc(s, isJSSpace)
	i := 0
	negative := false
	if i < len(t) && (t[i] == '+' || t[i] == '-') {
		negative = t[i] == '-'
		i++
	}
	if strings.HasPrefix(t[i:], "Infinity") {
		if negative {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	digits := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
		digits++
	}
	if i < len(t) && t[i] == '.' {
		i++
		for i < len(t) && t[i] >= '0' && t[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return math.NaN()
	}
	mantissaEnd := i
	if i < len(t) && (t[i] == 'e' || t[i] == 'E') {
		probe := i + 1
		if probe < len(t) && (t[probe] == '+' || t[probe] == '-') {
			probe++
		}
		exponentStart := probe
		for probe < len(t) && t[probe] >= '0' && t[probe] <= '9' {
			probe++
		}
		if probe > exponentStart {
			i = probe
		}
	}
	if i < mantissaEnd {
		i = mantissaEnd
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(t[:i], "."), 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

// childNode is the node of a rule's child, read from the END of the
// child's rotation chain.
//
// A rule that replaces itself (`r: list`, `r: elem`) leaves the original
// rule in `r.Child` with the new one hanging off its `Next`. A JavaScript
// array aliases by reference, so the canonical port reads `rule.child.node`
// and sees every element the rotated rules appended. A Go slice is a
// header, and the engine's append writes the grown slice back through
// `r.Parent.Node`, which is the ROTATED rule's parent, never the original
// child: the original keeps the one-element header the implicit-list
// promotion gave it, and a reader of `r.Child.Node` sees `["a"]` for
// `"a" "b"`. Every closure in this plugin that reads a child's node goes
// through here so it sees the whole value.
func childNode(r *tabnas.Rule) any {
	child := r.Child
	if child == nil || child == tabnas.NoRule {
		return tabnas.Undefined
	}
	if final := chainEnd(child); final != child && !tabnas.IsUndefined(final.Node) {
		return final.Node
	}
	return child.Node
}

// chainEnd is the last rule a rotation chain replaced `rule` with, or
// `rule` itself when it never rotated.
func chainEnd(rule *tabnas.Rule) *tabnas.Rule {
	final := rule
	for final.Next != nil && final.Next != tabnas.NoRule && final.Next.Prev == final {
		final = final.Next
	}
	return final
}

// formatKey converts a value to a string suitable for use as a map key.
func formatKey(v any) string {
	switch k := v.(type) {
	case string:
		return k
	case float64:
		if k == float64(int64(k)) {
			return fmt.Sprintf("%d", int64(k))
		}
		return fmt.Sprintf("%g", k)
	case bool:
		if k {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Yaml is a jsonic plugin that adds YAML parsing support.
func Yaml(j *tabnas.Tabnas, opts map[string]any) error {
	// Install once per instance: a second Use(Yaml) on the same instance,
	// or the plugin listed twice (which Derive then re-runs twice), returns
	// here. Without it the grammar alternates and the val/map/stream state
	// handlers would be registered again over a second set of per-parse
	// state. SetOptions does not re-run plugins, and Derive re-runs them on
	// the child before it copies decorations, so a derived instance
	// installs its own.
	if j.Decoration("yaml-installed") == true {
		return nil
	}
	j.Decorate("yaml-installed", true)

	wantMeta := false
	if opts != nil {
		if v, ok := opts["meta"].(bool); ok {
			wantMeta = v
		}
	}

	TX := j.Token("#TX")
	NR := j.Token("#NR")
	ST := j.Token("#ST")
	VL := j.Token("#VL")
	CL := j.Token("#CL")
	ZZ := j.Token("#ZZ")
	CA := j.Token("#CA")
	CS := j.Token("#CS")
	CB := j.Token("#CB")

	// Register custom tokens.
	IN := j.Token("#IN") // Indent token
	EL := j.Token("#EL") // Element marker (- )
	QM := j.Token("#QM") // YAML `?` explicit-key marker in flow context
	DS := j.Token("#DS") // YAML document start marker (---)
	DE := j.Token("#DE") // YAML document end marker (...)
	DR := j.Token("#DR") // YAML directive line (%YAML / %TAG)
	_ = QM
	_ = DS
	_ = DE
	_ = DR

	KEY := []tabnas.Tin{TX, NR, ST, VL}

	// Shared state for the plugin instance.
	anchors := make(map[string]any)
	var pendingAnchors []anchorInfo
	pendingExplicitCL := false
	var pendingTokens []*tabnas.Token
	tagHandles := make(map[string]string)
	// Flag to tell the number matcher to skip, so TextCheck handles the value.
	skipNumberMatch := false
	// Incremental flow-context cache shared between yamlMatcher and handlePlainScalar.
	flowState := &flowScanState{}
	// Stream-rule per-parse accumulators.
	var streamDocs []any
	var streamMeta []*DocMeta
	var streamCurMeta *DocMeta

	cfg := j.Config()

	// ===== TextCheck: handles block scalars, !!tags, and plain scalars =====
	textCheck := func(lex *tabnas.Lex) *tabnas.LexCheckResult {
		pnt := lex.Cursor()
		src := lex.Src
		fwd := src[pnt.SI:]
		if len(fwd) == 0 {
			return nil
		}
		ch := fwd[0]

		// Block scalar: | or >
		if ch == '|' || ch == '>' {
			if r := handleBlockScalar(lex, pnt, src, fwd, ch); r != nil {
				return r
			}
			// handleBlockScalar returns nil when the indicator is followed by
			// text on the same line (`a: > x`), which YAML forbids. TS does
			// not return there — the block-scalar branch is inline and simply
			// falls through to plain-scalar handling, yielding {"a":"> x"}.
			// Go must fall through too, or it rejects what TS accepts
			// (yaml-test-suite S4GJ).
			return handlePlainScalar(lex, pnt, src, fwd, flowState)
		}

		// !!type tags in text check context
		if ch == '!' && len(fwd) > 1 && fwd[1] == '!' {
			return handleTagInTextCheck(lex, pnt, fwd, tagHandles)
		}

		// Skip special chars that should be handled by other matchers.
		if ch == '{' || ch == '}' || ch == '[' || ch == ']' ||
			ch == ',' || ch == '#' || ch == '\n' || ch == '\r' ||
			ch == '"' || ch == '\'' || ch == '*' || ch == '&' || ch == '!' {
			return nil
		}

		// Colon followed by space/tab/newline/eof is a separator, not text.
		if ch == ':' && (len(fwd) < 2 || fwd[1] == ' ' || fwd[1] == '\t' || fwd[1] == '\n' || fwd[1] == '\r') {
			return nil
		}

		// Plain scalar — scan to end of line, handling multiline continuation.
		return handlePlainScalar(lex, pnt, src, fwd, flowState)
	}

	// ===== Custom YAML matcher (priority 500000 — before fixed tokens) =====
	var lastSeenSrc string
	srcSeen := false

	yamlMatcher := func(lex *tabnas.Lex, _ *tabnas.Rule) *tabnas.Token {
		pnt := lex.Cursor()
		src := lex.Src

		// First call (or new source on a reused plugin instance): reset
		// per-parse state. Mirrors the TS first-call setup. The source is
		// no longer mutated — directives, --- / ... markers, and explicit
		// keys flow through as #DR / #DS / #DE / #QM tokens.
		if !srcSeen || src != lastSeenSrc {
			srcSeen = true
			lastSeenSrc = src
			for k := range anchors {
				delete(anchors, k)
			}
			pendingAnchors = pendingAnchors[:0]
			pendingExplicitCL = false
			skipNumberMatch = false
			pendingTokens = pendingTokens[:0]
			for k := range tagHandles {
				delete(tagHandles, k)
			}
			flowState.reset()
			streamDocs = nil
			streamMeta = nil
			streamCurMeta = nil

			// Empty / whitespace-only / comments-only source: emit one #VL
			// null so the parser yields nil rather than a parse error.
			stripped := stripCommentLines(src)
			// `src.trim() === '' || stripped === ''`, and `trim` is
			// JavaScript's: a source of one byte-order mark is empty here,
			// and one of a next-line character is a scalar.
			if jsTrim(src) == "" || jsTrim(stripped) == "" {
				pnt.Len = 0
				tkn := lex.Token("#VL", VL, nil, "")
				pnt.SI = 0
				return tkn
			}
		}
		// A byte order mark opening the stream is not content: step over
		// it without counting a column. atLineStart and the flow scan
		// treat the offset after it as a line start. This sits outside the
		// reset, which a second parse of the same source skips.
		if pnt.SI == 0 && strings.HasPrefix(src, bomText) {
			pnt.SI = len(bomText)
		}

		if pnt.SI >= pnt.Len {
			return nil
		}

		// Emit pending tokens (from explicit key handling).
		if len(pendingTokens) > 0 {
			tkn := pendingTokens[0]
			pendingTokens = pendingTokens[1:]
			return tkn
		}

		// Emit pending explicit CL token.
		if pendingExplicitCL {
			pendingExplicitCL = false
			tkn := lex.Token("#CL", CL, 1, ": ")
			return tkn
		}

		fwd := lex.Src[pnt.SI:]
		if len(fwd) == 0 {
			return nil
		}

		// Process YAML features in a loop to handle chaining.
		for {
			if pnt.SI >= pnt.Len {
				return nil
			}
			fwd = lex.Src[pnt.SI:]
			if len(fwd) == 0 {
				return nil
			}

			// Alias: *name
			if fwd[0] == '*' {
				nameEnd := 1
				for nameEnd < len(fwd) && fwd[nameEnd] != ' ' && fwd[nameEnd] != '\t' &&
					fwd[nameEnd] != '\n' && fwd[nameEnd] != '\r' && fwd[nameEnd] != ',' &&
					fwd[nameEnd] != '{' && fwd[nameEnd] != '}' && fwd[nameEnd] != '[' &&
					fwd[nameEnd] != ']' {
					nameEnd++
				}
				aliasName := fwd[1:nameEnd]
				if val, ok := anchors[aliasName]; ok {
					var tkn *tabnas.Token
					switch v := val.(type) {
					case string:
						tkn = lex.Token("#TX", TX, v, fwd[:nameEnd])
					case float64:
						tkn = lex.Token("#NR", NR, v, fwd[:nameEnd])
					case bool:
						tkn = lex.Token("#VL", VL, v, fwd[:nameEnd])
					case nil:
						tkn = lex.Token("#VL", VL, nil, fwd[:nameEnd])
					default:
						// Complex value — use alias marker for later resolution.
						tkn = lex.Token("#VL", VL, map[string]any{"__yamlAlias": aliasName}, fwd[:nameEnd])
					}
					advanceCol(pnt, fwd, nameEnd)
					return tkn
				}
				// Unknown alias — return as marker.
				tkn := lex.Token("#VL", VL, map[string]any{"__yamlAlias": aliasName}, fwd[:nameEnd])
				advanceCol(pnt, fwd, nameEnd)
				return tkn
			}

			// Anchor: &name
			if fwd[0] == '&' {
				nameEnd := 1
				for nameEnd < len(fwd) && fwd[nameEnd] != ' ' && fwd[nameEnd] != '\t' &&
					fwd[nameEnd] != '\n' && fwd[nameEnd] != '\r' && fwd[nameEnd] != ',' &&
					fwd[nameEnd] != '{' && fwd[nameEnd] != '}' && fwd[nameEnd] != '[' &&
					fwd[nameEnd] != ']' {
					nameEnd++
				}
				anchorName := fwd[1:nameEnd]
				anchorInline := true

				// Check if anchor is standalone (nothing meaningful after it on the line).
				afterAnchor := nameEnd
				for afterAnchor < len(fwd) && (fwd[afterAnchor] == ' ' || fwd[afterAnchor] == '\t') {
					afterAnchor++
				}
				isStandalone := afterAnchor >= len(fwd) || fwd[afterAnchor] == '\n' ||
					fwd[afterAnchor] == '\r' || fwd[afterAnchor] == '#'

				if isStandalone {
					anchorInline = false
				}

				// Try to capture inline scalar value for the anchor.
				if anchorInline && afterAnchor < len(fwd) {
					peek := fwd[afterAnchor:]
					var scalarVal any
					pch := byte(0)
					if len(peek) > 0 {
						pch = peek[0]
					}
					if pch == '"' {
						ei := 1
						for ei < len(peek) && peek[ei] != '"' {
							if peek[ei] == '\\' {
								ei++
							}
							ei++
						}
						raw := peek[1:ei]
						raw = strings.ReplaceAll(raw, "\\n", "\n")
						raw = strings.ReplaceAll(raw, "\\t", "\t")
						raw = strings.ReplaceAll(raw, "\\\\", "\\")
						raw = strings.ReplaceAll(raw, `\"`, `"`)
						scalarVal = raw
					} else if pch == '\'' {
						ei := 1
						for ei < len(peek) && peek[ei] != '\'' {
							if ei+1 < len(peek) && peek[ei] == '\'' && peek[ei+1] == '\'' {
								ei++
							}
							ei++
						}
						raw := peek[1:ei]
						raw = strings.ReplaceAll(raw, "''", "'")
						scalarVal = raw
					} else if pch != 0 && pch != '{' && pch != '[' && pch != '\n' && pch != '\r' {
						ei := 0
						for ei < len(peek) && peek[ei] != '\n' && peek[ei] != '\r' &&
							peek[ei] != ',' && peek[ei] != '}' && peek[ei] != ']' {
							if peek[ei] == ':' && (ei+1 >= len(peek) || peek[ei+1] == ' ' ||
								peek[ei+1] == '\t' || peek[ei+1] == '\n' || peek[ei+1] == '\r') {
								break
							}
							if peek[ei] == ' ' && ei+1 < len(peek) && peek[ei+1] == '#' {
								break
							}
							ei++
						}
						// `peek.substring(0, ei).trim()`: both ends, and
						// JavaScript's set, so a byte-order mark before the
						// scalar is not part of what the alias resolves to.
						raw := jsTrim(peek[:ei])
						if len(raw) > 0 {
							scalarVal = raw
						}
					}
					if scalarVal != nil {
						anchors[anchorName] = scalarVal
					}
				}

				pendingAnchors = append(pendingAnchors, anchorInfo{name: anchorName, inline: anchorInline})

				// Check whether the anchor is the first content on its line
				// (only whitespace precedes it), and record that indent.
				// Mirrors src/yaml.ts:1121-1135.
				lineStandalone := true
				anchorIndent := 0
				for bi := pnt.SI - 1; bi >= 0 && lex.Src[bi] != '\n' && lex.Src[bi] != '\r'; bi-- {
					if lex.Src[bi] != ' ' && lex.Src[bi] != '\t' {
						lineStandalone = false
						break
					}
					anchorIndent++
				}

				// Consume the anchor name (and trailing spaces, but NOT the newline).
				skip := nameEnd
				for skip < len(fwd) && (fwd[skip] == ' ' || fwd[skip] == '\t') {
					skip++
				}
				// Skip comments after anchor.
				if skip < len(fwd) && fwd[skip] == '#' {
					for skip < len(fwd) && fwd[skip] != '\n' && fwd[skip] != '\r' {
						skip++
					}
				}
				advanceCol(pnt, fwd, skip)

				// If the anchor is standalone on its own line (followed by a
				// newline), consume the newline and leading spaces so no extra
				// IN token is emitted. Only consume when the next line has
				// content and its indent >= the anchor's indent, otherwise let
				// the normal indent handler manage the transition.
				// Mirrors src/yaml.ts:1187-1205.
				if lineStandalone && pnt.SI < pnt.Len &&
					(lex.Src[pnt.SI] == '\r' || lex.Src[pnt.SI] == '\n') {
					nl := pnt.SI
					if lex.Src[nl] == '\r' {
						nl++
					}
					if nl < pnt.Len && lex.Src[nl] == '\n' {
						nl++
					}
					spaces := 0
					for nl+spaces < pnt.Len && lex.Src[nl+spaces] == ' ' {
						spaces++
					}
					if nl+spaces < pnt.Len {
						nextCh := lex.Src[nl+spaces]
						if nextCh != '\n' && nextCh != '\r' && spaces >= anchorIndent {
							pnt.SI = nl + spaces
							pnt.CI = spaces
							pnt.RI++
						}
					}
				}

				continue // Re-loop to process what follows the anchor
			}

			// Non-specific tag: ! value
			if fwd[0] == '!' && len(fwd) > 1 && fwd[1] != '!' {
				if fwd[1] == ' ' {
					// Non-specific tag: ! value
					valStart := 2
					valEnd := valStart
					for valEnd < len(fwd) && fwd[valEnd] != '\n' && fwd[valEnd] != '\r' {
						valEnd++
					}
					rawVal := trimRight(fwd[valStart:valEnd])
					tkn := lex.Token("#TX", TX, rawVal, fwd[:valEnd])
					advanceCol(pnt, fwd, valEnd)
					return tkn
				}
				// Local tag: !name value — skip the tag.
				tagEnd := 1
				for tagEnd < len(fwd) && fwd[tagEnd] != ' ' && fwd[tagEnd] != '\n' && fwd[tagEnd] != '\r' {
					tagEnd++
				}
				if tagEnd < len(fwd) && fwd[tagEnd] == ' ' {
					tagEnd++
				}
				advanceCol(pnt, fwd, tagEnd)
				// If tag is standalone, consume newline + spaces.
				if pnt.SI < pnt.Len && (lex.Src[pnt.SI] == '\n' || lex.Src[pnt.SI] == '\r') {
					tagStandalone := true
					tagLineIndent := 0
					tbi := pnt.SI - tagEnd - 1
					for tbi >= 0 && lex.Src[tbi] != '\n' && lex.Src[tbi] != '\r' {
						if lex.Src[tbi] != ' ' && lex.Src[tbi] != '\t' {
							tagStandalone = false
							break
						}
						tagLineIndent++
						tbi--
					}
					_ = tagLineIndent
					if tagStandalone {
						nl := pnt.SI
						if nl < pnt.Len && lex.Src[nl] == '\r' {
							nl++
						}
						if nl < pnt.Len && lex.Src[nl] == '\n' {
							nl++
						}
						spaces := 0
						for nl+spaces < pnt.Len && lex.Src[nl+spaces] == ' ' {
							spaces++
						}
						pnt.SI = nl + spaces
						pnt.CI = spaces
						pnt.RI++
					}
				}
				continue
			}

			// !!seq, !!map, !!omap, etc. structural tags — skip them.
			if fwd[0] == '!' && len(fwd) > 1 && fwd[1] == '!' && structTagPrefixRe.MatchString(fwd) {
				skip := 2
				for skip < len(fwd) && fwd[skip] != ' ' && fwd[skip] != '\n' {
					skip++
				}
				for skip < len(fwd) && fwd[skip] == ' ' {
					skip++
				}
				// If standalone, consume newline.
				tagIndent := 0
				tbi := pnt.SI - 1
				standalone := true
				for tbi >= 0 && lex.Src[tbi] != '\n' && lex.Src[tbi] != '\r' {
					if lex.Src[tbi] != ' ' && lex.Src[tbi] != '\t' {
						standalone = false
						break
					}
					tagIndent++
					tbi--
				}
				if standalone && skip < len(fwd) && (fwd[skip] == '\n' || fwd[skip] == '\r') {
					nl := skip
					if nl < len(fwd) && fwd[nl] == '\r' {
						nl++
					}
					if nl < len(fwd) && fwd[nl] == '\n' {
						nl++
					}
					spaces := 0
					for nl+spaces < len(fwd) && fwd[nl+spaces] == ' ' {
						spaces++
					}
					if spaces >= tagIndent {
						skip = nl + spaces
						pnt.SI += skip
						pnt.CI = spaces
						pnt.RI++
						continue
					}
				}
				advanceCol(pnt, fwd, skip)
				continue
			}

			// !!type tags (!!str, !!int, !!float, !!bool, !!null).
			if fwd[0] == '!' && len(fwd) > 1 && fwd[1] == '!' {
				preSI := pnt.SI
				if tkn := handleTypeTag(lex, pnt, fwd, tagHandles, &pendingAnchors, anchors, TX, NR, VL, ST); tkn != nil {
					return tkn
				}
				// Tag followed by a newline was consumed with no token —
				// re-loop so the value on the next line (possibly an anchor
				// or another tag) is handled in this same match call
				// (mirrors TS `continue yamlMatchLoop`, src/yaml.ts:1381-1392).
				if pnt.SI == preSI {
					return nil // safety: no progress, avoid infinite loop
				}
				continue
			}

			// Flow-context `?` explicit-key marker: emit #QM. Pair/elem rule
			// alts handle the marker. Block-context `?` falls through to
			// the heavyweight handleExplicitKey below.
			if fwd[0] == '?' && len(fwd) > 1 && (fwd[1] == ' ' || fwd[1] == '\t') {
				flowState.advance(lex.Src, pnt.SI)
				if flowState.depth > 0 {
					tkn := lex.Token("#QM", QM, tabnas.Undefined, "?")
					pnt.SI++
					pnt.CI++
					return tkn
				}
			}

			// Explicit key: ? key (block context)
			if fwd[0] == '?' && (len(fwd) < 2 || fwd[1] == ' ' || fwd[1] == '\t' ||
				fwd[1] == '\n' || fwd[1] == '\r') {
				return handleExplicitKey(lex, pnt, fwd, &pendingExplicitCL, &pendingTokens, TX, CL, VL, IN)
			}

			// Document markers: --- → #DS, ... → #DE.
			// Only at column 0 (start of line or start of source).
			if isDocMarker(fwd, 0) && atLineStart(lex.Src, pnt.SI) {
				return handleDocMarker(lex, pnt, fwd, DS, DE)
			}

			// Directive line at column 0: emit #DR token. The stream rule
			// applies %TAG handles via the @apply-directive action.
			if fwd[0] == '%' && atLineStart(lex.Src, pnt.SI) {
				pos := 0
				for pos < len(fwd) && fwd[pos] != '\n' && fwd[pos] != '\r' {
					pos++
				}
				directiveSrc := fwd[:pos]
				advanceCol(pnt, fwd, pos)
				return lex.Token("#DR", DR, directiveSrc, directiveSrc)
			}

			// Non-specific tag after ---.
			if fwd[0] == '!' && len(fwd) > 1 && fwd[1] == ' ' {
				valStart := 2
				valEnd := valStart
				for valEnd < len(fwd) && fwd[valEnd] != '\n' && fwd[valEnd] != '\r' {
					valEnd++
				}
				rawVal := trimRight(fwd[valStart:valEnd])
				tkn := lex.Token("#TX", TX, rawVal, fwd[:valEnd])
				advanceCol(pnt, fwd, valEnd)
				return tkn
			}

			// Anchor after --- fall-through.
			if fwd[0] == '&' {
				continue // Will be handled at top of loop
			}

			// YAML double-quoted string.
			if fwd[0] == '"' {
				return handleDoubleQuotedString(lex, pnt, fwd, ST)
			}

			// YAML single-quoted string.
			if fwd[0] == '\'' {
				return handleSingleQuotedString(lex, pnt, fwd, ST)
			}

			// Plain scalars starting with digits that contain colons (e.g. 20:03:20).
			// A sign before the digit counts too: `+190:20:30` is one plain
			// scalar (mirrors src/yaml.ts).
			if (fwd[0] >= '0' && fwd[0] <= '9') ||
				((fwd[0] == '+' || fwd[0] == '-') && len(fwd) > 1 && fwd[1] >= '0' && fwd[1] <= '9') {
				if tkn := handleNumericColon(lex, pnt, fwd, TX, &skipNumberMatch, flowState); tkn != nil {
					return tkn
				}
			}

			// Element marker: - (followed by space/tab/newline/eof)
			if fwd[0] == '-' && (len(fwd) < 2 || fwd[1] == ' ' || fwd[1] == '\t' ||
				fwd[1] == '\n' || fwd[1] == '\r') {
				tkn := lex.Token("#EL", EL, nil, "- ")
				pnt.SI++
				pnt.CI++
				if len(fwd) > 1 && (fwd[1] == ' ' || fwd[1] == '\t') {
					pnt.SI++
					pnt.CI++
				}
				return tkn
			}

			// YAML colon: ": ", ":\t", ":\n", ":" at end.
			isFlowColon := false
			if fwd[0] == ':' && len(fwd) > 1 && fwd[1] != ' ' && fwd[1] != '\t' &&
				fwd[1] != '\n' && fwd[1] != '\r' {
				// Walk back skipping whitespace and any line-comment regions
				// (so e.g. `"foo" # c\n  :bar` recognizes the closing quote).
				prevI := pnt.SI - 1
				for prevI >= 0 {
					pc := lex.Src[prevI]
					if pc == ' ' || pc == '\t' || pc == '\n' || pc == '\r' {
						prevI--
						continue
					}
					// If on a line whose `#` is preceded by whitespace, that's
					// a comment — jump past it and keep walking back.
					lineStart := prevI
					for lineStart > 0 && lex.Src[lineStart-1] != '\n' &&
						lex.Src[lineStart-1] != '\r' {
						lineStart--
					}
					// Track quoting while scanning: a `#` inside a quoted scalar
					// is literal, not a comment start. Without this, the value in
					// {"d":"a #b","e":1} swallows the rest of the line and the
					// following key never lexes.
					hashAt := -1
					quote := byte(0)
					for li := lineStart; li <= prevI; li++ {
						qc := lex.Src[li]
						if quote != 0 {
							// Double quotes escape with backslash, single quotes
							// by doubling the quote character.
							if quote == '"' && qc == '\\' {
								li++
								continue
							}
							if qc == quote {
								if quote == '\'' && li+1 <= prevI && lex.Src[li+1] == '\'' {
									li++
									continue
								}
								quote = 0
							}
							continue
						}
						if qc == '"' || qc == '\'' {
							quote = qc
							continue
						}
						if qc == '#' &&
							(li == lineStart || lex.Src[li-1] == ' ' ||
								lex.Src[li-1] == '\t') {
							hashAt = li
							break
						}
					}
					if hashAt >= 0 {
						prevI = hashAt - 1
						continue
					}
					break
				}
				if prevI >= 0 && (lex.Src[prevI] == '"' || lex.Src[prevI] == '\'') {
					isFlowColon = true
				}
			}
			if fwd[0] == ':' && (len(fwd) < 2 || fwd[1] == ' ' || fwd[1] == '\t' ||
				fwd[1] == '\n' || fwd[1] == '\r' || isFlowColon) {
				tkn := lex.Token("#CL", CL, 1, ": ")
				pnt.SI++
				if len(fwd) > 1 && (fwd[1] == ' ' || fwd[1] == '\t') {
					pnt.CI += 2
				} else if len(fwd) > 1 && (fwd[1] == '\n' || fwd[1] == '\r') {
					// Don't consume newline.
				} else {
					pnt.CI++
				}
				return tkn
			}

			// Newline handling — YAML indentation is significant.
			if fwd[0] == '\n' || fwd[0] == '\r' {
				// Check if we're inside a flow collection (incremental scan).
				flowState.advance(lex.Src, pnt.SI)
				if flowState.depth > 0 {
					// Inside flow collection — consume whitespace.
					// Bump RI for each consumed newline so error positions stay accurate.
					pos := 0
					rows := 0
					// Index of the last consumed \n, for the column below.
					// Only \n is tracked, matching the TypeScript half — a
					// bare \r leaves the column advancing rather than
					// restarting there.
					lastNL := -1
					for pos < len(fwd) && (fwd[pos] == '\n' || fwd[pos] == '\r' ||
						fwd[pos] == ' ' || fwd[pos] == '\t') {
						if fwd[pos] == '\r' && pos+1 < len(fwd) && fwd[pos+1] == '\n' {
							lastNL = pos + 1
							pos += 2
							rows++
							continue
						}
						if fwd[pos] == '\n' || fwd[pos] == '\r' {
							rows++
							if fwd[pos] == '\n' {
								lastNL = pos
							}
						}
						pos++
					}
					if pos < len(fwd) && fwd[pos] == '#' {
						for pos < len(fwd) && fwd[pos] != '\n' && fwd[pos] != '\r' {
							pos++
						}
					}
					pnt.SI += pos
					pnt.RI += rows
					// The column of the NEXT character, 1-based: characters
					// since the last newline. This was the constant 0 — not a
					// valid 1-based column at all, and TypeScript's comment
					// beside its own version says why that matters (a close
					// `]` at column 0 lexes as #BD rather than #CS).
					//
					// TypeScript computes `pos - lastNL`, so a newline
					// followed by three spaces puts the next token at column
					// 4. The constant put it at 1 whatever the indent:
					//
					//     [a,<LF> }        TS col 2   Go was 1
					//     [a,<LF>   }      TS col 4   Go was 1
					//
					// Counted in characters, like every other column here.
					if lastNL >= 0 {
						pnt.CI = utf8.RuneCountInString(fwd[lastNL:pos])
					} else {
						pnt.CI += utf8.RuneCountInString(fwd[:pos])
					}
					continue
				}

				// Block context newline — emit #IN with indent level.
				pos := 0
				spaces := 0
				rows := 0
				for pos < len(fwd) {
					if fwd[pos] == '\r' && pos+1 < len(fwd) && fwd[pos+1] == '\n' {
						pos += 2
						rows++
					} else if fwd[pos] == '\n' {
						pos++
						rows++
					} else {
						break
					}
					spaces = 0
					for pos < len(fwd) && fwd[pos] == ' ' {
						pos++
						spaces++
					}
					// Comment-only line — skip.
					if pos < len(fwd) && fwd[pos] == '#' {
						for pos < len(fwd) && fwd[pos] != '\n' && fwd[pos] != '\r' {
							pos++
						}
						continue
					}
					// Tab-only line — skip.
					if pos < len(fwd) && fwd[pos] == '\t' {
						tp := pos
						for tp < len(fwd) && (fwd[tp] == ' ' || fwd[tp] == '\t') {
							tp++
						}
						if tp >= len(fwd) || fwd[tp] == '\n' || fwd[tp] == '\r' {
							pos = tp
							continue
						}
					}
					// Anchor-only line.
					if pos < len(fwd) && fwd[pos] == '&' {
						ae := pos + 1
						for ae < len(fwd) && fwd[ae] != ' ' && fwd[ae] != '\t' &&
							fwd[ae] != '\n' && fwd[ae] != '\r' {
							ae++
						}
						afterAnchor := ae
						for afterAnchor < len(fwd) && (fwd[afterAnchor] == ' ' || fwd[afterAnchor] == '\t') {
							afterAnchor++
						}
						if afterAnchor >= len(fwd) || fwd[afterAnchor] == '\n' ||
							fwd[afterAnchor] == '\r' || fwd[afterAnchor] == '#' {
							pendingAnchors = append(pendingAnchors, anchorInfo{name: fwd[pos+1 : ae], inline: false})
							for afterAnchor < len(fwd) && fwd[afterAnchor] != '\n' && fwd[afterAnchor] != '\r' {
								afterAnchor++
							}
							pos = afterAnchor
							continue
						}
					}
				}

				// Consumed everything — emit ZZ.
				if pos >= len(fwd) {
					pnt.SI += pos
					pnt.RI += rows
					pnt.CI = spaces + 1
					tkn := lex.Token("#ZZ", ZZ, tabnas.Undefined, "")
					return tkn
				}

				// Skip #IN when next line is a doc-frame marker (--- / ...)
				// or directive (%) — let the next call emit #DS / #DE / #DR.
				if spaces == 0 && (isDocMarker(fwd, pos) || fwd[pos] == '%') {
					pnt.SI += pos
					pnt.RI += rows
					pnt.CI = 1
					continue
				}

				// Skip #IN when next content is a flow indicator or quoted
				// string at column 0 — there's no block to indent into. A
				// quoted KEY is different: it continues (or closes back to)
				// the root block mapping, so it needs its #IN like a plain key
				// does (tabnas/yaml#86).
				if spaces == 0 &&
					(fwd[pos] == '{' || fwd[pos] == '[' ||
						((fwd[pos] == '"' || fwd[pos] == '\'') && !quotedKeyAt(fwd, pos))) {
					pnt.SI += pos
					pnt.RI += rows
					pnt.CI = 1
					continue
				}

				// Emit #IN with indent level.
				tkn := lex.Token("#IN", IN, spaces, fwd[:pos])
				pnt.SI += pos
				pnt.RI += rows
				pnt.CI = spaces + 1
				return tkn
			}

			break // End of yamlMatchLoop
		}

		return nil
	}

	// Skip number matching when the yamlMatcher detected trailing text
	// after a digit-starting value (e.g. "64 characters, hexadecimal.").
	numberCheck := func(lex *tabnas.Lex) *tabnas.LexCheckResult {
		if skipNumberMatch {
			skipNumberMatch = false
			return &tabnas.LexCheckResult{Done: true}
		}
		return nil
	}

	// The lexer hooks go in THROUGH OPTIONS, as the TS plugin passes them
	// to tn.options(). SetOptions rebuilds the lexer config from options
	// and copies it over the live one, so a hook written onto the live
	// config was lost to the caller's next SetOptions, and without the
	// number and text checks ordinary block YAML stopped parsing (#81).
	//
	// - The YAML matcher, and `stream` as the start rule (replacing
	//   `val`) so the stream rule below consumes #DS / #DE / #DR
	//   doc-frame tokens.
	// - The text check, as Text.Check (TS text.check).
	// - The number check, as a config modifier rather than Number.Check:
	//   in this engine Number options without a Sep switch the `_` digit
	//   separator off, and `1_000` needs it. A modifier re-runs on every
	//   rebuild, so it survives a later SetOptions as an option does.
	j.SetOptions(tabnas.Options{
		Lex: &tabnas.LexOptions{Match: map[string]*tabnas.MatchSpec{
			"yaml": {Order: 500000, Make: func(_ *tabnas.LexConfig, _ *tabnas.Options) tabnas.LexMatcher {
				return yamlMatcher
			}},
		}},
		Rule: &tabnas.RuleOptions{Start: "stream"},
		Text: &tabnas.TextOptions{Check: textCheck},
		Property: &tabnas.PropertyOptions{ConfigModify: map[string]tabnas.ConfigModifier{
			"yaml-number-check": func(built *tabnas.LexConfig, _ *tabnas.Options) {
				built.NumberCheck = numberCheck
			},
		}},
	})

	// Remove colon as a fixed token — YAML uses ": " (colon-space). Fixed
	// tokens are per-instance state that SetOptions carries forward.
	delete(cfg.FixedTokens, ":")
	cfg.SortFixedTokens()

	// ===== Grammar rules =====
	configureGrammarRules(j, IN, EL, KEY, CL, ZZ, CA, CS, CB, TX, ST, VL, NR,
		anchors, &pendingAnchors)

	// ===== Stream rule: top-level YAML document collector =====
	// Replaces `val` as the parser's start rule. Consumes #DS / #DE / #DR
	// tokens emitted by yamlMatcher, pushes a fresh val for each document's
	// content, and accumulates results. Final shape:
	//   - 0 docs (empty source) → nil
	//   - 1 doc                  → the single value
	//   - >1 docs                → []any
	// When wantMeta is true, the final result is wrapped as *MetaResult.
	ensureCurMeta := func() {
		if streamCurMeta == nil {
			streamCurMeta = &DocMeta{Directives: []string{}}
		}
	}
	flushCurMeta := func(ended bool) {
		ensureCurMeta()
		if ended {
			streamCurMeta.Ended = true
		}
		streamMeta = append(streamMeta, streamCurMeta)
		streamCurMeta = nil
	}
	// The document is read through childNode: a top-level implicit list
	// (`"a" "b"`) rotates the pushed `val` into a `list`, and only the end
	// of that chain holds every element.
	pushChildDoc := func(r *tabnas.Rule) {
		if node := childNode(r); !tabnas.IsUndefined(node) {
			streamDocs = append(streamDocs, node)
		} else {
			streamDocs = append(streamDocs, nil)
		}
	}
	accumChildDoc := func(r *tabnas.Rule, _ *tabnas.Context) {
		pushChildDoc(r)
		// The matched close-phase token tells us if this doc ended with `...`.
		ended := r.C0 != nil && r.C0.Tin == DE
		flushCurMeta(ended)
	}
	finalizeStream := func(r *tabnas.Rule, ctx *tabnas.Context) {
		if node := childNode(r); !tabnas.IsUndefined(node) {
			streamDocs = append(streamDocs, node)
			flushCurMeta(false)
		} else if streamCurMeta != nil {
			// The final document was explicitly opened (a `---` / `%TAG`
			// directive started a doc, recorded in streamCurMeta) but its
			// value coalesced to undefined — a bare `---` at end-of-stream,
			// or a trailing empty doc in `---\n---\n---`. jsonic's val-close
			// treats a deliberate @val-set-null as undefined, so the empty
			// doc's null is lost here; restore it the same way accumChildDoc
			// forces a null for the non-final empty docs.
			// Without an open doc (empty or comment-only source) streamCurMeta
			// stays nil and the stream correctly finalizes to nil/undefined.
			streamDocs = append(streamDocs, nil)
			flushCurMeta(false)
		}
		var content any
		switch len(streamDocs) {
		case 0:
			content = nil
		case 1:
			content = streamDocs[0]
		default:
			content = append([]any(nil), streamDocs...)
		}
		var result any = content
		if wantMeta {
			var meta any
			switch len(streamMeta) {
			case 0:
				meta = nil
			case 1:
				meta = streamMeta[0]
			default:
				meta = append([]*DocMeta(nil), streamMeta...)
			}
			result = &MetaResult{Meta: meta, Content: content}
		}
		r.Node = result
		// Rotation via `r: stream` creates a chain; ctx.Root is the
		// original stream the parser hands back.
		if ctx.Root != nil {
			ctx.Root.Node = result
		}
		// Reset for any subsequent parse on the same plugin instance.
		streamDocs = nil
		streamMeta = nil
		streamCurMeta = nil
	}
	applyDirective := func(r *tabnas.Rule, _ *tabnas.Context) {
		src := r.O0.Src
		if src == "" {
			if s, ok := r.O0.Val.(string); ok {
				src = s
			}
		}
		if m := yamlTagDirectiveRe.FindStringSubmatch(src); m != nil {
			tagHandles[m[1]] = m[2]
		}
		ensureCurMeta()
		streamCurMeta.Directives = append(streamCurMeta.Directives, src)
	}
	markExplicit := func(_ *tabnas.Rule, _ *tabnas.Context) {
		ensureCurMeta()
		streamCurMeta.Explicit = true
	}

	j.Rule("stream", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddOpen(
			// Consume directive line; rotate to stream to look for the next token.
			&tabnas.AltSpec{S: [][]tabnas.Tin{{DR}}, A: applyDirective, R: "stream", G: "yaml"},
			// Explicit doc start: push val for the document content.
			&tabnas.AltSpec{S: [][]tabnas.Tin{{DS}}, A: markExplicit, P: "val", G: "yaml"},
			// `...` with no document open: it terminates nothing, so it does
			// NOT produce a document. Consume it and look for the next one.
			// (YAML 1.2 9.1.2; yaml-test-suite HWV9 / QT73 / M7A3, where a
			// stray or comment-only `...` region yields no document at all.)
			&tabnas.AltSpec{S: [][]tabnas.Tin{{DE}}, R: "stream", G: "yaml"},
			// Empty source: end immediately.
			&tabnas.AltSpec{S: [][]tabnas.Tin{{ZZ}}, B: 1, G: "yaml"},
			// Implicit first doc.
			&tabnas.AltSpec{P: "val", G: "yaml"},
		)
		rs.AddClose(
			// End of input: accumulate last doc, finalize result shape.
			&tabnas.AltSpec{S: [][]tabnas.Tin{{ZZ}}, A: finalizeStream, G: "yaml"},
			// Directive between docs.
			&tabnas.AltSpec{S: [][]tabnas.Tin{{DR}},
				A: func(r *tabnas.Rule, ctx *tabnas.Context) {
					accumChildDoc(r, ctx)
					applyDirective(r, ctx)
				},
				R: "stream", G: "yaml"},
			// ... terminator: accumulate, look for next doc.
			&tabnas.AltSpec{S: [][]tabnas.Tin{{DE}}, A: accumChildDoc, R: "stream", G: "yaml"},
			// --- start of next doc (back up so stream.open consumes it).
			&tabnas.AltSpec{S: [][]tabnas.Tin{{DS}}, B: 1, A: accumChildDoc, R: "stream", G: "yaml"},
		)
	})

	return nil
}

// isWordByte reports whether b is an ASCII letter or digit (used for the
// apostrophe-in-word check throughout flow preprocessing).
func isWordByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// handleBlockScalar processes | and > block scalar indicators.
func handleBlockScalar(lex *tabnas.Lex, pnt *tabnas.Point, src, fwd string, ch byte) *tabnas.LexCheckResult {
	fold := ch == '>'
	chomp := "clip"
	explicitIndent := 0
	idx := 1

	// Parse chomping and indent indicators.
	for pi := 0; pi < 2 && idx < len(fwd); pi++ {
		if fwd[idx] == '+' {
			chomp = "keep"
			idx++
		} else if fwd[idx] == '-' {
			chomp = "strip"
			idx++
		} else if fwd[idx] >= '1' && fwd[idx] <= '9' {
			explicitIndent = int(fwd[idx] - '0')
			idx++
		}
	}

	// Skip trailing spaces and comments.
	for idx < len(fwd) && fwd[idx] == ' ' {
		idx++
	}
	if idx < len(fwd) && fwd[idx] == '#' {
		for idx < len(fwd) && fwd[idx] != '\n' && fwd[idx] != '\r' {
			idx++
		}
	}

	// Must be followed by newline or eof.
	if idx < len(fwd) && fwd[idx] != '\n' && fwd[idx] != '\r' {
		return nil // Not a block scalar.
	}

	// Skip the indicator line's newline.
	if idx < len(fwd) && fwd[idx] == '\r' {
		idx++
	}
	if idx < len(fwd) && fwd[idx] == '\n' {
		idx++
	}

	// Determine block indent.
	blockIndent := 0
	if explicitIndent == 0 {
		// Auto-detect from first content line.
		tempIdx := idx
		for tempIdx < len(fwd) {
			lineSpaces := 0
			for tempIdx+lineSpaces < len(fwd) && fwd[tempIdx+lineSpaces] == ' ' {
				lineSpaces++
			}
			afterSpaces := tempIdx + lineSpaces
			if afterSpaces >= len(fwd) || fwd[afterSpaces] == '\n' || fwd[afterSpaces] == '\r' {
				tempIdx = afterSpaces
				if tempIdx < len(fwd) && fwd[tempIdx] == '\r' {
					tempIdx++
				}
				if tempIdx < len(fwd) && fwd[tempIdx] == '\n' {
					tempIdx++
				}
				continue
			}
			blockIndent = lineSpaces
			break
		}
	}

	// Determine containing indent.
	containingIndent := 0
	isDocStart := false
	isDocRoot := false
	// Walk back from the indicator to the start of its own line. Start at
	// pnt.SI, not pnt.SI - 1: when the indicator IS the first character of
	// its line, SI - 1 is the preceding newline and the scan would land on
	// the PREVIOUS line, mis-reading both containingIndent and the --- check.
	li := pnt.SI
	for li > 0 && src[li-1] != '\n' && src[li-1] != '\r' {
		li--
	}
	if li == 0 && strings.HasPrefix(src, bomText) {
		li = len(bomText)
	}
	lineStart := li
	for li < pnt.SI && src[li] == ' ' {
		containingIndent++
		li++
	}
	if lineStart+2 < len(src) && src[lineStart] == '-' && src[lineStart+1] == '-' && src[lineStart+2] == '-' {
		isDocStart = true
	}
	// The indicator is the first thing on a column-0 line, so nothing can
	// enclose it — it can only be a document's root node. YAML gives the root
	// node parent indent -1, so content at column 0 is "more indented" and the
	// block is not empty. (yaml-test-suite M7A3: a bare `|` document whose
	// content starts at column 0.)
	isDocRoot = containingIndent == 0 && li == pnt.SI

	// Apply explicit indent.
	if explicitIndent > 0 {
		hasColonOnLine := false
		for ci := lineStart + containingIndent; ci < pnt.SI; ci++ {
			if src[ci] == ':' && ci+1 < len(src) && (src[ci+1] == ' ' || src[ci+1] == '\t') {
				hasColonOnLine = true
				break
			}
		}
		keyCol := containingIndent
		if hasColonOnLine {
			scanI := lineStart + containingIndent
			for scanI < pnt.SI && src[scanI] == '-' &&
				scanI+1 < len(src) && (src[scanI+1] == ' ' || src[scanI+1] == '\t') {
				keyCol += 2
				scanI += 2
				for scanI < pnt.SI && src[scanI] == ' ' {
					keyCol++
					scanI++
				}
			}
			blockIndent = keyCol + explicitIndent
		} else {
			parentIndent := 0
			searchI := lineStart - 1
			if searchI > 0 {
				if src[searchI] == '\n' {
					searchI--
				}
				if searchI > 0 && src[searchI] == '\r' {
					searchI--
				}
				prevLineEnd := searchI + 1
				for searchI > 0 && src[searchI-1] != '\n' && src[searchI-1] != '\r' {
					searchI--
				}
				prevLineStart := searchI
				for ci := prevLineStart; ci < prevLineEnd; ci++ {
					if src[ci] == ':' && (ci+1 >= prevLineEnd || src[ci+1] == ' ' ||
						src[ci+1] == '\t' || src[ci+1] == '\n' || src[ci+1] == '\r') {
						parentIndent = 0
						pi := prevLineStart
						for pi < prevLineEnd && src[pi] == ' ' {
							parentIndent++
							pi++
						}
						break
					}
				}
			}
			blockIndent = parentIndent + explicitIndent
			containingIndent = parentIndent
		}
	}

	if blockIndent <= containingIndent && !isDocStart && !isDocRoot && idx < len(fwd) {
		// Content is not indented enough — empty block scalar.
		var val string
		if chomp == "keep" {
			blankCount := 0
			bi := idx
			for bi < len(fwd) {
				if fwd[bi] == '\n' {
					blankCount++
					bi++
				} else if fwd[bi] == '\r' {
					bi++
					if bi < len(fwd) && fwd[bi] == '\n' {
						bi++
					}
					blankCount++
				} else {
					break
				}
			}
			if blankCount > 0 {
				val = strings.Repeat("\n", blankCount)
			} else {
				val = "\n"
			}
			idx = bi
		} else {
			val = ""
		}
		tkn := lex.Token("#TX", tabnas.TinTX, val, fwd[:idx])
		pnt.SI += idx
		pnt.RI++
		pnt.CI = 0
		return &tabnas.LexCheckResult{Done: true, Token: tkn}
	}

	// Collect indented lines.
	var lines []string
	pos := idx
	rows := 1
	lastNewlinePos := idx
	for pos < len(fwd) {
		lineIndent := 0
		for pos+lineIndent < len(fwd) && fwd[pos+lineIndent] == ' ' {
			lineIndent++
		}
		afterSpaces := pos + lineIndent
		if afterSpaces >= len(fwd) || fwd[afterSpaces] == '\n' || fwd[afterSpaces] == '\r' {
			if lineIndent > blockIndent {
				lines = append(lines, fwd[pos+blockIndent:afterSpaces])
			} else {
				lines = append(lines, "")
			}
			lastNewlinePos = afterSpaces
			pos = afterSpaces
			if pos < len(fwd) && fwd[pos] == '\r' {
				pos++
			}
			if pos < len(fwd) && fwd[pos] == '\n' {
				pos++
			}
			rows++
			continue
		}
		if lineIndent < blockIndent {
			break
		}
		// The one marker test of the four that takes NO tab after the
		// marker: `---<TAB>` stays inside the scalar (src/yaml.ts:576).
		if lineIndent == 0 && isDocMarkerNoTab(fwd, pos) {
			break
		}
		lineStartPos := pos + blockIndent
		lineEnd := lineStartPos
		for lineEnd < len(fwd) && fwd[lineEnd] != '\n' && fwd[lineEnd] != '\r' {
			lineEnd++
		}
		lines = append(lines, fwd[lineStartPos:lineEnd])
		lastNewlinePos = lineEnd
		pos = lineEnd
		if pos < len(fwd) && fwd[pos] == '\r' {
			pos++
		}
		if pos < len(fwd) && fwd[pos] == '\n' {
			pos++
		}
		rows++
	}

	// Build scalar value.
	var val string
	if fold {
		val = foldLines(lines)
	} else {
		val = strings.Join(lines, "\n")
	}

	// Apply chomping.
	if len(lines) == 0 {
		val = ""
	} else if chomp == "strip" {
		val = strings.TrimRight(val, "\n")
	} else if chomp == "clip" {
		val = strings.TrimRight(val, "\n") + "\n"
	} else {
		// keep
		val = val + "\n"
	}

	// Don't consume final newline if more content follows.
	endPos := pos
	endRows := rows
	if pos < len(fwd) && pos > lastNewlinePos {
		ni := pos
		nextLineIndent := 0
		for ni < len(fwd) && fwd[ni] == ' ' {
			nextLineIndent++
			ni++
		}
		// Three marker characters and nothing about what follows them,
		// as the canonical tests here (src/yaml.ts:674).
		isNextDocMarker := nextLineIndent == 0 && isDocMarkerRun(fwd, ni)
		if !isNextDocMarker {
			endPos = lastNewlinePos
			endRows = rows - 1
		}
	}

	tkn := lex.Token("#TX", tabnas.TinTX, val, fwd[:endPos])
	pnt.SI += endPos
	pnt.RI += endRows
	pnt.CI = 0
	return &tabnas.LexCheckResult{Done: true, Token: tkn}
}

// foldLines implements YAML folded scalar line joining.
func foldLines(lines []string) string {
	var result strings.Builder
	prevWasNormal := false
	pendingEmptyCount := 0

	for _, line := range lines {
		isMore := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
		isEmpty := line == ""

		if isEmpty {
			pendingEmptyCount++
		} else if isMore {
			if prevWasNormal && result.Len() > 0 {
				result.WriteByte('\n')
			}
			for ei := 0; ei < pendingEmptyCount; ei++ {
				result.WriteByte('\n')
			}
			pendingEmptyCount = 0
			if result.Len() > 0 {
				s := result.String()
				if s[len(s)-1] != '\n' {
					result.WriteByte('\n')
				}
			}
			result.WriteString(line)
			result.WriteByte('\n')
			prevWasNormal = false
		} else {
			if pendingEmptyCount > 0 {
				if prevWasNormal && result.Len() > 0 {
					result.WriteByte('\n')
					for ei := 1; ei < pendingEmptyCount; ei++ {
						result.WriteByte('\n')
					}
				} else {
					for ei := 0; ei < pendingEmptyCount; ei++ {
						result.WriteByte('\n')
					}
				}
				pendingEmptyCount = 0
			}
			if prevWasNormal && result.Len() > 0 {
				s := result.String()
				if s[len(s)-1] != '\n' {
					result.WriteByte(' ')
				}
			}
			result.WriteString(line)
			prevWasNormal = true
		}
	}
	for ei := 0; ei < pendingEmptyCount; ei++ {
		result.WriteByte('\n')
	}
	return result.String()
}

// handleTagInTextCheck processes !!type tags encountered in the text check callback.
func handleTagInTextCheck(lex *tabnas.Lex, pnt *tabnas.Point, fwd string, tagHandles map[string]string) *tabnas.LexCheckResult {
	tagEnd := 2
	for tagEnd < len(fwd) && fwd[tagEnd] != ' ' && fwd[tagEnd] != '\n' && fwd[tagEnd] != '\r' {
		tagEnd++
	}
	tag := fwd[2:tagEnd]
	if tag == "seq" || tag == "map" {
		return nil // Let yamlMatcher handle.
	}
	valStart := tagEnd
	if valStart < len(fwd) && fwd[valStart] == ' ' {
		valStart++
	}
	rawVal := ""
	valEnd := valStart
	if valStart < len(fwd) && (fwd[valStart] == '"' || fwd[valStart] == '\'') {
		q := fwd[valStart]
		valEnd = valStart + 1
		for valEnd < len(fwd) && fwd[valEnd] != q {
			if fwd[valEnd] == '\\' && q == '"' {
				valEnd++
			}
			valEnd++
		}
		if valEnd < len(fwd) && fwd[valEnd] == q {
			valEnd++
		}
		rawVal = jsSubstringLessOneUnit(fwd, valStart+1, valEnd)
	} else {
		// A `#` straight after the tag's space starts a comment.
		for valEnd < len(fwd) && fwd[valEnd] != '\n' && fwd[valEnd] != '\r' {
			if valEnd == valStart && fwd[valEnd] == '#' && valStart > 0 &&
				(fwd[valStart-1] == ' ' || fwd[valStart-1] == '\t') {
				break
			}
			if fwd[valEnd] == ':' && (valEnd+1 >= len(fwd) || fwd[valEnd+1] == ' ' ||
				fwd[valEnd+1] == '\n' || fwd[valEnd+1] == '\r') {
				break
			}
			if fwd[valEnd] == ' ' && valEnd+1 < len(fwd) && fwd[valEnd+1] == '#' {
				break
			}
			valEnd++
		}
		rawVal = trimRight(fwd[valStart:valEnd])
	}

	result := applyTagConversion(tag, rawVal, tagHandles)
	tknTin := tabnas.TinTX
	switch result.(type) {
	case float64:
		tknTin = tabnas.TinNR
	case bool, nil:
		tknTin = tabnas.TinVL
	}
	if result == nil {
		tknTin = tabnas.TinVL
	}

	tkn := lex.Token(tinToName(tknTin), tknTin, result, fwd[:valEnd])
	advanceCol(pnt, fwd, valEnd)
	return &tabnas.LexCheckResult{Done: true, Token: tkn}
}

// handlePlainScalar processes YAML plain scalar values with multiline continuation.
func handlePlainScalar(lex *tabnas.Lex, pnt *tabnas.Point, src, fwd string, flowState *flowScanState) *tabnas.LexCheckResult {
	// Detect flow context (incremental scan, see flowScanState).
	flowState.advance(src, pnt.SI)
	inFlowCtx := flowState.depth > 0

	// Find current line indent.
	lineStartPos := pnt.SI
	for lineStartPos > 0 && src[lineStartPos-1] != '\n' && src[lineStartPos-1] != '\r' {
		lineStartPos--
	}
	if lineStartPos == 0 && strings.HasPrefix(src, bomText) {
		lineStartPos = len(bomText)
	}
	currentLineIndent := 0
	ci := lineStartPos
	for ci < pnt.SI && src[ci] == ' ' {
		currentLineIndent++
		ci++
	}

	// Check if text is preceded by ": " on the same line.
	isMapValue := false
	ci = pnt.SI - 1
	for ci >= lineStartPos && (src[ci] == ' ' || src[ci] == '\t') {
		ci--
	}
	if ci >= lineStartPos && src[ci] == ':' {
		isMapValue = true
	}

	minContinuationIndent := currentLineIndent
	if isMapValue {
		minContinuationIndent = currentLineIndent + 1
	}

	// Scan first line.
	text := ""
	i := 0
	totalConsumed := 0
	rows := 0

	scanLine := func() string {
		start := i
		for i < len(fwd) {
			c := fwd[i]
			if c == '\n' || c == '\r' {
				break
			}
			if c == ':' && (i+1 >= len(fwd) || fwd[i+1] == ' ' || fwd[i+1] == '\t' ||
				fwd[i+1] == '\n' || fwd[i+1] == '\r') {
				break
			}
			if (c == ' ' || c == '\t') && i+1 < len(fwd) && fwd[i+1] == '#' {
				break
			}
			if inFlowCtx && (c == ']' || c == '}') {
				break
			}
			if c == ',' && inFlowCtx {
				break
			}
			i++
		}
		return trimRight(fwd[start:i])
	}

	text = scanLine()
	totalConsumed = i

	// Check for continuation lines (multiline plain scalars).
	for i < len(fwd) && (fwd[i] == '\n' || fwd[i] == '\r') {
		nlPos := i
		blankLines := 0
		for i < len(fwd) && (fwd[i] == '\n' || fwd[i] == '\r') {
			if fwd[i] == '\r' {
				i++
			}
			if i < len(fwd) && fwd[i] == '\n' {
				i++
			}
			li := 0
			for i+li < len(fwd) && (fwd[i+li] == ' ' || fwd[i+li] == '\t') {
				li++
			}
			if i+li >= len(fwd) || fwd[i+li] == '\n' || fwd[i+li] == '\r' {
				blankLines++
				i += li
				continue
			}
			break
		}
		lineIndent := 0
		for i < len(fwd) && (fwd[i] == ' ' || fwd[i] == '\t') {
			lineIndent++
			i++
		}

		isNextDocMarker := lineIndent == 0 && i < len(fwd) && isDocMarker(fwd, i)
		isSeqMarker := false
		if i < len(fwd) && fwd[i] == '-' && (i+1 >= len(fwd) || fwd[i+1] == ' ' ||
			fwd[i+1] == '\t' || fwd[i+1] == '\n' || fwd[i+1] == '\r') {
			seqIndent := -1
			si := pnt.SI - 1
			for si >= lineStartPos {
				if src[si] == '-' && (si+1 < len(src) && (src[si+1] == ' ' || src[si+1] == '\t')) {
					seqIndent = si - lineStartPos
					break
				}
				si--
			}
			isSeqMarker = (seqIndent >= 0 && lineIndent == seqIndent) ||
				(seqIndent < 0 && lineIndent <= currentLineIndent)
		}

		canContinue := false
		if inFlowCtx {
			canContinue = i < len(fwd) && fwd[i] != '\n' && fwd[i] != '\r' &&
				fwd[i] != '#' && fwd[i] != '{' && fwd[i] != '}' &&
				fwd[i] != '[' && fwd[i] != ']'
		} else {
			canContinue = lineIndent >= minContinuationIndent && i < len(fwd) &&
				fwd[i] != '\n' && fwd[i] != '\r' && fwd[i] != '#' &&
				!isNextDocMarker && !isSeqMarker
		}

		if canContinue {
			// Check if continuation line is a key-value pair.
			isKV := false
			peekJ := i
			for peekJ < len(fwd) && fwd[peekJ] != '\n' && fwd[peekJ] != '\r' {
				if fwd[peekJ] == ':' && (peekJ+1 >= len(fwd) || fwd[peekJ+1] == ' ' ||
					fwd[peekJ+1] == '\t' || fwd[peekJ+1] == '\n' || fwd[peekJ+1] == '\r') {
					isKV = true
					break
				}
				if fwd[peekJ] == '}' || fwd[peekJ] == ']' || fwd[peekJ] == ',' {
					break
				}
				peekJ++
			}
			if !isKV || inFlowCtx {
				contLine := scanLine()
				if len(contLine) > 0 {
					if blankLines > 0 {
						for b := 0; b < blankLines; b++ {
							text += "\n"
						}
					} else {
						text += " "
					}
					text += contLine
					totalConsumed = i
					rows++
					continue
				}
			}
		}
		i = nlPos
		break
	}

	text = trimRight(text)
	if len(text) == 0 {
		return nil
	}

	// Check if this is a YAML value keyword.
	if val, ok := isYamlValue(text); ok {
		tkn := lex.Token("#VL", tabnas.TinVL, val, text)
		// Correct today by accident of the keyword set — `true`, `null`,
		// `~` and the rest are ASCII — and written in the unit the column
		// actually means, so it stays correct if that ever changes.
		advanceCol(pnt, text, len(text))
		return &tabnas.LexCheckResult{Done: true, Token: tkn}
	}

	// Check if it's a number.
	if num, ok := parseYamlNumber(text); ok {
		tkn := lex.Token("#NR", tabnas.TinNR, num, text)
		// Correct today by accident of the grammar — a YAML number is
		// ASCII, so bytes and characters coincide — and written in the
		// unit the column actually means, so it stays correct if that
		// ever changes.
		advanceCol(pnt, text, len(text))
		return &tabnas.LexCheckResult{Done: true, Token: tkn}
	}

	// Plain text. THE path a non-ASCII scalar takes, and the one the
	// column bug lived on: `pnt.CI += totalConsumed` charged a byte
	// count. Traced rather than guessed — instrumenting every CI site
	// and parsing `{a: é, ]` showed this line and one other firing, out
	// of forty.
	//
	// The TypeScript half is `pnt.cI += totalConsumed  // approximate`,
	// and the approximation is the multi-row case below: when `rows > 0`
	// the column should restart on the last line rather than accumulate
	// across the newlines. That is true in BOTH ports and is not what
	// this changes — the two are now approximate in the same way and in
	// the same unit, which is the parity claim. Making them exact is a
	// separate question, and one the TypeScript comment has been asking
	// for longer.
	tkn := lex.Token("#TX", tabnas.TinTX, text, fwd[:totalConsumed])
	pnt.RI += rows
	advanceCol(pnt, fwd, totalConsumed)
	return &tabnas.LexCheckResult{Done: true, Token: tkn}
}

// handleTypeTag processes !!type tags (!!str, !!int, !!float, etc.).
func handleTypeTag(lex *tabnas.Lex, pnt *tabnas.Point, fwd string,
	tagHandles map[string]string, pendingAnchors *[]anchorInfo,
	anchors map[string]any, TX, NR, VL, ST tabnas.Tin) *tabnas.Token {

	tagEnd := 2
	for tagEnd < len(fwd) && fwd[tagEnd] != ' ' && fwd[tagEnd] != '\n' &&
		fwd[tagEnd] != '\r' && fwd[tagEnd] != ',' &&
		fwd[tagEnd] != '}' && fwd[tagEnd] != ']' && fwd[tagEnd] != ':' {
		tagEnd++
	}
	tag := fwd[2:tagEnd]
	valStart := tagEnd
	if valStart < len(fwd) && fwd[valStart] == ' ' {
		valStart++
	}
	valEnd := valStart

	// Skip anchor before value.
	tagAnchorName := ""
	if valStart < len(fwd) && fwd[valStart] == '&' {
		anchorEnd := valStart + 1
		for anchorEnd < len(fwd) && fwd[anchorEnd] != ' ' && fwd[anchorEnd] != '\n' && fwd[anchorEnd] != '\r' {
			anchorEnd++
		}
		tagAnchorName = fwd[valStart+1 : anchorEnd]
		*pendingAnchors = append(*pendingAnchors, anchorInfo{name: tagAnchorName, inline: true})
		if anchorEnd < len(fwd) && fwd[anchorEnd] == ' ' {
			anchorEnd++
		}
		valStart = anchorEnd
		valEnd = valStart
	}

	// Check for quoted value.
	if valStart < len(fwd) && (fwd[valStart] == '"' || fwd[valStart] == '\'') {
		q := fwd[valStart]
		valEnd = valStart + 1
		for valEnd < len(fwd) && fwd[valEnd] != q {
			if fwd[valEnd] == '\\' && q == '"' {
				valEnd++
			}
			valEnd++
		}
		if valEnd < len(fwd) && fwd[valEnd] == q {
			valEnd++
		}
		rawVal := jsSubstringLessOneUnit(fwd, valStart+1, valEnd)
		result := applyTagConversion(tag, rawVal, tagHandles)
		if tagAnchorName != "" {
			anchors[tagAnchorName] = result
		}
		tknTin := TX
		switch result.(type) {
		case float64:
			tknTin = NR
		case bool:
			tknTin = VL
		}
		if result == nil {
			tknTin = VL
		}
		tkn := lex.Token(tinToName(tknTin), tknTin, result, fwd[:valEnd])
		advanceCol(pnt, fwd, valEnd)
		return tkn
	}

	// Tag followed by newline — skip and let next cycle handle.
	if valStart < len(fwd) && (fwd[valStart] == '\n' || fwd[valStart] == '\r') && valStart < len(fwd)-1 {
		nl := valStart
		if nl < len(fwd) && fwd[nl] == '\r' {
			nl++
		}
		if nl < len(fwd) && fwd[nl] == '\n' {
			nl++
		}
		pnt.SI += nl
		pnt.CI = 0
		pnt.RI++
		return nil // Will re-enter matcher
	}

	// Unquoted value. A `#` straight after the tag's space starts a
	// comment, so the value is empty (`!!str #c` is "").
	for valEnd < len(fwd) && fwd[valEnd] != '\n' && fwd[valEnd] != '\r' &&
		fwd[valEnd] != ',' && fwd[valEnd] != '}' && fwd[valEnd] != ']' {
		if valEnd == valStart && fwd[valEnd] == '#' && valStart > 0 &&
			(fwd[valStart-1] == ' ' || fwd[valStart-1] == '\t') {
			break
		}
		if fwd[valEnd] == ':' && (valEnd+1 >= len(fwd) || fwd[valEnd+1] == ' ' ||
			fwd[valEnd+1] == '\n' || fwd[valEnd+1] == '\r') {
			break
		}
		if fwd[valEnd] == ' ' && valEnd+1 < len(fwd) && fwd[valEnd+1] == '#' {
			break
		}
		valEnd++
	}
	rawVal := trimRight(fwd[valStart:valEnd])
	result := applyTagConversion(tag, rawVal, tagHandles)
	if tagAnchorName != "" {
		anchors[tagAnchorName] = result
	}
	tknTin := TX
	switch result.(type) {
	case string:
		if result.(string) == "" {
			tknTin = ST
		} else {
			tknTin = TX
		}
	case float64:
		tknTin = NR
	case bool:
		tknTin = VL
	}
	if result == nil {
		tknTin = VL
	}
	tkn := lex.Token(tinToName(tknTin), tknTin, result, fwd[:valEnd])
	advanceCol(pnt, fwd, valEnd)
	return tkn
}

// handleExplicitKey processes ? key\n: value patterns.
func handleExplicitKey(lex *tabnas.Lex, pnt *tabnas.Point, fwd string,
	pendingExplicitCL *bool, pendingTokens *[]*tabnas.Token,
	TX, CL, VL, IN tabnas.Tin) *tabnas.Token {

	start := 1
	if len(fwd) > 1 && (fwd[1] == ' ' || fwd[1] == '\t') {
		start = 2
	}

	// Collect key text.
	keyEnd := start
	for keyEnd < len(fwd) && fwd[keyEnd] != '\n' && fwd[keyEnd] != '\r' {
		if fwd[keyEnd] == ' ' && keyEnd+1 < len(fwd) && fwd[keyEnd+1] == '#' {
			break
		}
		keyEnd++
	}
	key := trimRight(fwd[start:keyEnd])
	// Strip !!type tags from explicit keys (mirrors src/yaml.ts:1455-1461).
	if m := explicitKeyTagRe.FindStringSubmatch(key); m != nil {
		key = m[2]
	}
	// A quoted explicit key is the scalar inside the quotes (`? "k"` is k).
	key = unquoteWholeScalar(key)
	consumed := keyEnd

	// Skip comment at end of key line.
	for consumed < len(fwd) && fwd[consumed] != '\n' && fwd[consumed] != '\r' {
		consumed++
	}
	beforeNewline := consumed

	// Consume newline.
	if consumed < len(fwd) && fwd[consumed] == '\r' {
		consumed++
	}
	if consumed < len(fwd) && fwd[consumed] == '\n' {
		consumed++
	}

	// Check for continuation lines.
	qIndent := 0
	li := pnt.SI
	for li > 0 && lex.Src[li-1] != '\n' && lex.Src[li-1] != '\r' {
		li--
	}
	for li < pnt.SI && lex.Src[li] == ' ' {
		qIndent++
		li++
	}

	// Count extra rows consumed (for multiline / block scalar keys).
	extraRows := 0

	// Handle block scalar keys (| or >). Mirrors src/yaml.ts:1481-1530.
	if bm := explicitKeyBlockScalarRe.FindStringSubmatch(key); bm != nil {
		isFolded := bm[1] == ">"
		chomp := bm[2]
		explicitIndent := 0
		if bm[3] != "" {
			explicitIndent, _ = strconv.Atoi(bm[3])
		}
		// Collect block scalar content lines.
		var blockLines []string
		contentIndent := 0
		for consumed < len(fwd) {
			lineIndent := 0
			for consumed+lineIndent < len(fwd) && fwd[consumed+lineIndent] == ' ' {
				lineIndent++
			}
			afterSpaces := consumed + lineIndent
			// Empty line or line with only spaces.
			if afterSpaces >= len(fwd) || fwd[afterSpaces] == '\n' || fwd[afterSpaces] == '\r' {
				blockLines = append(blockLines, "")
				consumed = afterSpaces
				if consumed < len(fwd) && fwd[consumed] == '\r' {
					consumed++
				}
				if consumed < len(fwd) && fwd[consumed] == '\n' {
					consumed++
				}
				extraRows++
				continue
			}
			// Determine content indent from first non-empty line.
			if contentIndent == 0 {
				if explicitIndent > 0 {
					contentIndent = qIndent + explicitIndent
				} else {
					contentIndent = lineIndent
				}
			}
			// Line must be indented more than ? to be content.
			if lineIndent < contentIndent {
				break
			}
			// Collect line content.
			lineEnd := afterSpaces
			for lineEnd < len(fwd) && fwd[lineEnd] != '\n' && fwd[lineEnd] != '\r' {
				lineEnd++
			}
			blockLines = append(blockLines, fwd[consumed+contentIndent:lineEnd])
			consumed = lineEnd
			if consumed < len(fwd) && fwd[consumed] == '\r' {
				consumed++
			}
			if consumed < len(fwd) && fwd[consumed] == '\n' {
				consumed++
			}
			extraRows++
		}
		// Apply chomping: remove trailing empty lines for non-keep.
		if chomp != "+" {
			for len(blockLines) > 0 && blockLines[len(blockLines)-1] == "" {
				blockLines = blockLines[:len(blockLines)-1]
			}
		}
		if isFolded {
			key = strings.Join(blockLines, " ") + "\n"
		} else {
			key = strings.Join(blockLines, "\n") + "\n"
		}
		if chomp == "-" {
			key = strings.TrimSuffix(key, "\n")
		}
	} else {
		// Scan continuation lines (plain scalar multiline key).
		for consumed < len(fwd) {
			lineIndent := 0
			for consumed+lineIndent < len(fwd) && fwd[consumed+lineIndent] == ' ' {
				lineIndent++
			}
			afterSpaces := consumed + lineIndent
			if afterSpaces < len(fwd) && fwd[afterSpaces] == '#' {
				for afterSpaces < len(fwd) && fwd[afterSpaces] != '\n' && fwd[afterSpaces] != '\r' {
					afterSpaces++
				}
				beforeNewline = afterSpaces
				if afterSpaces < len(fwd) && fwd[afterSpaces] == '\r' {
					afterSpaces++
				}
				if afterSpaces < len(fwd) && fwd[afterSpaces] == '\n' {
					afterSpaces++
				}
				extraRows++
				consumed = afterSpaces
				continue
			}
			if lineIndent > qIndent && afterSpaces < len(fwd) &&
				fwd[afterSpaces] != ':' && fwd[afterSpaces] != '?' && fwd[afterSpaces] != '-' {
				contEnd := afterSpaces
				for contEnd < len(fwd) && fwd[contEnd] != '\n' && fwd[contEnd] != '\r' {
					if fwd[contEnd] == ' ' && contEnd+1 < len(fwd) && fwd[contEnd+1] == '#' {
						break
					}
					contEnd++
				}
				contText := trimRight(fwd[afterSpaces:contEnd])
				if len(contText) > 0 {
					key += " " + contText
				}
				consumed = contEnd
				beforeNewline = consumed
				if consumed < len(fwd) && fwd[consumed] == '\r' {
					consumed++
				}
				if consumed < len(fwd) && fwd[consumed] == '\n' {
					consumed++
				}
				extraRows++
				continue
			}
			break
		}
	}

	// Check if next line starts with ":".
	hasValue := false
	valConsumed := consumed
	ci := consumed
	for ci < len(fwd) && fwd[ci] == ' ' {
		ci++
	}
	if ci < len(fwd) && fwd[ci] == ':' &&
		(ci+1 >= len(fwd) || fwd[ci+1] == ' ' || fwd[ci+1] == '\t' ||
			fwd[ci+1] == '\n' || fwd[ci+1] == '\r') {
		hasValue = true
		valConsumed = ci + 1
		if valConsumed < len(fwd) && (fwd[valConsumed] == ' ' || fwd[valConsumed] == '\t') {
			valConsumed++
		}
	}

	if hasValue {
		pnt.SI += valConsumed
		pnt.RI += 1 + extraRows
		// Characters, not bytes: `consumed` and `valConsumed` are BYTE
		// indices into fwd, so a non-ASCII key set the following indent
		// column too far right by that key's extra bytes.
		indent := utf8.RuneCountInString(fwd[consumed:valConsumed])
		pnt.CI = indent + 1

		// If there's inline content after `: ` on the same line that itself
		// starts a block mapping/sequence (e.g. `: get:\n      summary: ...`),
		// emit CL + IN now so the inner block has a proper indent context.
		// Mirrors src/yaml.ts:1891-1931.
		needsIndent := false
		if valConsumed < len(fwd) {
			nextCh := fwd[valConsumed]
			if nextCh != '\n' && nextCh != '\r' &&
				nextCh != '"' && nextCh != '\'' &&
				nextCh != '[' && nextCh != '{' && nextCh != '!' {
				// Look for ` ' or ':' at end of line — indicates a block-mapping key.
				le := valConsumed
				for le < len(fwd) && fwd[le] != '\n' && fwd[le] != '\r' {
					le++
				}
				for ri := valConsumed; ri < le; ri++ {
					if fwd[ri] == ':' {
						nc := byte(0)
						if ri+1 < len(fwd) {
							nc = fwd[ri+1]
						}
						if nc == ' ' || nc == '\t' || nc == '\n' || nc == '\r' || ri+1 == le {
							needsIndent = true
							break
						}
					}
				}
				// Or sequence indicator `- `.
				if !needsIndent && nextCh == '-' && valConsumed+1 < len(fwd) &&
					(fwd[valConsumed+1] == ' ' || fwd[valConsumed+1] == '\t') {
					needsIndent = true
				}
			}
		}
		if needsIndent {
			clTkn := lex.Token("#CL", CL, 1, ": ")
			inTkn := lex.Token("#IN", IN, indent, "")
			*pendingTokens = append(*pendingTokens, clTkn, inTkn)
		} else {
			*pendingExplicitCL = true
		}
	} else {
		advanceCol(pnt, fwd, beforeNewline)
		clTkn := lex.Token("#CL", CL, 1, ": ")
		vlTkn := lex.Token("#VL", VL, nil, "")
		*pendingTokens = append(*pendingTokens, clTkn, vlTkn)
	}

	// Token source spans the consumed key text (mirrors src/yaml.ts:1587).
	srcEnd := keyEnd
	if hasValue {
		srcEnd = consumed
	}
	tkn := lex.Token("#TX", TX, key, fwd[:srcEnd])
	return tkn
}

// handleDocMarker processes --- and ... document markers.
// handleDocMarker emits a #DS for `---` or #DE for `...`, consuming the
// rest of the marker line (including any trailing comment + newline) so the
// next matcher call lands on the next document's content with no spurious
// #IN. Inline content on the same line as the marker (--- foo) is left
// for subsequent matcher calls.
func handleDocMarker(lex *tabnas.Lex, pnt *tabnas.Point, fwd string,
	DS, DE tabnas.Tin) *tabnas.Token {

	isEnd := fwd[0] == '.'
	pos := 3
	// Skip trailing whitespace after marker.
	for pos < len(fwd) && (fwd[pos] == ' ' || fwd[pos] == '\t') {
		pos++
	}
	hasInline := pos < len(fwd) &&
		fwd[pos] != '\n' && fwd[pos] != '\r' && fwd[pos] != '#'

	if !hasInline {
		// Skip trailing comment, then consume the line terminator.
		for pos < len(fwd) && fwd[pos] != '\n' && fwd[pos] != '\r' {
			pos++
		}
		if pos < len(fwd) && fwd[pos] == '\r' {
			pos++
		}
		if pos < len(fwd) && fwd[pos] == '\n' {
			pos++
			pnt.RI++
		}
		pnt.CI = 1 // column 1 at start of next line
	} else {
		// Characters, not bytes — `pos` is a BYTE index into fwd, and the
		// TypeScript half advances by the same expression over UTF-16
		// indices. Inline content after a `---` marker is where this
		// shows.
		pnt.CI += utf8.RuneCountInString(fwd[:pos])
	}
	var tkn *tabnas.Token
	if isEnd {
		tkn = lex.Token("#DE", DE, tabnas.Undefined, "...")
	} else {
		tkn = lex.Token("#DS", DS, tabnas.Undefined, "---")
	}
	pnt.SI += pos
	return tkn
}

// handleDoubleQuotedString processes YAML double-quoted strings.
func handleDoubleQuotedString(lex *tabnas.Lex, pnt *tabnas.Point, fwd string, ST tabnas.Tin) *tabnas.Token {
	i := 1
	val := ""
	escapedUpTo := 0
	rows := 0
	lastNewlineEnd := 0

	// A `\u` or `\U` escape naming a HIGH surrogate, held back until the
	// next escape either completes the pair or does not. The canonical
	// handler appends UTF-16 code units (`String.fromCharCode`, and
	// `fromCodePoint` gives the same unit for a surrogate value), so
	// `\uD83D\uDE00` is one astral character there and not two escapes.
	// A Go string holds UTF-8, so the pair is combined here before it is
	// written, and a surrogate that never finds its partner becomes
	// U+FFFD, which is what `string(rune(n))` made of every one before.
	pendingHigh := int64(-1)
	flushSurrogate := func() {
		if pendingHigh >= 0 {
			val += "\uFFFD"
			pendingHigh = -1
		}
	}
	pushCodeUnit := func(n int64) {
		switch {
		case 0xD800 <= n && n <= 0xDBFF:
			flushSurrogate()
			pendingHigh = n
		case 0xDC00 <= n && n <= 0xDFFF:
			if pendingHigh >= 0 {
				val += string(rune(0x10000 + (pendingHigh-0xD800)<<10 + (n - 0xDC00)))
				pendingHigh = -1
			} else {
				val += "\uFFFD"
			}
		default:
			flushSurrogate()
			val += string(rune(n))
		}
	}

	for i < len(fwd) && fwd[i] != '"' {
		if fwd[i] == '\\' {
			i++
			if i >= len(fwd) {
				break
			}
			esc := fwd[i]
			// Anything but a code-unit escape or a line continuation
			// separates a high surrogate from the low one it needed.
			if esc != 'x' && esc != 'u' && esc != 'U' && esc != '\n' && esc != '\r' {
				flushSurrogate()
			}
			switch esc {
			case 'n':
				val += "\n"
				i++
				escapedUpTo = len(val)
			case 't':
				val += "\t"
				i++
				escapedUpTo = len(val)
			case 'r':
				val += "\r"
				i++
				escapedUpTo = len(val)
			case '"':
				val += "\""
				i++
				escapedUpTo = len(val)
			case '\\':
				val += "\\"
				i++
				escapedUpTo = len(val)
			case '/':
				val += "/"
				i++
				escapedUpTo = len(val)
			case 'b':
				val += "\b"
				i++
				escapedUpTo = len(val)
			case 'f':
				val += "\f"
				i++
				escapedUpTo = len(val)
			case 'a':
				val += "\x07"
				i++
				escapedUpTo = len(val)
			case 'e':
				val += "\x1b"
				i++
				escapedUpTo = len(val)
			case 'v':
				val += "\v"
				i++
				escapedUpTo = len(val)
			case '0':
				val += "\x00"
				i++
				escapedUpTo = len(val)
			case '\t':
				// Escaped literal tab (mirrors src/yaml.ts:1734): mark it
				// non-trimmable so line folding preserves it.
				val += "\t"
				i++
				escapedUpTo = len(val)
			case ' ':
				val += " "
				i++
				escapedUpTo = len(val)
			case '_':
				val += "\u00a0"
				i++
				escapedUpTo = len(val)
			case 'N':
				val += "\u0085"
				i++
				escapedUpTo = len(val)
			case 'L':
				val += "\u2028"
				i++
				escapedUpTo = len(val)
			case 'P':
				val += "\u2029"
				i++
				escapedUpTo = len(val)
			case 'x', 'u', 'U':
				// `fwd.substring(i + 1, i + 1 + width)`: a FIXED WIDTH
				// window, not a run of hexadecimal digits. It routinely
				// holds the closing quote or the rest of the line, and the
				// canonical handler reads a number out of it with
				// `parseInt`, which takes the longest prefix it can. The
				// width counts UTF-16 code units, and the cursor after it is
				// `width + 1` units along whatever the window held.
				width := 2
				if esc == 'u' {
					width = 4
				} else if esc == 'U' {
					width = 8
				}
				digits, end, splitLow := utf16Window(fwd, i+1, width)
				n := jsParseInt16(digits)
				if esc == 'U' {
					// `String.fromCodePoint` throws on anything that is not
					// a code point, and the document is refused.
					code, ok := jsFromCodePoint(n)
					if !ok {
						return lex.Bad("unexpected")
					}
					pushCodeUnit(code)
				} else {
					// `String.fromCharCode` takes `ToUint16` instead, so it
					// never throws and an unreadable window is a NUL.
					pushCodeUnit(jsToUint16(n))
				}
				i = end
				escapedUpTo = len(val)
				if splitLow >= 0 {
					// The canonical cursor lands one unit into the character
					// the window cut, on its LOW surrogate, and the scan
					// appends that unit as a character of its own. Put
					// through the same pairing an escape goes through, it
					// completes a high surrogate the escape left pending.
					pushCodeUnit(splitLow)
					_, size := utf8.DecodeRuneInString(fwd[i:])
					i += size
				}
			case '\n', '\r':
				// Escaped newline: line continuation.
				if esc == '\r' && i+1 < len(fwd) && fwd[i+1] == '\n' {
					i++
				}
				i++
				rows++
				lastNewlineEnd = i
				for i < len(fwd) && (fwd[i] == ' ' || fwd[i] == '\t') {
					i++
				}
			default:
				val += string(esc)
				i++
			}
		} else if fwd[i] == '\n' || fwd[i] == '\r' {
			flushSurrogate()
			// Flow scalar line folding.
			trimTo := len(val)
			for trimTo > escapedUpTo && (val[trimTo-1] == ' ' || val[trimTo-1] == '\t') {
				trimTo--
			}
			val = val[:trimTo]
			emptyLines := 0
			for i < len(fwd) && (fwd[i] == '\n' || fwd[i] == '\r') {
				if fwd[i] == '\r' {
					i++
				}
				if i < len(fwd) && fwd[i] == '\n' {
					i++
				}
				emptyLines++
				rows++
				lastNewlineEnd = i
				for i < len(fwd) && (fwd[i] == ' ' || fwd[i] == '\t') {
					i++
				}
			}
			if emptyLines > 1 {
				for e := 1; e < emptyLines; e++ {
					val += "\n"
				}
			} else {
				val += " "
			}
		} else {
			// Append the source byte as-is so multi-byte UTF-8 sequences
			// survive intact. `string(byte)` would treat the byte value as
			// a Unicode codepoint and re-encode it as UTF-8, mangling any
			// non-ASCII byte that is part of a multi-byte sequence.
			flushSurrogate()
			val += fwd[i : i+1]
			i++
		}
	}
	flushSurrogate()
	if i < len(fwd) && fwd[i] == '"' {
		i++
	}
	tkn := lex.Token("#ST", ST, val, fwd[:i])
	pnt.SI += i
	pnt.RI += rows
	// Characters, not bytes — `i` and `lastNewlineEnd` are BYTE indices
	// into fwd. See advanceCol.
	//
	// The `rows > 0` branch has no TypeScript counterpart at all: that
	// port does a bare `pnt.cI += i` for a quoted scalar and never
	// touches `rI`, so a multi-line quoted string leaves its row and
	// column wrong there in a way this port does not. Deliberately left
	// as it is — this change is about the UNIT, and closing that
	// structural gap means deciding which port is right about rows,
	// which is a separate question with its own answer.
	if rows > 0 {
		pnt.CI = utf8.RuneCountInString(fwd[lastNewlineEnd:i])
	} else {
		pnt.CI += utf8.RuneCountInString(fwd[:i])
	}
	return tkn
}

// handleSingleQuotedString processes YAML single-quoted strings.
func handleSingleQuotedString(lex *tabnas.Lex, pnt *tabnas.Point, fwd string, ST tabnas.Tin) *tabnas.Token {
	i := 1
	val := ""
	rows := 0
	lastNewlineEnd := 0
	for i < len(fwd) {
		if fwd[i] == '\'' {
			if i+1 < len(fwd) && fwd[i+1] == '\'' {
				val += "'"
				i += 2
			} else {
				i++
				break
			}
		} else if fwd[i] == '\n' || fwd[i] == '\r' {
			// Flow scalar line folding.
			val = strings.TrimRight(val, " \t")
			emptyLines := 0
			for i < len(fwd) && (fwd[i] == '\n' || fwd[i] == '\r') {
				if fwd[i] == '\r' {
					i++
				}
				if i < len(fwd) && fwd[i] == '\n' {
					i++
				}
				emptyLines++
				rows++
				lastNewlineEnd = i
				for i < len(fwd) && (fwd[i] == ' ' || fwd[i] == '\t') {
					i++
				}
			}
			if emptyLines > 1 {
				for e := 1; e < emptyLines; e++ {
					val += "\n"
				}
			} else {
				val += " "
			}
		} else {
			// Append the source byte as-is so multi-byte UTF-8 sequences
			// survive intact. `string(byte)` would treat the byte value as
			// a Unicode codepoint and re-encode it as UTF-8, mangling any
			// non-ASCII byte that is part of a multi-byte sequence.
			val += fwd[i : i+1]
			i++
		}
	}
	tkn := lex.Token("#ST", ST, val, fwd[:i])
	pnt.SI += i
	pnt.RI += rows
	// Characters, not bytes — `i` and `lastNewlineEnd` are BYTE indices
	// into fwd. See advanceCol.
	//
	// The `rows > 0` branch has no TypeScript counterpart at all: that
	// port does a bare `pnt.cI += i` for a quoted scalar and never
	// touches `rI`, so a multi-line quoted string leaves its row and
	// column wrong there in a way this port does not. Deliberately left
	// as it is — this change is about the UNIT, and closing that
	// structural gap means deciding which port is right about rows,
	// which is a separate question with its own answer.
	if rows > 0 {
		pnt.CI = utf8.RuneCountInString(fwd[lastNewlineEnd:i])
	} else {
		pnt.CI += utf8.RuneCountInString(fwd[:i])
	}
	return tkn
}

// handleNumericColon handles plain scalars starting with digits that contain
// colons (e.g. 20:03:20), trailing commas (e.g. 12,), or non-numeric text
// after a space (e.g. "64 characters, hexadecimal.") — captured before the
// number matcher grabs just the leading digits. Mirrors src/yaml.ts:2204-2266.
//
// skipNumberMatch is set to true when trailing text is detected so the
// NumberCheck callback skips the number matcher and lets TextCheck handle
// the scalar (with multiline continuation support).
func handleNumericColon(lex *tabnas.Lex, pnt *tabnas.Point, fwd string, TX tabnas.Tin, skipNumberMatch *bool, flowState *flowScanState) *tabnas.Token {
	flowState.advance(lex.Src, pnt.SI)
	inFlow := flowState.depth > 0

	hasEmbeddedColon := false
	hasTrailingText := false
	hasBlockComma := false
	pi := 1
	for pi < len(fwd) && fwd[pi] != '\n' && fwd[pi] != '\r' {
		if fwd[pi] == ':' && pi+1 < len(fwd) && fwd[pi+1] != ' ' && fwd[pi+1] != '\t' &&
			fwd[pi+1] != '\n' && fwd[pi+1] != '\r' {
			// An embedded colon does not end the scan: the scalar can go on
			// past a space ("09:00 AM"), which only the trailing-text branch
			// reads as one scalar (tabnas/yaml#90).
			hasEmbeddedColon = true
			pi++
			continue
		}
		// A COMMA IS NOT A SEPARATOR IN BLOCK CONTEXT.
		//
		// Flow indicators only indicate inside a flow collection, so in block
		// context `example: 1,2,3` is the plain scalar "1,2,3" — not a number,
		// a separator, and two more numbers.
		//
		// Only a comma at END of line was accepted before, which left `1,2,3`
		// to the number matcher: it took the `1`, the `,` became a structural
		// token, and the parse died on the `2`.
		//
		// The scan CONTINUES rather than breaking, so a scalar that also has a
		// space with text after it ("12, hexadecimal") still reaches the
		// trailing-text branch, which handles spaces and multiline
		// continuation that the token scan cannot.
		if fwd[pi] == ',' {
			if inFlow {
				break
			}
			hasBlockComma = true
			pi++
			continue
		}
		if fwd[pi] == ' ' || fwd[pi] == '\t' {
			// Check if after the space there are non-separator characters,
			// meaning this is a plain scalar like "64 characters, hexadecimal."
			si := pi
			for si < len(fwd) && (fwd[si] == ' ' || fwd[si] == '\t') {
				si++
			}
			if si < len(fwd) && fwd[si] != '\n' && fwd[si] != '\r' &&
				fwd[si] != '#' && fwd[si] != ':' {
				hasTrailingText = true
			}
			break
		}
		pi++
	}
	// TRAILING TEXT FIRST. A scalar can be both ("12, hexadecimal"), and the
	// token scan below stops at the first space, which would truncate it to
	// "12,". TextCheck takes the whole scalar, continuation lines included.
	//
	// In FLOW context too. The canonical takes this branch without asking
	// about flow depth, so `[12 x]` is the one scalar "12 x" there, and a
	// `flowState.depth == 0` guard here left the digits to the number
	// matcher and the text to the grammar: `[12, "x"]`. The comma branch
	// below keeps its flow test, because a comma inside a flow collection
	// IS a separator.
	if hasTrailingText {
		*skipNumberMatch = true
		return nil
	}
	// A `#` inside the scalar's token follows a non-blank character, and YAML
	// starts a comment only at a `#` after white space, so `80#` is the plain
	// scalar "80#". Left to the number matcher it read 80, and the rest of the
	// line went as a comment (tabnas/yaml#95). TextCheck reads it whole, as it
	// reads `foo#bar`, and as for trailing text it takes continuation lines
	// and stops at a mapping colon (`80#:`). The token ends at a blank, or in
	// a flow collection at its `,` `]` `}`, so a `#` past the collection's
	// close keeps the reading it had.
	//
	// Outside a flow collection `[` `]` `{` `}` indicate nothing either, so
	// `5[` and `12[x]` are plain scalars too. The number matcher took the
	// digits and the parse failed on the bracket (tabnas/yaml#99).
	tokenEnd := 0
	for tokenEnd < len(fwd) && fwd[tokenEnd] != ' ' && fwd[tokenEnd] != '\t' &&
		fwd[tokenEnd] != '\n' && fwd[tokenEnd] != '\r' &&
		!(inFlow && (fwd[tokenEnd] == ',' || fwd[tokenEnd] == ']' || fwd[tokenEnd] == '}')) {
		tokenEnd++
	}
	token := fwd[:tokenEnd]
	if strings.IndexByte(token, '#') > 0 || (!inFlow && strings.ContainsAny(token, "[]{}")) {
		*skipNumberMatch = true
		return nil
	}
	if hasBlockComma {
		end := 0
		for end < len(fwd) && fwd[end] != ' ' && fwd[end] != '\t' &&
			fwd[end] != '\n' && fwd[end] != '\r' {
			end++
		}
		text := fwd[:end]
		tkn := lex.Token("#TX", TX, text, text)
		advanceCol(pnt, fwd, end)
		return tkn
	}
	if !hasEmbeddedColon {
		return nil
	}
	// In a flow collection the scalar also ends at its `,` `]` `}`:
	// `[1, 12:30]` holds "12:30", not "12:30,".
	end := 0
	for end < len(fwd) && fwd[end] != ' ' && fwd[end] != '\t' &&
		fwd[end] != '\n' && fwd[end] != '\r' &&
		!(inFlow && (fwd[end] == ',' || fwd[end] == ']' || fwd[end] == '}')) {
		end++
	}
	text := fwd[:end]
	tkn := lex.Token("#TX", TX, text, text)
	advanceCol(pnt, fwd, end)
	return tkn
}

// applyTagConversion applies !!type tag conversion to a raw value.
func applyTagConversion(tag, rawVal string, tagHandles map[string]string) any {
	if _, ok := tagHandles["!!"]; ok {
		return rawVal // Custom tag handle — don't apply built-in conversion.
	}
	switch tag {
	case "str":
		return rawVal
	case "int":
		// yamlTagInt: the core schema's hex and octal forms, then
		// `parseInt(rawVal, 10)`. A tag that cannot read its value still
		// applies: the result is NaN, a number, not the text.
		return yamlTagInt(rawVal)
	case "float":
		// yamlTagFloat: the core schema's infinities and NaN, then
		// `parseFloat(rawVal)`, the same way.
		return yamlTagFloat(rawVal)
	case "bool":
		return rawVal == "true" || rawVal == "True" || rawVal == "TRUE"
	case "null":
		return nil
	default:
		return rawVal
	}
}

var (
	tagHexRe = regexp.MustCompile(`^([-+]?)0x([0-9a-fA-F]+)$`)
	tagOctRe = regexp.MustCompile(`^([-+]?)0o([0-7]+)$`)
	tagInfRe = regexp.MustCompile(`^([-+]?)\.(inf|Inf|INF)$`)
	tagNanRe = regexp.MustCompile(`^\.(nan|NaN|NAN)$`)
)

// yamlTagInt is the value a !!int tag gives its text: YAML's core-schema
// hex (0x1f) and octal (0o17) forms, then a decimal parse. Mirrors
// yamlTagInt in src/yaml.ts.
func yamlTagInt(raw string) any {
	base := 16
	m := tagHexRe.FindStringSubmatch(raw)
	if m == nil {
		base = 8
		m = tagOctRe.FindStringSubmatch(raw)
	}
	if m != nil {
		// Exact, then rounded to the nearest float64 (ties to even) as
		// JavaScript's parseInt does, so a value past 64 bits is not lost.
		n, _ := new(big.Int).SetString(m[2], base)
		v, _ := new(big.Float).SetInt(n).Float64()
		if m[1] == "-" {
			v = -v
		}
		return v
	}
	return jsParseInt(raw)
}

// yamlTagFloat is the value a !!float tag gives its text: the core
// schema's infinities and not-a-number, then a decimal parse. Mirrors
// yamlTagFloat in src/yaml.ts.
func yamlTagFloat(raw string) any {
	if m := tagInfRe.FindStringSubmatch(raw); m != nil {
		if m[1] == "-" {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if tagNanRe.MatchString(raw) {
		return math.NaN()
	}
	return jsParseFloat(raw)
}

// tinToName converts a Tin to its name string.
func tinToName(tin tabnas.Tin) string {
	switch tin {
	case tabnas.TinTX:
		return "#TX"
	case tabnas.TinNR:
		return "#NR"
	case tabnas.TinST:
		return "#ST"
	case tabnas.TinVL:
		return "#VL"
	case tabnas.TinOB:
		return "#OB"
	case tabnas.TinCB:
		return "#CB"
	case tabnas.TinOS:
		return "#OS"
	case tabnas.TinCS:
		return "#CS"
	case tabnas.TinCL:
		return "#CL"
	case tabnas.TinCA:
		return "#CA"
	case tabnas.TinZZ:
		return "#ZZ"
	default:
		return "#UK"
	}
}

// --- BEGIN EMBEDDED yaml-grammar.jsonic ---
const grammarText = `
# YAML Grammar Definition
# Parsed by a standard Tabnas instance and passed to tabnas.grammar()
# Function references (@ prefixed) are resolved against the refs map.
# State handlers (bo/ao/bc/ac) remain wired in code, since they use
# closures over per-parse state (anchors, pendingAnchors, etc.).

{
  # Amend val rule: YAML indent/element-marker handling.
  rule: val: open: {
    alts: [
      # Doc-frame markers between docs mean an empty value here; back up so
      # the stream rule consumes the marker and starts the next document.
      { s: '#DS' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DE' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DR' b: 1 a: '@val-set-null' g: yaml }
      # Indent followed by content: push indent rule.
      { s: '#IN' c: '@val-indent-deeper' p: indent a: '@val-set-in-from-o0' g: yaml }
      # Same indent followed by element marker: list value at map level.
      { s: ['#IN' '#EL'] c: '@val-indent-eq-parent' p: yamlBlockList a: '@val-set-in-from-o0' g: yaml }
      # End of input means empty value.
      { s: '#ZZ' b: 1 a: '@val-set-null' g: yaml }
      # Same or lesser indent after a colon means empty value — backtrack.
      { s: '#IN' b: 1 u: { yamlEmpty: true } g: yaml }
      # This value is a list.
      { s: '#EL' p: yamlBlockList a: '@val-set-el-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: val: close: {
    alts: [
      # Doc-frame markers terminate val; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Indent rule: start for block content at a given indent.
  rule: indent: open: [
    # Key pair => map.
    { s: ['#KEY' '#CL'] p: map b: 2 g: yaml }
    # Element marker => block sequence. yamlBlockList, not jsonic's list:
    # list reads a '[' as its own opening bracket, so a flow sequence as the
    # first item (k:\n  - [1, 2]) became the block sequence itself, or
    # failed at the next item (tabnas/yaml#88).
    { s: '#EL' p: yamlBlockList g: yaml }
    # Flow collection as a block-mapping value on the FOLLOWING line:
    #     required:
    #       [a, b, c]
    # Valid YAML 1.2, and the inline spelling has always worked. The
    # indent rule opens at the deeper indent and meets a
    # flow opener, which only the val rule knows how to read, so hand it back
    # one token so val sees the bracket or brace itself.
    { s: '#OS' p: val b: 1 g: yaml }
    { s: '#OB' p: val b: 1 g: yaml }
    # Plain value after indent (for nested scalars).
    { s: '#KEY' a: '@indent-plain-value' g: yaml }
  ]

  # YAML block list: handles "- " sequences without consuming "[".
  rule: yamlBlockList: open: [
    # Element value is a key-value map: - key: val
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    # Default: push to val for the element's value.
    { p: val g: yaml }
  ]
  rule: yamlBlockList: close: [
    # Doc-frame markers terminate list; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    # Indent followed by element marker: next element at same level.
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: 'yaml,comma' }
    # Same or lesser indent: close list.
    { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    # Element marker at top level (no preceding newline).
    { s: '#EL' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#ZZ' b: 1 g: 'yaml,end' }
  ]

  # Subsequent elements in a yamlBlockList (via rotation).
  rule: yamlBlockElem: open: [
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    { p: val g: yaml }
  ]
  rule: yamlBlockElem: close: [
    # Doc-frame markers terminate elem; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    { s: '#EL' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#ZZ' b: 1 g: 'yaml,end' }
  ]

  # Amend list rule: close on dedent or same-indent non-element.
  rule: list: close: {
    alts: [
      # Doc-frame markers terminate list; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Amend map rule: same-indent indent continues map with pair.
  rule: map: open: {
    alts: [
      { s: '#IN' c: '@o0-eq-in' r: pair g: yaml }
    ]
    inject: { append: false }
  }
  rule: map: close: {
    alts: [
      # Doc-frame markers terminate map; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Amend pair rule: end of input ends pair; dedent closes, same-indent repeats.
  # Also handle YAML flow-mapping shapes Tabnas doesn't have natively:
  # - implicit null values: {a, b: c}  — KEY followed directly by CA or CB
  # - explicit-key marker:  {? k : v}  — leading #QM is consumed
  rule: pair: open: {
    alts: [
      { s: ['#KEY' '#CA'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#KEY' '#CB'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: val u: { pair: true } a: '@qm-pairkey' g: yaml }
      { s: ['#QM' '#KEY' '#CA'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CB'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: '#ZZ' b: 1 g: 'yaml,end' }
    ]
    inject: { append: false }
  }
  rule: pair: close: {
    alts: [
      # Doc-frame markers terminate pair; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-eq-in' r: pair g: 'yaml,comma' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # yamlElemMap: a mapping that starts in a sequence entry, "- key: val",
  # or "[key: val]" in a flow sequence. It only opens the mapping (its
  # before-open action makes the node) and hands the first pair, unread,
  # to yamlElemPair, which reads every pair. A pair rule opening on the
  # open mapping names the member in u.key before it pushes the value's
  # rule, so a consumer that follows rule events has the key before a
  # value that is a collection opens. When the rule that opened the
  # mapping named the first member as well, that consumer was never told
  # the key, and the first member's value opened ahead of it
  # (tabnas/yaml#105).
  rule: yamlElemMap: open: [
    { s: ['#KEY' '#CL'] r: yamlElemPair b: 2 g: yaml }
  ]

  # Every pair in a yamlElemMap, the first included.
  rule: yamlElemPair: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemPair: close: [
    # Doc-frame markers terminate elem-pair; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: 'yaml,comma' }
    { s: '#IN' b: 1 g: 'yaml,close' }
    { s: '#CA' b: 1 g: 'yaml,comma' }
    { s: '#CS' b: 1 g: 'yaml,close' }
    { s: '#CB' b: 1 g: 'yaml,close' }
    { s: '#ZZ' g: 'yaml,end' }
  ]

  # Amend elem rule for YAML sequences ("- key: val" at top level of [ ... ]).
  # Also handle flow-sequence explicit-key entries: [? k : v] is a single-pair
  # map element. Eat the leading #QM, then back up KEY+CL so yamlElemMap
  # consumes them as a normal pair.
  rule: elem: open: {
    alts: [
      { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: elem: close: {
    alts: [
      # Doc-frame markers terminate elem; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: ['#IN' '#EL'] c: '@t0-eq-in' r: elem g: 'yaml,comma' }
      { s: '#IN' c: '@t0-eq-in' b: 1 g: 'yaml,close' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
      { s: '#EL' r: elem g: 'yaml,comma' }
    ]
    inject: { append: false }
  }
}
`

// --- END EMBEDDED yaml-grammar.jsonic ---

// configureGrammarRules installs the YAML grammar (alts from the declarative
// yaml-grammar.jsonic file) and wires state handlers (bo/ao/bc/ac) that need
// closure access to per-parse state.
func configureGrammarRules(j *tabnas.Tabnas, IN, EL tabnas.Tin, KEY []tabnas.Tin,
	CL, ZZ, CA, CS, CB, TX, ST, VL, NR tabnas.Tin,
	anchors map[string]any, pendingAnchors *[]anchorInfo) {

	_ = IN
	_ = EL
	_ = KEY
	_ = CL
	_ = ZZ
	_ = CA
	_ = CS
	_ = CB
	_ = NR
	_ = VL

	// Function refs used by the declarative grammar.
	refs := map[tabnas.FuncRef]any{
		"@val-indent-deeper": tabnas.AltCond(func(r *tabnas.Rule, ctx *tabnas.Context) bool {
			parentIn, hasParentIn := r.K["yamlIn"]
			listIn, hasListIn := r.K["yamlListIn"]
			if hasListIn && listIn != nil {
				if listInVal, ok := toInt(listIn); ok {
					if t0Val, ok := toInt(ctx.T0.Val); ok {
						if t0Val <= listInVal {
							return false
						}
					}
				}
			}
			if !hasParentIn || parentIn == nil {
				return true
			}
			if parentInVal, ok := toInt(parentIn); ok {
				if t0Val, ok := toInt(ctx.T0.Val); ok {
					return t0Val > parentInVal
				}
			}
			return true
		}),
		"@val-indent-eq-parent": tabnas.AltCond(func(r *tabnas.Rule, ctx *tabnas.Context) bool {
			parentIn, hasParentIn := r.K["yamlIn"]
			if !hasParentIn || parentIn == nil {
				return false
			}
			if parentInVal, ok := toInt(parentIn); ok {
				if t0Val, ok := toInt(ctx.T0.Val); ok {
					return t0Val == parentInVal
				}
			}
			return false
		}),
		"@val-set-in-from-o0": tabnas.AltAction(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if v, ok := toInt(r.O0.Val); ok {
				r.EnsureN()["in"] = v
			}
		}),
		"@val-set-null": tabnas.AltAction(func(r *tabnas.Rule, ctx *tabnas.Context) {
			r.Node = nil
		}),
		"@val-set-el-in": tabnas.AltAction(func(r *tabnas.Rule, ctx *tabnas.Context) {
			r.EnsureN()["in"] = r.O0.CI - 1
		}),
		"@indent-plain-value": tabnas.AltAction(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if r.O0.Tin == ST || r.O0.Tin == TX {
				r.Node = r.O0.Val
			} else {
				r.Node = r.O0.Src
			}
		}),
		"@set-map-in": tabnas.AltAction(func(r *tabnas.Rule, ctx *tabnas.Context) {
			r.EnsureK()["yamlMapIn"] = r.N["in"] + 2
		}),
		"@t0-eq-in": tabnas.AltCond(func(r *tabnas.Rule, ctx *tabnas.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				return v == r.N["in"]
			}
			return false
		}),
		"@t0-le-in": tabnas.AltCond(func(r *tabnas.Rule, ctx *tabnas.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				return v <= r.N["in"]
			}
			return false
		}),
		"@t0-lt-in": tabnas.AltCond(func(r *tabnas.Rule, ctx *tabnas.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				return v < r.N["in"]
			}
			return false
		}),
		"@o0-eq-in": tabnas.AltCond(func(r *tabnas.Rule, ctx *tabnas.Context) bool {
			if v, ok := toInt(r.O0.Val); ok {
				return v == r.N["in"]
			}
			return false
		}),
		"@t0-eq-map-in": tabnas.AltCond(func(r *tabnas.Rule, ctx *tabnas.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				if mapIn, ok := toInt(r.K["yamlMapIn"]); ok {
					return v == mapIn
				}
			}
			return false
		}),
		"@elem-key": tabnas.AltAction(func(r *tabnas.Rule, ctx *tabnas.Context) {
			r.EnsureU()["key"] = extractKey(r.O0, anchors)
		}),
		"@implicit-null-pair": tabnas.AltAction(func(r *tabnas.Rule, _ *tabnas.Context) {
			key := extractKey(r.O0, anchors)
			r.EnsureU()["key"] = key
			setNodeKey(r.Node, formatKey(key), nil)
		}),
		"@qm-pairkey": tabnas.AltAction(func(r *tabnas.Rule, _ *tabnas.Context) {
			r.EnsureU()["key"] = extractKey(r.O1, anchors)
		}),
		"@qm-implicit-null-pair": tabnas.AltAction(func(r *tabnas.Rule, _ *tabnas.Context) {
			key := extractKey(r.O1, anchors)
			r.EnsureU()["key"] = key
			setNodeKey(r.Node, formatKey(key), nil)
		}),
	}

	// Parse the embedded grammar text and build a GrammarSpec.
	parser := jsonic.Make()
	parsed, err := parser.Parse(grammarText)
	if err != nil {
		panic(fmt.Sprintf("yaml: failed to parse grammar text: %v", err))
	}
	parsedMap, ok := asStringMap(parsed)
	if !ok {
		panic(fmt.Sprintf("yaml: grammar text did not parse to a map: %T", parsed))
	}
	gs := &tabnas.GrammarSpec{Ref: refs}
	if ruleMap, ok := asStringMap(parsedMap["rule"]); ok {
		gs.Rule = mapToGrammarRules(ruleMap)
	}
	if err := j.Grammar(gs); err != nil {
		panic(fmt.Sprintf("yaml: failed to apply grammar: %v", err))
	}

	// ===== State handlers (bo/ao/bc/ac) — kept in code for closure capture =====

	// val rule: claim pending anchors (ao), handle empty (bc), resolve
	// aliases and record anchors (ac), follow replacement chain (bc).
	j.Rule("val", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddAO(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if len(*pendingAnchors) > 0 {
				anchorsCopy := make([]anchorInfo, len(*pendingAnchors))
				copy(anchorsCopy, *pendingAnchors)
				r.EnsureU()["yamlAnchors"] = anchorsCopy
				r.EnsureU()["yamlAnchorOpenNode"] = r.Node
				*pendingAnchors = (*pendingAnchors)[:0]
			}
		})
		rs.AddBC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			// A child that rotated (an implicit list) left its value at
			// the end of the chain; see childNode.
			if child := r.Child; child != nil && child != tabnas.NoRule {
				if final := chainEnd(child); final != child && !tabnas.IsUndefined(final.Node) {
					r.Node = final.Node
				}
			}
		})
		rs.AddBC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if _, ok := r.U["yamlEmpty"]; ok {
				r.Node = tabnas.Undefined
			}
		})
		rs.AddAC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if m, ok := r.Node.(map[string]any); ok {
				if alias, ok := m["__yamlAlias"].(string); ok {
					// `rule.node = anchors[name]`, with no test that
					// the anchor is there: an alias to a name the
					// document never anchored reads as the absent
					// value, which is null. Keeping the marker instead
					// publishes this plugin's own bookkeeping as the
					// parse result, so `b: *nope` handed a caller
					// `{"__yamlAlias":"nope"}`, a map the document does
					// not contain and no other runtime produces.
					val, exists := anchors[alias]
					switch v := val.(type) {
					case *tabnas.OrderedMap, tabnas.OrderedMap, map[string]any, []any:
						r.Node = deepCopy(v)
					default:
						if exists {
							r.Node = val
						} else {
							// `undefined`, which the engine drops from a
							// list and reads as null in a map, rather
							// than a Go nil, which a list would keep.
							r.Node = tabnas.Undefined
						}
					}
				}
			}
			if anchorList, ok := r.U["yamlAnchors"]; ok {
				anchorsSlice, ok := anchorList.([]anchorInfo)
				if ok {
					for _, anchor := range anchorsSlice {
						if anchor.inline {
							openNode := r.U["yamlAnchorOpenNode"]
							if openNode != nil {
								switch openNode.(type) {
								case *tabnas.OrderedMap, tabnas.OrderedMap, map[string]any, []any:
									continue
								}
							}
						}
						val := r.Node
						switch v := val.(type) {
						case *tabnas.OrderedMap, tabnas.OrderedMap, map[string]any, []any:
							val = deepCopy(v)
						}
						anchors[anchor.name] = val
					}
				}
			}
		})
	})

	j.Rule("indent", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddBC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if node := childNode(r); !tabnas.IsUndefined(node) {
				r.Node = node
			}
		})
	})

	// pushBack keeps the replaced-rule (rotation) chain in sync so the
	// parent rule always sees the latest slice through r.Parent.Child.Node.
	// Go's append may reallocate the backing array, so a rotated element
	// bc that appends to its own r.Node would leave the original
	// yamlBlockList head's Node (which the parent val reads as r.Child.Node)
	// stale — only the first element would survive. Unlike JS arrays, which
	// alias by reference, Go needs this explicit write-back. Mirrors the
	// jsonic Go grammar's CSV pushBack.
	pushBack := func(r *tabnas.Rule) {
		if r.Parent != nil && r.Parent != tabnas.NoRule &&
			r.Parent.Child != nil && r.Parent.Child != tabnas.NoRule {
			r.Parent.Child.Node = r.Node
		}
	}

	j.Rule("yamlBlockList", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddBO(func(r *tabnas.Rule, ctx *tabnas.Context) {
			r.Node = make([]any, 0)
			r.EnsureK()["yamlBlockArr"] = r.Node
			r.EnsureK()["yamlListIn"] = r.N["in"]
		})
		rs.AddBC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			val := childNode(r)
			if tabnas.IsUndefined(val) {
				val = nil
			}
			if arr, ok := r.K["yamlBlockArr"].([]any); ok {
				arr = append(arr, val)
				r.EnsureK()["yamlBlockArr"] = arr
				r.Node = arr
				pushBack(r)
			}
		})
	})

	j.Rule("yamlBlockElem", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddBO(func(r *tabnas.Rule, ctx *tabnas.Context) {
			r.Node = r.K["yamlBlockArr"]
		})
		rs.AddBC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			val := childNode(r)
			if tabnas.IsUndefined(val) {
				val = nil
			}
			if arr, ok := r.K["yamlBlockArr"].([]any); ok {
				arr = append(arr, val)
				r.EnsureK()["yamlBlockArr"] = arr
				r.Node = arr
				pushBack(r)
			}
		})
	})

	j.Rule("list", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddBO(func(r *tabnas.Rule, ctx *tabnas.Context) {
			r.EnsureK()["yamlListIn"] = r.N["in"]
			// OWN the node-append phase for an indented YAML block sequence.
			//
			// jsonic's @array$ only allocates the list's array on the flow
			// `[` (#OS) open alt; @list-bo only allocates for a top-level
			// implicit comma/space list (prev.u.implist). A block sequence
			// nested deeper than its map key reaches `list` a third way —
			// the indent rule's #EL alt does `p: list` with no #OS — so
			// neither builder runs and r.Node stays the inherited parent
			// container (the map). jsonic's @elem-bc/replace then pushes onto
			// that map: in Go the type assertion fails and every element is
			// silently dropped. Allocate the array here so the push lands in
			// a real list. Only the indent path needs this: a flow `[...]`
			// list is pushed by `val` (parent=val) and gets its array from
			// @array$, so it is left untouched.
			if r.Parent != nil && r.Parent != tabnas.NoRule &&
				r.Parent.Name == "indent" {
				r.Node = []any{}
			}
		})
	})

	j.Rule("map", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddBO(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if _, ok := r.N["in"]; !ok {
				r.EnsureN()["in"] = 0
			}
			r.EnsureK()["yamlIn"] = r.N["in"]
		})
		rs.AddAC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			applyMergeKeys(r.Node)
		})
	})

	// yamlElemMap only makes the map: its open alternate hands every pair,
	// the first included, to yamlElemPair (see yaml-grammar.jsonic), so it
	// never reaches a close phase and stores nothing itself.
	j.Rule("yamlElemMap", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddBO(func(r *tabnas.Rule, ctx *tabnas.Context) {
			// Build inline/flow element mappings as insertion-ordered maps
			// so they preserve source key order, matching the block-mapping
			// path (jsonic core now yields *OrderedMap) and the TS engine.
			r.Node = tabnas.NewOrderedMap()
		})
	})

	// yamlElemPair stores each pair into the shared map.
	j.Rule("yamlElemPair", func(rs *tabnas.RuleSpec, _ *tabnas.Parser) {
		rs.AddBC(func(r *tabnas.Rule, ctx *tabnas.Context) {
			if key := r.U["key"]; key != nil {
				if m, ok := r.Node.(*tabnas.OrderedMap); ok {
					val := childNode(r)
					if tabnas.IsUndefined(val) {
						val = nil
					}
					m.Set(formatKey(key), val)
				}
			}
		})
	})
}

// mapToGrammarRules converts a parsed rule map into typed GrammarRuleSpec map.
func mapToGrammarRules(ruleMap map[string]any) map[string]*tabnas.GrammarRuleSpec {
	rules := make(map[string]*tabnas.GrammarRuleSpec, len(ruleMap))
	for name, v := range ruleMap {
		rm, ok := asStringMap(v)
		if !ok {
			continue
		}
		spec := &tabnas.GrammarRuleSpec{}
		if open, ok := rm["open"]; ok {
			spec.Open = parseGrammarAltsOrSpec(open)
		}
		if close, ok := rm["close"]; ok {
			spec.Close = parseGrammarAltsOrSpec(close)
		}
		rules[name] = spec
	}
	return rules
}

// parseGrammarAltsOrSpec converts parsed JSON-like values into either
// []*GrammarAltSpec or *GrammarAltListSpec depending on shape.
func parseGrammarAltsOrSpec(v any) any {
	switch v.(type) {
	case []any:
		return mapsToAlts(v.([]any))
	}
	val, ok := asStringMap(v)
	if !ok {
		return nil
	}
	alts, _ := val["alts"].([]any)
	spec := &tabnas.GrammarAltListSpec{Alts: mapsToAlts(alts)}
	if inj, ok := asStringMap(val["inject"]); ok {
		spec.Inject = &tabnas.GrammarInjectSpec{}
		if app, ok := inj["append"].(bool); ok {
			spec.Inject.Append = app
		}
		if del, ok := inj["delete"].([]any); ok {
			for _, d := range del {
				if n, ok := toInt(d); ok {
					spec.Inject.Delete = append(spec.Inject.Delete, n)
				}
			}
		}
		if mv, ok := inj["move"].([]any); ok {
			for _, m := range mv {
				if n, ok := toInt(m); ok {
					spec.Inject.Move = append(spec.Inject.Move, n)
				}
			}
		}
	}
	return spec
}

// mapsToAlts converts an []any of parsed alt maps into []*GrammarAltSpec.
func mapsToAlts(list []any) []*tabnas.GrammarAltSpec {
	out := make([]*tabnas.GrammarAltSpec, 0, len(list))
	for _, item := range list {
		m, ok := asStringMap(item)
		if !ok {
			continue
		}
		a := &tabnas.GrammarAltSpec{}
		if s, ok := m["s"]; ok {
			a.S = normalizeS(s)
		}
		if p, ok := m["p"].(string); ok {
			a.P = p
		}
		if r, ok := m["r"].(string); ok {
			a.R = r
		}
		if b, ok := toInt(m["b"]); ok {
			a.B = b
		}
		if c, ok := m["c"].(string); ok {
			a.C = c
		}
		if a2, ok := m["a"].(string); ok {
			a.A = a2
		}
		if g, ok := m["g"].(string); ok {
			a.G = g
		}
		if u, ok := asStringMap(m["u"]); ok {
			a.U = u
		}
		if k, ok := asStringMap(m["k"]); ok {
			a.K = k
		}
		if n, ok := asStringMap(m["n"]); ok {
			a.N = make(map[string]int, len(n))
			for nk, nv := range n {
				if ni, ok := toInt(nv); ok {
					a.N[nk] = ni
				}
			}
		}
		out = append(out, a)
	}
	return out
}

// normalizeS converts a parsed S field to a form accepted by resolveTokenField
// (string or []string). Parsed YAML-ish text yields []any for arrays; convert
// those into []string so token-name resolution runs.
func normalizeS(s any) any {
	switch v := s.(type) {
	case string:
		return v
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return s
}

// toInt converts an any value to int.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}
