package tabnasyaml

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	jsonic "github.com/tabnas/jsonic/go"
)

// Version is the module version, injected at release by `make publish-go`.
const Version = "0.4.0"

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
var (
	structTagPrefixRe  = regexp.MustCompile(`^!!(seq|map|omap|set|pairs|binary|ordered|python/\S*)`)
	yamlTagDirectiveRe = regexp.MustCompile(`^%TAG\s+(\S+)\s+(\S+)`)
	// !!type tag prefix on an explicit (`? `) key (mirrors src/yaml.ts:1457).
	explicitKeyTagRe = regexp.MustCompile(`^!!(\w+)\s+(.*)$`)
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
	inDoubleQuote bool
	inSingleQuote bool
}

// reset clears flow scan state at the start of a new parse.
func (s *flowScanState) reset() {
	s.depth = 0
	s.pos = 0
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
// flow-collection depth and quote state. If target < pos the cache is
// reset (cleanSource may have replaced lex.Src and shortened the cursor).
func (s *flowScanState) advance(src string, target int) {
	if target < s.pos {
		s.depth = 0
		s.pos = 0
		s.inDoubleQuote = false
		s.inSingleQuote = false
	}
	for fi := s.pos; fi < target; fi++ {
		fc := src[fi]
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
				if fi+1 < target && src[fi+1] == '\'' {
					fi++ // escaped ''
				} else {
					s.inSingleQuote = false
				}
			}
			continue
		}
		switch fc {
		case '{', '[':
			s.depth++
		case '}', ']':
			if s.depth > 0 {
				s.depth--
			}
		case '"':
			s.inDoubleQuote = true
		case '\'':
			// Apostrophes preceded by a word char are not quote openers
			// (matches src/yaml.ts:941-947).
			if fi > 0 {
				pc := src[fi-1]
				if (pc >= 'A' && pc <= 'Z') || (pc >= 'a' && pc <= 'z') || (pc >= '0' && pc <= '9') {
					continue
				}
			}
			s.inSingleQuote = true
		}
	}
	s.pos = target
}

// Parse parses a YAML string and returns the resulting Go value.
// The returned value can be:
//   - *jsonic.OrderedMap for mappings (insertion-ordered: keys are exposed
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
	defaultParser *jsonic.Jsonic
)

func Parse(src string) (any, error) {
	defaultOnce.Do(func() { defaultParser = MakeJsonic() })
	return defaultParser.Parse(src)
}

// MakeJsonic creates a jsonic instance configured for YAML parsing.
// If a YamlOptions is passed, its fields are propagated to the plugin.
func MakeJsonic(opts ...YamlOptions) *jsonic.Jsonic {
	yo := YamlOptions{}
	if len(opts) > 0 {
		yo = opts[0]
	}
	j := jsonic.Make(jsonic.Options{
		String: &jsonic.StringOptions{
			Chars: "`", // Remove single quote from string chars; we handle YAML strings in yamlMatcher
		},
		Lex: &jsonic.LexOptions{
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
	"-.inf": math.Inf(-1), "-.Inf": math.Inf(-1), "-.INF": math.Inf(-1),
	".nan": math.NaN(), ".NaN": math.NaN(), ".NAN": math.NaN(),
}

// isYamlValue checks if text is a YAML value keyword and returns the value.
func isYamlValue(text string) (any, bool) {
	val, ok := yamlValueMap[text]
	return val, ok
}

// parseYamlNumber attempts to parse text as a YAML number.
// Returns the number and true if successful, or 0 and false if not a number.
func parseYamlNumber(text string) (float64, bool) {
	if text == "" {
		return 0, false
	}
	// Try standard float parsing
	num, err := strconv.ParseFloat(text, 64)
	if err == nil {
		return num, true
	}
	// Try integer formats: hex, octal, binary
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
		if n, err := strconv.ParseInt(text[2:], 16, 64); err == nil {
			return float64(n), true
		}
	}
	if strings.HasPrefix(text, "0o") || strings.HasPrefix(text, "0O") {
		if n, err := strconv.ParseInt(text[2:], 8, 64); err == nil {
			return float64(n), true
		}
	}
	if strings.HasPrefix(text, "0b") || strings.HasPrefix(text, "0B") {
		if n, err := strconv.ParseInt(text[2:], 2, 64); err == nil {
			return float64(n), true
		}
	}
	// Negative hex/oct/bin
	if len(text) > 1 && text[0] == '-' {
		if num, ok := parseYamlNumber(text[1:]); ok {
			return -num, true
		}
	}
	if len(text) > 1 && text[0] == '+' {
		return parseYamlNumber(text[1:])
	}
	return 0, false
}

// deepCopy performs a structural deep copy of a value, preserving both the
// concrete container type and, for insertion-ordered *jsonic.OrderedMap
// objects, their source key order. Anchors deep-copy their value so a later
// merge or mutation of one alias cannot leak into another. (A JSON
// round-trip would flatten *OrderedMap back to an alphabetical
// map[string]any, silently dropping source order — hence the manual walk.)
func deepCopy(v any) any {
	switch val := v.(type) {
	case *jsonic.OrderedMap:
		out := jsonic.NewOrderedMap()
		out.Sorted = val.Sorted
		for _, k := range val.Keys {
			out.Set(k, deepCopy(val.Vals[k]))
		}
		return out
	case jsonic.OrderedMap:
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
// insertion-ordered *jsonic.OrderedMap instead of a bare map[string]any.
// Grammar text is parsed with the stock jsonic parser, so the values this
// module reads back from that parse (the embedded grammar spec) are
// *OrderedMap. This helper unwraps either shape to the underlying map for
// value-only access; callers that reconstruct typed specs from it do not
// depend on key order.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case *jsonic.OrderedMap:
		return m.Vals, true
	case jsonic.OrderedMap:
		return m.Vals, true
	case map[string]any:
		return m, true
	}
	return nil, false
}

// orderedEntries returns the ordered (key, value) entries of a parsed
// object value. Block maps are *jsonic.OrderedMap (source order); flow /
// inline element maps built by this plugin are plain map[string]any (whose
// iteration order is undefined, so keys are visited in Go's map order).
func orderedEntries(v any) ([]string, map[string]any) {
	switch m := v.(type) {
	case *jsonic.OrderedMap:
		return m.Keys, m.Vals
	case jsonic.OrderedMap:
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
	case *jsonic.OrderedMap:
		m.Set(key, val)
	case map[string]any:
		m[key] = val
	}
}

// applyMergeKeys resolves the YAML `<<` merge key on a parsed mapping node
// in place, preserving source key order. Explicit keys win over merged
// keys; the merged (non-duplicate) keys are spliced in at the position the
// `<<` key occupied. The node may be an insertion-ordered *OrderedMap
// (block mapping) — the common case — or, defensively, a plain
// map[string]any (order-free flow mapping).
func applyMergeKeys(node any) {
	om, ok := node.(*jsonic.OrderedMap)
	if !ok {
		// Plain map (flow mapping): no order to preserve; merge by value.
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
	// Rebuild key order, expanding `<<` into its (non-duplicate) merged
	// keys at its original position. Explicit keys keep their positions.
	explicit := make(map[string]bool, len(om.Keys))
	for _, k := range om.Keys {
		if k != "<<" {
			explicit[k] = true
		}
	}
	newKeys := make([]string, 0, len(om.Keys))
	seen := make(map[string]bool, len(om.Keys))
	for _, k := range om.Keys {
		if k == "<<" {
			for _, mm := range mergeSources(mergeVal) {
				mkeys, mvals := orderedEntries(mm)
				for _, mk := range mkeys {
					if explicit[mk] || seen[mk] {
						continue
					}
					if _, present := om.Vals[mk]; !present {
						om.Vals[mk] = mvals[mk]
					}
					newKeys = append(newKeys, mk)
					seen[mk] = true
				}
			}
			continue
		}
		if seen[k] {
			continue
		}
		newKeys = append(newKeys, k)
		seen[k] = true
	}
	delete(om.Vals, "<<")
	om.Keys = newKeys
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
func extractKey(o0 *jsonic.Token, anchors map[string]any) any {
	if o0.Tin == jsonic.TinVL {
		if m, ok := o0.Val.(map[string]any); ok {
			if alias, ok := m["__yamlAlias"].(string); ok {
				if val, exists := anchors[alias]; exists {
					return val
				}
				return "*" + alias
			}
		}
	}
	if o0.Tin == jsonic.TinST || o0.Tin == jsonic.TinTX {
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

// trimRight removes trailing whitespace from a string.
func trimRight(s string) string {
	return strings.TrimRight(s, " \t")
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
func Yaml(j *jsonic.Jsonic, opts map[string]any) error {
	// Guard against re-entry during SetOptions's plugin re-application.
	// Without this, Grammar()/Rule() calls would be duplicated.
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

	KEY := []jsonic.Tin{TX, NR, ST, VL}

	// Shared state for the plugin instance.
	anchors := make(map[string]any)
	var pendingAnchors []anchorInfo
	pendingExplicitCL := false
	var pendingTokens []*jsonic.Token
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
	textCheck := func(lex *jsonic.Lex) *jsonic.LexCheckResult {
		pnt := lex.Cursor()
		src := lex.Src
		fwd := src[pnt.SI:]
		if len(fwd) == 0 {
			return nil
		}
		ch := fwd[0]

		// Block scalar: | or >
		if ch == '|' || ch == '>' {
			return handleBlockScalar(lex, pnt, src, fwd, ch)
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

	yamlMatcher := func(lex *jsonic.Lex, _ *jsonic.Rule) *jsonic.Token {
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
			if strings.TrimSpace(src) == "" || strings.TrimSpace(stripped) == "" {
				pnt.Len = 0
				tkn := lex.Token("#VL", VL, nil, "")
				pnt.SI = 0
				return tkn
			}
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
					var tkn *jsonic.Token
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
					pnt.SI += nameEnd
					pnt.CI += nameEnd
					return tkn
				}
				// Unknown alias — return as marker.
				tkn := lex.Token("#VL", VL, map[string]any{"__yamlAlias": aliasName}, fwd[:nameEnd])
				pnt.SI += nameEnd
				pnt.CI += nameEnd
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
						raw := strings.TrimRight(peek[:ei], " \t")
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
				pnt.SI += skip
				pnt.CI += skip

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
					pnt.SI += valEnd
					pnt.CI += valEnd
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
				pnt.SI += tagEnd
				pnt.CI += tagEnd
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
				pnt.SI += skip
				pnt.CI += skip
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
					tkn := lex.Token("#QM", QM, jsonic.Undefined, "?")
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
			if (pnt.SI == 0 || lex.Src[pnt.SI-1] == '\n' || lex.Src[pnt.SI-1] == '\r') &&
				isDocMarker(fwd, 0) {
				return handleDocMarker(lex, pnt, fwd, DS, DE)
			}

			// Directive line at column 0: emit #DR token. The stream rule
			// applies %TAG handles via the @apply-directive action.
			if fwd[0] == '%' && (pnt.SI == 0 || lex.Src[pnt.SI-1] == '\n' || lex.Src[pnt.SI-1] == '\r') {
				pos := 0
				for pos < len(fwd) && fwd[pos] != '\n' && fwd[pos] != '\r' {
					pos++
				}
				directiveSrc := fwd[:pos]
				pnt.SI += pos
				pnt.CI += pos
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
				pnt.SI += valEnd
				pnt.CI += valEnd
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
			if fwd[0] >= '0' && fwd[0] <= '9' {
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
					hashAt := -1
					for li := lineStart; li <= prevI; li++ {
						if lex.Src[li] == '#' &&
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
					for pos < len(fwd) && (fwd[pos] == '\n' || fwd[pos] == '\r' ||
						fwd[pos] == ' ' || fwd[pos] == '\t') {
						if fwd[pos] == '\r' && pos+1 < len(fwd) && fwd[pos+1] == '\n' {
							pos += 2
							rows++
							continue
						}
						if fwd[pos] == '\n' || fwd[pos] == '\r' {
							rows++
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
					pnt.CI = 0
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
					tkn := lex.Token("#ZZ", ZZ, jsonic.Undefined, "")
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
				// string at column 0 — there's no block to indent into.
				if spaces == 0 &&
					(fwd[pos] == '{' || fwd[pos] == '[' ||
						fwd[pos] == '"' || fwd[pos] == '\'') {
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

	// Register the YAML matcher via SetOptions (must come before cfg mutations
	// below, as SetOptions rebuilds parts of the config). Also configure
	// `stream` as the start rule (replaces `val`) so the stream rule below
	// consumes #DS / #DE / #DR doc-frame tokens.
	j.SetOptions(jsonic.Options{
		Lex: &jsonic.LexOptions{Match: map[string]*jsonic.MatchSpec{
			"yaml": {Order: 500000, Make: func(_ *jsonic.LexConfig, _ *jsonic.Options) jsonic.LexMatcher {
				return yamlMatcher
			}},
		}},
		Rule: &jsonic.RuleOptions{Start: "stream"},
	})

	// Remove colon as a fixed token — YAML uses ": " (colon-space).
	delete(cfg.FixedTokens, ":")
	cfg.SortFixedTokens()

	// Add colon as an ender char so text tokens stop at ":".
	if cfg.EnderChars == nil {
		cfg.EnderChars = make(map[rune]bool)
	}
	cfg.EnderChars[':'] = true

	// Skip number matching when the yamlMatcher detected trailing text
	// after a digit-starting value (e.g. "64 characters, hexadecimal.").
	cfg.NumberCheck = func(lex *jsonic.Lex) *jsonic.LexCheckResult {
		if skipNumberMatch {
			skipNumberMatch = false
			return &jsonic.LexCheckResult{Done: true}
		}
		return nil
	}

	cfg.TextCheck = textCheck

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
	pushChildDoc := func(r *jsonic.Rule) {
		if r.Child != nil && r.Child != jsonic.NoRule && !jsonic.IsUndefined(r.Child.Node) {
			streamDocs = append(streamDocs, r.Child.Node)
		} else {
			streamDocs = append(streamDocs, nil)
		}
	}
	accumChildDoc := func(r *jsonic.Rule, _ *jsonic.Context) {
		pushChildDoc(r)
		// The matched close-phase token tells us if this doc ended with `...`.
		ended := r.C0 != nil && r.C0.Tin == DE
		flushCurMeta(ended)
	}
	finalizeStream := func(r *jsonic.Rule, ctx *jsonic.Context) {
		if r.Child != nil && r.Child != jsonic.NoRule && !jsonic.IsUndefined(r.Child.Node) {
			streamDocs = append(streamDocs, r.Child.Node)
			flushCurMeta(false)
		} else if streamCurMeta != nil {
			// The final document was explicitly opened (a `---` / `%TAG`
			// directive started a doc, recorded in streamCurMeta) but its
			// value coalesced to undefined — a bare `---` at end-of-stream,
			// or a trailing empty doc in `---\n---\n---`. jsonic's val-close
			// treats a deliberate @val-set-null as undefined, so the empty
			// doc's null is lost here; restore it the same way accumChildDoc
			// / pushEmptyDoc force a null for the non-final empty docs.
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
	applyDirective := func(r *jsonic.Rule, _ *jsonic.Context) {
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
	markExplicit := func(_ *jsonic.Rule, _ *jsonic.Context) {
		ensureCurMeta()
		streamCurMeta.Explicit = true
	}
	pushEmptyDoc := func(_ *jsonic.Rule, _ *jsonic.Context) {
		streamDocs = append(streamDocs, nil)
		flushCurMeta(true)
	}

	j.Rule("stream", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddOpen(
			// Consume directive line; rotate to stream to look for the next token.
			&jsonic.AltSpec{S: [][]jsonic.Tin{{DR}}, A: applyDirective, R: "stream", G: "yaml"},
			// Explicit doc start: push val for the document content.
			&jsonic.AltSpec{S: [][]jsonic.Tin{{DS}}, A: markExplicit, P: "val", G: "yaml"},
			// ... before any content: count as empty doc, look for more.
			&jsonic.AltSpec{S: [][]jsonic.Tin{{DE}}, A: pushEmptyDoc, R: "stream", G: "yaml"},
			// Empty source: end immediately.
			&jsonic.AltSpec{S: [][]jsonic.Tin{{ZZ}}, B: 1, G: "yaml"},
			// Implicit first doc.
			&jsonic.AltSpec{P: "val", G: "yaml"},
		)
		rs.AddClose(
			// End of input: accumulate last doc, finalize result shape.
			&jsonic.AltSpec{S: [][]jsonic.Tin{{ZZ}}, A: finalizeStream, G: "yaml"},
			// Directive between docs.
			&jsonic.AltSpec{S: [][]jsonic.Tin{{DR}},
				A: func(r *jsonic.Rule, ctx *jsonic.Context) {
					accumChildDoc(r, ctx)
					applyDirective(r, ctx)
				},
				R: "stream", G: "yaml"},
			// ... terminator: accumulate, look for next doc.
			&jsonic.AltSpec{S: [][]jsonic.Tin{{DE}}, A: accumChildDoc, R: "stream", G: "yaml"},
			// --- start of next doc (back up so stream.open consumes it).
			&jsonic.AltSpec{S: [][]jsonic.Tin{{DS}}, B: 1, A: accumChildDoc, R: "stream", G: "yaml"},
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
func handleBlockScalar(lex *jsonic.Lex, pnt *jsonic.Point, src, fwd string, ch byte) *jsonic.LexCheckResult {
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
	li := pnt.SI - 1
	// Block indicator at the very start of the source: there is no
	// containing line (TS reads src[-1] as undefined; Go must not index
	// out of range). containingIndent stays 0 and isDocStart false.
	if li < 0 {
		li = 0
	}
	for li > 0 && src[li-1] != '\n' && src[li-1] != '\r' {
		li--
	}
	lineStart := li
	for li < pnt.SI && src[li] == ' ' {
		containingIndent++
		li++
	}
	if lineStart+2 < len(src) && src[lineStart] == '-' && src[lineStart+1] == '-' && src[lineStart+2] == '-' {
		isDocStart = true
	}

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

	if blockIndent <= containingIndent && !isDocStart && idx < len(fwd) {
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
		tkn := lex.Token("#TX", jsonic.TinTX, val, fwd[:idx])
		pnt.SI += idx
		pnt.RI++
		pnt.CI = 0
		return &jsonic.LexCheckResult{Done: true, Token: tkn}
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
		if lineIndent == 0 && isDocMarker(fwd, pos) {
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
		isNextDocMarker := nextLineIndent == 0 && isDocMarker(fwd, ni)
		if !isNextDocMarker {
			endPos = lastNewlinePos
			endRows = rows - 1
		}
	}

	tkn := lex.Token("#TX", jsonic.TinTX, val, fwd[:endPos])
	pnt.SI += endPos
	pnt.RI += endRows
	pnt.CI = 0
	return &jsonic.LexCheckResult{Done: true, Token: tkn}
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
func handleTagInTextCheck(lex *jsonic.Lex, pnt *jsonic.Point, fwd string, tagHandles map[string]string) *jsonic.LexCheckResult {
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
		rawVal = fwd[valStart+1 : valEnd-1]
	} else {
		for valEnd < len(fwd) && fwd[valEnd] != '\n' && fwd[valEnd] != '\r' {
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
	tknTin := jsonic.TinTX
	switch result.(type) {
	case float64:
		tknTin = jsonic.TinNR
	case bool, nil:
		tknTin = jsonic.TinVL
	}
	if result == nil {
		tknTin = jsonic.TinVL
	}

	tkn := lex.Token(tinToName(tknTin), tknTin, result, fwd[:valEnd])
	pnt.SI += valEnd
	pnt.CI += valEnd
	return &jsonic.LexCheckResult{Done: true, Token: tkn}
}

// handlePlainScalar processes YAML plain scalar values with multiline continuation.
func handlePlainScalar(lex *jsonic.Lex, pnt *jsonic.Point, src, fwd string, flowState *flowScanState) *jsonic.LexCheckResult {
	// Detect flow context (incremental scan, see flowScanState).
	flowState.advance(src, pnt.SI)
	inFlowCtx := flowState.depth > 0

	// Find current line indent.
	lineStartPos := pnt.SI
	for lineStartPos > 0 && src[lineStartPos-1] != '\n' && src[lineStartPos-1] != '\r' {
		lineStartPos--
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
		tkn := lex.Token("#VL", jsonic.TinVL, val, text)
		pnt.SI += len(text)
		pnt.CI += len(text)
		return &jsonic.LexCheckResult{Done: true, Token: tkn}
	}

	// Check if it's a number.
	if num, ok := parseYamlNumber(text); ok {
		tkn := lex.Token("#NR", jsonic.TinNR, num, text)
		pnt.SI += len(text)
		pnt.CI += len(text)
		return &jsonic.LexCheckResult{Done: true, Token: tkn}
	}

	// Plain text.
	tkn := lex.Token("#TX", jsonic.TinTX, text, fwd[:totalConsumed])
	pnt.SI += totalConsumed
	pnt.RI += rows
	pnt.CI += totalConsumed
	return &jsonic.LexCheckResult{Done: true, Token: tkn}
}

// handleTypeTag processes !!type tags (!!str, !!int, !!float, etc.).
func handleTypeTag(lex *jsonic.Lex, pnt *jsonic.Point, fwd string,
	tagHandles map[string]string, pendingAnchors *[]anchorInfo,
	anchors map[string]any, TX, NR, VL, ST jsonic.Tin) *jsonic.Token {

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
		rawVal := fwd[valStart+1 : valEnd-1]
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
		pnt.SI += valEnd
		pnt.CI += valEnd
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

	// Unquoted value.
	for valEnd < len(fwd) && fwd[valEnd] != '\n' && fwd[valEnd] != '\r' &&
		fwd[valEnd] != ',' && fwd[valEnd] != '}' && fwd[valEnd] != ']' {
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
	pnt.SI += valEnd
	pnt.CI += valEnd
	return tkn
}

// handleExplicitKey processes ? key\n: value patterns.
func handleExplicitKey(lex *jsonic.Lex, pnt *jsonic.Point, fwd string,
	pendingExplicitCL *bool, pendingTokens *[]*jsonic.Token,
	TX, CL, VL, IN jsonic.Tin) *jsonic.Token {

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
		indent := valConsumed - consumed
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
		pnt.SI += beforeNewline
		pnt.CI += beforeNewline
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
func handleDocMarker(lex *jsonic.Lex, pnt *jsonic.Point, fwd string,
	DS, DE jsonic.Tin) *jsonic.Token {

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
		pnt.CI += pos
	}
	var tkn *jsonic.Token
	if isEnd {
		tkn = lex.Token("#DE", DE, jsonic.Undefined, "...")
	} else {
		tkn = lex.Token("#DS", DS, jsonic.Undefined, "---")
	}
	pnt.SI += pos
	return tkn
}

// handleDoubleQuotedString processes YAML double-quoted strings.
func handleDoubleQuotedString(lex *jsonic.Lex, pnt *jsonic.Point, fwd string, ST jsonic.Tin) *jsonic.Token {
	i := 1
	val := ""
	escapedUpTo := 0
	rows := 0
	lastNewlineEnd := 0

	for i < len(fwd) && fwd[i] != '"' {
		if fwd[i] == '\\' {
			i++
			if i >= len(fwd) {
				break
			}
			esc := fwd[i]
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
			case 'x':
				if i+3 <= len(fwd) {
					n, err := strconv.ParseInt(fwd[i+1:i+3], 16, 32)
					if err == nil {
						val += string(rune(n))
						i += 3
						escapedUpTo = len(val)
					} else {
						val += string(esc)
						i++
					}
				} else {
					val += string(esc)
					i++
				}
			case 'u':
				if i+5 <= len(fwd) {
					n, err := strconv.ParseInt(fwd[i+1:i+5], 16, 32)
					if err == nil {
						val += string(rune(n))
						i += 5
						escapedUpTo = len(val)
					} else {
						val += string(esc)
						i++
					}
				} else {
					val += string(esc)
					i++
				}
			case 'U':
				if i+9 <= len(fwd) {
					n, err := strconv.ParseInt(fwd[i+1:i+9], 16, 32)
					if err == nil {
						val += string(rune(n))
						i += 9
						escapedUpTo = len(val)
					} else {
						val += string(esc)
						i++
					}
				} else {
					val += string(esc)
					i++
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
			val += fwd[i : i+1]
			i++
		}
	}
	if i < len(fwd) && fwd[i] == '"' {
		i++
	}
	tkn := lex.Token("#ST", ST, val, fwd[:i])
	pnt.SI += i
	pnt.RI += rows
	if rows > 0 {
		pnt.CI = i - lastNewlineEnd
	} else {
		pnt.CI += i
	}
	return tkn
}

// handleSingleQuotedString processes YAML single-quoted strings.
func handleSingleQuotedString(lex *jsonic.Lex, pnt *jsonic.Point, fwd string, ST jsonic.Tin) *jsonic.Token {
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
	if rows > 0 {
		pnt.CI = i - lastNewlineEnd
	} else {
		pnt.CI += i
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
func handleNumericColon(lex *jsonic.Lex, pnt *jsonic.Point, fwd string, TX jsonic.Tin, skipNumberMatch *bool, flowState *flowScanState) *jsonic.Token {
	hasEmbeddedColon := false
	hasTrailingText := false
	hasTrailingComma := false
	pi := 1
	for pi < len(fwd) && fwd[pi] != '\n' && fwd[pi] != '\r' {
		if fwd[pi] == ':' && pi+1 < len(fwd) && fwd[pi+1] != ' ' && fwd[pi+1] != '\t' &&
			fwd[pi+1] != '\n' && fwd[pi+1] != '\r' {
			hasEmbeddedColon = true
			break
		}
		// Trailing comma at end of line means plain scalar in block context
		// (e.g. "12,"). In flow context commas are separators followed by
		// more values on the same line.
		if fwd[pi] == ',' {
			ci := pi + 1
			for ci < len(fwd) && (fwd[ci] == ' ' || fwd[ci] == '\t') {
				ci++
			}
			if ci >= len(fwd) || fwd[ci] == '\n' || fwd[ci] == '\r' {
				hasTrailingComma = true
			}
			break
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
	if hasTrailingComma {
		// Block-context only — in flow context the comma is a real separator.
		flowState.advance(lex.Src, pnt.SI)
		if flowState.depth == 0 {
			end := 0
			for end < len(fwd) && fwd[end] != ' ' && fwd[end] != '\t' &&
				fwd[end] != '\n' && fwd[end] != '\r' {
				end++
			}
			text := fwd[:end]
			tkn := lex.Token("#TX", TX, text, text)
			pnt.SI += end
			pnt.CI += end
			return tkn
		}
	}
	if hasTrailingText {
		// Check if we're in a flow context — if so, the number is standalone.
		flowState.advance(lex.Src, pnt.SI)
		if flowState.depth == 0 {
			*skipNumberMatch = true
			return nil
		}
	}
	if !hasEmbeddedColon {
		return nil
	}
	end := 0
	for end < len(fwd) && fwd[end] != ' ' && fwd[end] != '\t' &&
		fwd[end] != '\n' && fwd[end] != '\r' {
		end++
	}
	text := fwd[:end]
	tkn := lex.Token("#TX", TX, text, text)
	pnt.SI += end
	pnt.CI += end
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
		n, err := strconv.ParseInt(rawVal, 10, 64)
		if err == nil {
			return float64(n)
		}
		return rawVal
	case "float":
		n, err := strconv.ParseFloat(rawVal, 64)
		if err == nil {
			return n
		}
		return rawVal
	case "bool":
		return rawVal == "true" || rawVal == "True" || rawVal == "TRUE"
	case "null":
		return nil
	default:
		return rawVal
	}
}

// tinToName converts a Tin to its name string.
func tinToName(tin jsonic.Tin) string {
	switch tin {
	case jsonic.TinTX:
		return "#TX"
	case jsonic.TinNR:
		return "#NR"
	case jsonic.TinST:
		return "#ST"
	case jsonic.TinVL:
		return "#VL"
	case jsonic.TinOB:
		return "#OB"
	case jsonic.TinCB:
		return "#CB"
	case jsonic.TinOS:
		return "#OS"
	case jsonic.TinCS:
		return "#CS"
	case jsonic.TinCL:
		return "#CL"
	case jsonic.TinCA:
		return "#CA"
	case jsonic.TinZZ:
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
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' b: 1 g: yaml }
    ]
    inject: { append: false }
  }

  # Indent rule: start for block content at a given indent.
  rule: indent: open: [
    # Key pair => map.
    { s: ['#KEY' '#CL'] p: map b: 2 g: yaml }
    # Element marker => list.
    { s: '#EL' p: list g: yaml }
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
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    # Indent followed by element marker: next element at same level.
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: yaml }
    # Same or lesser indent: close list.
    { s: '#IN' c: '@t0-le-in' b: 1 g: yaml }
    # Element marker at top level (no preceding newline).
    { s: '#EL' r: yamlBlockElem g: yaml }
    { s: '#ZZ' b: 1 g: yaml }
  ]

  # Subsequent elements in a yamlBlockList (via rotation).
  rule: yamlBlockElem: open: [
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    { p: val g: yaml }
  ]
  rule: yamlBlockElem: close: [
    # Doc-frame markers terminate elem; back up for the stream rule.
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: yaml }
    { s: '#IN' c: '@t0-le-in' b: 1 g: yaml }
    { s: '#EL' r: yamlBlockElem g: yaml }
    { s: '#ZZ' b: 1 g: yaml }
  ]

  # Amend list rule: close on dedent or same-indent non-element.
  rule: list: close: {
    alts: [
      # Doc-frame markers terminate list; back up for the stream rule.
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' c: '@t0-le-in' b: 1 g: yaml }
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
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: yaml }
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
      { s: '#ZZ' b: 1 g: yaml }
    ]
    inject: { append: false }
  }
  rule: pair: close: {
    alts: [
      # Doc-frame markers terminate pair; back up for the stream rule.
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' c: '@t0-eq-in' r: pair g: yaml }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: yaml }
    ]
    inject: { append: false }
  }

  # yamlElemMap: "- key: val" patterns.
  rule: yamlElemMap: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemMap: close: [
    # Doc-frame markers terminate elem-map; back up for the stream rule.
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: yaml }
    { s: '#IN' b: 1 g: yaml }
    { s: '#CA' b: 1 g: yaml }
    { s: '#CS' b: 1 g: yaml }
    { s: '#CB' b: 1 g: yaml }
    { s: '#ZZ' g: yaml }
  ]

  # Additional pairs in a yamlElemMap.
  rule: yamlElemPair: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemPair: close: [
    # Doc-frame markers terminate elem-pair; back up for the stream rule.
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: yaml }
    { s: '#IN' b: 1 g: yaml }
    { s: '#CA' b: 1 g: yaml }
    { s: '#CS' b: 1 g: yaml }
    { s: '#CB' b: 1 g: yaml }
    { s: '#ZZ' g: yaml }
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
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: ['#IN' '#EL'] c: '@t0-eq-in' r: elem g: yaml }
      { s: '#IN' c: '@t0-eq-in' b: 1 g: yaml }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: yaml }
      { s: '#EL' r: elem g: yaml }
    ]
    inject: { append: false }
  }
}
`

// --- END EMBEDDED yaml-grammar.jsonic ---

// configureGrammarRules installs the YAML grammar (alts from the declarative
// yaml-grammar.jsonic file) and wires state handlers (bo/ao/bc/ac) that need
// closure access to per-parse state.
func configureGrammarRules(j *jsonic.Jsonic, IN, EL jsonic.Tin, KEY []jsonic.Tin,
	CL, ZZ, CA, CS, CB, TX, ST, VL, NR jsonic.Tin,
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
	refs := map[jsonic.FuncRef]any{
		"@val-indent-deeper": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
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
		"@val-indent-eq-parent": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
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
		"@val-set-in-from-o0": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if v, ok := toInt(r.O0.Val); ok {
				r.EnsureN()["in"] = v
			}
		}),
		"@val-set-null": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.Node = nil
		}),
		"@val-set-el-in": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.EnsureN()["in"] = r.O0.CI - 1
		}),
		"@indent-plain-value": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if r.O0.Tin == ST || r.O0.Tin == TX {
				r.Node = r.O0.Val
			} else {
				r.Node = r.O0.Src
			}
		}),
		"@set-map-in": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.EnsureK()["yamlMapIn"] = r.N["in"] + 2
		}),
		"@t0-eq-in": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				return v == r.N["in"]
			}
			return false
		}),
		"@t0-le-in": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				return v <= r.N["in"]
			}
			return false
		}),
		"@t0-lt-in": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				return v < r.N["in"]
			}
			return false
		}),
		"@o0-eq-in": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			if v, ok := toInt(r.O0.Val); ok {
				return v == r.N["in"]
			}
			return false
		}),
		"@t0-eq-map-in": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			if v, ok := toInt(ctx.T0.Val); ok {
				if mapIn, ok := toInt(r.K["yamlMapIn"]); ok {
					return v == mapIn
				}
			}
			return false
		}),
		"@elem-key": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.EnsureU()["key"] = extractKey(r.O0, anchors)
		}),
		"@implicit-null-pair": jsonic.AltAction(func(r *jsonic.Rule, _ *jsonic.Context) {
			key := extractKey(r.O0, anchors)
			r.EnsureU()["key"] = key
			setNodeKey(r.Node, formatKey(key), nil)
		}),
		"@qm-pairkey": jsonic.AltAction(func(r *jsonic.Rule, _ *jsonic.Context) {
			r.EnsureU()["key"] = extractKey(r.O1, anchors)
		}),
		"@qm-implicit-null-pair": jsonic.AltAction(func(r *jsonic.Rule, _ *jsonic.Context) {
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
	gs := &jsonic.GrammarSpec{Ref: refs}
	if ruleMap, ok := asStringMap(parsedMap["rule"]); ok {
		gs.Rule = mapToGrammarRules(ruleMap)
	}
	if err := j.Grammar(gs); err != nil {
		panic(fmt.Sprintf("yaml: failed to apply grammar: %v", err))
	}

	// ===== State handlers (bo/ao/bc/ac) — kept in code for closure capture =====

	// val rule: claim pending anchors (ao), handle empty (bc), resolve
	// aliases and record anchors (ac), follow replacement chain (bc).
	j.Rule("val", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddAO(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if len(*pendingAnchors) > 0 {
				anchorsCopy := make([]anchorInfo, len(*pendingAnchors))
				copy(anchorsCopy, *pendingAnchors)
				r.EnsureU()["yamlAnchors"] = anchorsCopy
				r.EnsureU()["yamlAnchorOpenNode"] = r.Node
				*pendingAnchors = (*pendingAnchors)[:0]
			}
		})
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			child := r.Child
			if child != nil && child != jsonic.NoRule {
				final := child
				for final.Next != nil && final.Next != jsonic.NoRule &&
					final.Next.Prev == final {
					final = final.Next
				}
				if final != child && !jsonic.IsUndefined(final.Node) {
					r.Node = final.Node
				}
			}
		})
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if _, ok := r.U["yamlEmpty"]; ok {
				r.Node = jsonic.Undefined
			}
		})
		rs.AddAC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if m, ok := r.Node.(map[string]any); ok {
				if alias, ok := m["__yamlAlias"].(string); ok {
					val, exists := anchors[alias]
					if exists {
						switch v := val.(type) {
						case *jsonic.OrderedMap, jsonic.OrderedMap, map[string]any, []any:
							r.Node = deepCopy(v)
						default:
							r.Node = val
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
								case *jsonic.OrderedMap, jsonic.OrderedMap, map[string]any, []any:
									continue
								}
							}
						}
						val := r.Node
						switch v := val.(type) {
						case *jsonic.OrderedMap, jsonic.OrderedMap, map[string]any, []any:
							val = deepCopy(v)
						}
						anchors[anchor.name] = val
					}
				}
			}
		})
	})

	j.Rule("indent", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if !jsonic.IsUndefined(r.Child.Node) {
				r.Node = r.Child.Node
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
	pushBack := func(r *jsonic.Rule) {
		if r.Parent != nil && r.Parent != jsonic.NoRule &&
			r.Parent.Child != nil && r.Parent.Child != jsonic.NoRule {
			r.Parent.Child.Node = r.Node
		}
	}

	j.Rule("yamlBlockList", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddBO(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.Node = make([]any, 0)
			r.EnsureK()["yamlBlockArr"] = r.Node
			r.EnsureK()["yamlListIn"] = r.N["in"]
		})
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			val := r.Child.Node
			if jsonic.IsUndefined(val) {
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

	j.Rule("yamlBlockElem", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddBO(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.Node = r.K["yamlBlockArr"]
		})
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			val := r.Child.Node
			if jsonic.IsUndefined(val) {
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

	j.Rule("list", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddBO(func(r *jsonic.Rule, ctx *jsonic.Context) {
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
			if r.Parent != nil && r.Parent != jsonic.NoRule &&
				r.Parent.Name == "indent" {
				r.Node = []any{}
			}
		})
	})

	j.Rule("map", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddBO(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if _, ok := r.N["in"]; !ok {
				r.EnsureN()["in"] = 0
			}
			r.EnsureK()["yamlIn"] = r.N["in"]
		})
		rs.AddAC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			applyMergeKeys(r.Node)
		})
	})

	j.Rule("yamlElemMap", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddBO(func(r *jsonic.Rule, ctx *jsonic.Context) {
			// Build inline/flow element mappings as insertion-ordered maps
			// so they preserve source key order, matching the block-mapping
			// path (jsonic core now yields *OrderedMap) and the TS engine.
			r.Node = jsonic.NewOrderedMap()
		})
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if key := r.U["key"]; key != nil {
				if m, ok := r.Node.(*jsonic.OrderedMap); ok {
					val := r.Child.Node
					if jsonic.IsUndefined(val) {
						val = nil
					}
					m.Set(formatKey(key), val)
				}
			}
		})
	})

	j.Rule("yamlElemPair", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if key := r.U["key"]; key != nil {
				if m, ok := r.Node.(*jsonic.OrderedMap); ok {
					val := r.Child.Node
					if jsonic.IsUndefined(val) {
						val = nil
					}
					m.Set(formatKey(key), val)
				}
			}
		})
	})
}

// mapToGrammarRules converts a parsed rule map into typed GrammarRuleSpec map.
func mapToGrammarRules(ruleMap map[string]any) map[string]*jsonic.GrammarRuleSpec {
	rules := make(map[string]*jsonic.GrammarRuleSpec, len(ruleMap))
	for name, v := range ruleMap {
		rm, ok := asStringMap(v)
		if !ok {
			continue
		}
		spec := &jsonic.GrammarRuleSpec{}
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
	spec := &jsonic.GrammarAltListSpec{Alts: mapsToAlts(alts)}
	if inj, ok := asStringMap(val["inject"]); ok {
		spec.Inject = &jsonic.GrammarInjectSpec{}
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
func mapsToAlts(list []any) []*jsonic.GrammarAltSpec {
	out := make([]*jsonic.GrammarAltSpec, 0, len(list))
	for _, item := range list {
		m, ok := asStringMap(item)
		if !ok {
			continue
		}
		a := &jsonic.GrammarAltSpec{}
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
