package tabnasyaml

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
)

// y is a helper that parses YAML and returns the result.
func y(t *testing.T, src string) any {
	t.Helper()
	result, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse error: %v\nInput: %q", err, src)
	}
	return result
}

// asMap views a parsed object value as a plain map[string]any. The shared
// jsonic engine now returns parsed objects as an insertion-ordered
// *jsonic.OrderedMap; this unwraps either shape (dropping order) so tests
// can index into the result by key.
func asMap(v any) (map[string]any, bool) {
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

// jsonNormalize round-trips through JSON to normalize types (e.g., int→float64).
func jsonNormalize(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

func expectEqual(t *testing.T, got, want any) {
	t.Helper()
	gotN := jsonNormalize(got)
	wantN := jsonNormalize(want)
	if !reflect.DeepEqual(gotN, wantN) {
		gotJSON, _ := json.MarshalIndent(gotN, "", "  ")
		wantJSON, _ := json.MarshalIndent(wantN, "", "  ")
		t.Errorf("Mismatch:\nGot:  %s\nWant: %s", gotJSON, wantJSON)
	}
}

// ===== BLOCK MAPPINGS =====

func TestSinglePair(t *testing.T) {
	expectEqual(t, y(t, "a: 1"), map[string]any{"a": float64(1)})
}

func TestMultiplePairs(t *testing.T) {
	expectEqual(t, y(t, "a: 1\nb: 2\nc: 3"), map[string]any{"a": float64(1), "b": float64(2), "c": float64(3)})
}

func TestNestedMap(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b: 1\n  c: 2"), map[string]any{"a": map[string]any{"b": float64(1), "c": float64(2)}})
}

func TestDeeplyNestedMap(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b:\n    c:\n      d: 1"),
		map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": float64(1)}}}})
}

func TestSiblingNestedMaps(t *testing.T) {
	expectEqual(t, y(t, "a:\n  x: 1\nb:\n  y: 2"),
		map[string]any{"a": map[string]any{"x": float64(1)}, "b": map[string]any{"y": float64(2)}})
}

func TestEmptyValueFollowedBySibling(t *testing.T) {
	expectEqual(t, y(t, "a:\nb: 1"), map[string]any{"a": nil, "b": float64(1)})
}

func TestColonAtEndOfLine(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b: 1"), map[string]any{"a": map[string]any{"b": float64(1)}})
}

func TestTrailingNewline(t *testing.T) {
	expectEqual(t, y(t, "a: 1\n"), map[string]any{"a": float64(1)})
}

// ===== BLOCK SEQUENCES =====

func TestSimpleList(t *testing.T) {
	expectEqual(t, y(t, "- a\n- b\n- c"), []any{"a", "b", "c"})
}

func TestSingleElement(t *testing.T) {
	expectEqual(t, y(t, "- a"), []any{"a"})
}

func TestNestedListInMap(t *testing.T) {
	expectEqual(t, y(t, "items:\n  - a\n  - b"), map[string]any{"items": []any{"a", "b"}})
}

func TestListOfNumbers(t *testing.T) {
	expectEqual(t, y(t, "- 1\n- 2\n- 3"), []any{float64(1), float64(2), float64(3)})
}

func TestListOfMaps(t *testing.T) {
	expectEqual(t, y(t, "- name: alice\n- name: bob"),
		[]any{map[string]any{"name": "alice"}, map[string]any{"name": "bob"}})
}

func TestNestedListOfMapsMultikey(t *testing.T) {
	expectEqual(t, y(t, "items:\n  - name: alice\n    age: 30\n  - name: bob\n    age: 25"),
		map[string]any{"items": []any{
			map[string]any{"name": "alice", "age": float64(30)},
			map[string]any{"name": "bob", "age": float64(25)},
		}})
}

func TestDeeplyNestedList(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b:\n    - x\n    - y"),
		map[string]any{"a": map[string]any{"b": []any{"x", "y"}}})
}

func TestMixedMapThenList(t *testing.T) {
	expectEqual(t, y(t, "a: 1\nb:\n  - x\n  - y\nc: 3"),
		map[string]any{"a": float64(1), "b": []any{"x", "y"}, "c": float64(3)})
}

// ===== SCALAR TYPES =====

func TestInteger(t *testing.T) {
	expectEqual(t, y(t, "a: 42"), map[string]any{"a": float64(42)})
}

func TestNegativeInteger(t *testing.T) {
	expectEqual(t, y(t, "a: -7"), map[string]any{"a": float64(-7)})
}

func TestFloat(t *testing.T) {
	expectEqual(t, y(t, "a: 3.14"), map[string]any{"a": float64(3.14)})
}

func TestZero(t *testing.T) {
	expectEqual(t, y(t, "a: 0"), map[string]any{"a": float64(0)})
}

func TestBooleanTrue(t *testing.T) {
	expectEqual(t, y(t, "a: true"), map[string]any{"a": true})
}

func TestBooleanFalse(t *testing.T) {
	expectEqual(t, y(t, "a: false"), map[string]any{"a": false})
}

func TestNullKeyword(t *testing.T) {
	expectEqual(t, y(t, "a: null"), map[string]any{"a": nil})
}

func TestTildeNull(t *testing.T) {
	expectEqual(t, y(t, "a: ~"), map[string]any{"a": nil})
}

func TestEmptyValueNull(t *testing.T) {
	expectEqual(t, y(t, "a:"), map[string]any{"a": nil})
}

func TestPlainString(t *testing.T) {
	expectEqual(t, y(t, "a: hello world"), map[string]any{"a": "hello world"})
}

func TestPlainStringWithDoubleCurlyBraces(t *testing.T) {
	expectEqual(t, y(t, "foo: a{{q}}b"), map[string]any{"foo": "a{{q}}b"})
}

func TestOctalNumber(t *testing.T) {
	expectEqual(t, y(t, "a: 0o77"), map[string]any{"a": float64(63)})
}

func TestHexNumber(t *testing.T) {
	expectEqual(t, y(t, "a: 0xFF"), map[string]any{"a": float64(255)})
}

func TestPositiveInfinity(t *testing.T) {
	result := y(t, "a: .inf")
	m, ok := asMap(result)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	v, ok := m["a"].(float64)
	if !ok || !math.IsInf(v, 1) {
		t.Errorf("expected +Inf, got %v", m["a"])
	}
}

func TestNegativeInfinity(t *testing.T) {
	result := y(t, "a: -.inf")
	m, ok := asMap(result)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	v, ok := m["a"].(float64)
	if !ok || !math.IsInf(v, -1) {
		t.Errorf("expected -Inf, got %v", m["a"])
	}
}

func TestNaN(t *testing.T) {
	result := y(t, "a: .nan")
	m, ok := asMap(result)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	v, ok := m["a"].(float64)
	if !ok || !math.IsNaN(v) {
		t.Errorf("expected NaN, got %v", m["a"])
	}
}

func TestYesBoolean(t *testing.T) {
	expectEqual(t, y(t, "a: yes"), map[string]any{"a": true})
}

func TestNoBoolean(t *testing.T) {
	expectEqual(t, y(t, "a: no"), map[string]any{"a": false})
}

func TestOnBoolean(t *testing.T) {
	expectEqual(t, y(t, "a: on"), map[string]any{"a": true})
}

func TestOffBoolean(t *testing.T) {
	expectEqual(t, y(t, "a: off"), map[string]any{"a": false})
}

// ===== QUOTED STRINGS =====

func TestDoubleQuoted(t *testing.T) {
	expectEqual(t, y(t, `a: "hello"`), map[string]any{"a": "hello"})
}

func TestSingleQuoted(t *testing.T) {
	expectEqual(t, y(t, `a: 'hello'`), map[string]any{"a": "hello"})
}

func TestDoubleQuotedWithColon(t *testing.T) {
	expectEqual(t, y(t, `a: "key: value"`), map[string]any{"a": "key: value"})
}

func TestSingleQuotedWithColon(t *testing.T) {
	expectEqual(t, y(t, `a: 'key: value'`), map[string]any{"a": "key: value"})
}

func TestDoubleQuotedEmpty(t *testing.T) {
	expectEqual(t, y(t, `a: ""`), map[string]any{"a": ""})
}

func TestSingleQuotedEmpty(t *testing.T) {
	expectEqual(t, y(t, `a: ''`), map[string]any{"a": ""})
}

func TestQuotedNumberStaysString(t *testing.T) {
	expectEqual(t, y(t, `a: "42"`), map[string]any{"a": "42"})
}

func TestQuotedBooleanStaysString(t *testing.T) {
	expectEqual(t, y(t, `a: "true"`), map[string]any{"a": "true"})
}

func TestSingleQuotedWithDoubleCurlyBraces(t *testing.T) {
	expectEqual(t, y(t, `foo: 'a{{q}}b'`), map[string]any{"foo": "a{{q}}b"})
}

func TestDoubleQuotedWithDoubleCurlyBraces(t *testing.T) {
	expectEqual(t, y(t, `foo: "a{{q}}b"`), map[string]any{"foo": "a{{q}}b"})
}

// ===== BLOCK SCALARS =====

func TestLiteralBlock(t *testing.T) {
	expectEqual(t, y(t, "a: |\n  line1\n  line2\n  line3"),
		map[string]any{"a": "line1\nline2\nline3\n"})
}

func TestLiteralBlockStrip(t *testing.T) {
	expectEqual(t, y(t, "a: |-\n  line1\n  line2"),
		map[string]any{"a": "line1\nline2"})
}

func TestLiteralBlockKeep(t *testing.T) {
	expectEqual(t, y(t, "a: |+\n  line1\n  line2\n\n"),
		map[string]any{"a": "line1\nline2\n\n"})
}

func TestFoldedBlock(t *testing.T) {
	expectEqual(t, y(t, "a: >\n  line1\n  line2\n  line3"),
		map[string]any{"a": "line1 line2 line3\n"})
}

func TestFoldedBlockStrip(t *testing.T) {
	expectEqual(t, y(t, "a: >-\n  line1\n  line2"),
		map[string]any{"a": "line1 line2"})
}

func TestFoldedBlockKeep(t *testing.T) {
	expectEqual(t, y(t, "a: >+\n  line1\n  line2\n\n"),
		map[string]any{"a": "line1 line2\n\n"})
}

func TestLiteralBlockPreservesInnerIndent(t *testing.T) {
	expectEqual(t, y(t, "a: |\n  line1\n    indented\n  line3"),
		map[string]any{"a": "line1\n  indented\nline3\n"})
}

func TestLiteralBlockCSVExamplePreservedVerbatim(t *testing.T) {
	expectEqual(t, y(t, "schema:\n  example: |\n    \"clickId\",\"date\",\"placementId\",\"market\",\"merchantId\",\"merchantName\",\"revenue\",\"currency\"\n    \"532f889fd3ba56f628f3234647d9854650534789938b7fdaafddf1d75081fadc\",\"2018-01-01T00:00:01+00:00\",\"your-custom-placement-id-1\",\"de\",\"583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e\",\"example.com\",\"0.142898\",\"EUR\"\n    \"ae7facb00d557e7d92e1d2ee31bc05cc9787bc6802e636ccb284cfbaeb6680b8\",\"2018-01-01T00:00:02+00:00\",\"your-custom-placement-id-2\",\"de\",\"583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e\",\"example.com\",\"0.142825\",\"EUR\"\n    \"8bc875e7f5260fa14b21797508b9e47ee2df2c2fe0351b88edded847ee59bb1f\",\"2018-01-01T00:00:03+00:00\",\"your-custom-placement-id-3\",\"de\",\"583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e\",\"example.com\",\"0.120417\",\"EUR\""),
		map[string]any{
			"schema": map[string]any{
				"example": "\"clickId\",\"date\",\"placementId\",\"market\",\"merchantId\",\"merchantName\",\"revenue\",\"currency\"\n" +
					"\"532f889fd3ba56f628f3234647d9854650534789938b7fdaafddf1d75081fadc\",\"2018-01-01T00:00:01+00:00\",\"your-custom-placement-id-1\",\"de\",\"583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e\",\"example.com\",\"0.142898\",\"EUR\"\n" +
					"\"ae7facb00d557e7d92e1d2ee31bc05cc9787bc6802e636ccb284cfbaeb6680b8\",\"2018-01-01T00:00:02+00:00\",\"your-custom-placement-id-2\",\"de\",\"583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e\",\"example.com\",\"0.142825\",\"EUR\"\n" +
					"\"8bc875e7f5260fa14b21797508b9e47ee2df2c2fe0351b88edded847ee59bb1f\",\"2018-01-01T00:00:03+00:00\",\"your-custom-placement-id-3\",\"de\",\"583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e\",\"example.com\",\"0.120417\",\"EUR\"\n",
			},
		})
}

// ===== FLOW COLLECTIONS =====

func TestFlowSequence(t *testing.T) {
	expectEqual(t, y(t, "a: [1, 2, 3]"), map[string]any{"a": []any{float64(1), float64(2), float64(3)}})
}

func TestFlowMapping(t *testing.T) {
	expectEqual(t, y(t, "a: {x: 1, y: 2}"), map[string]any{"a": map[string]any{"x": float64(1), "y": float64(2)}})
}

func TestNestedFlowInBlock(t *testing.T) {
	expectEqual(t, y(t, "a: [1, [2, 3]]"),
		map[string]any{"a": []any{float64(1), []any{float64(2), float64(3)}}})
}

func TestEmptyFlowSequence(t *testing.T) {
	expectEqual(t, y(t, "a: []"), map[string]any{"a": []any{}})
}

func TestEmptyFlowMapping(t *testing.T) {
	expectEqual(t, y(t, "a: {}"), map[string]any{"a": map[string]any{}})
}

func TestFlowAtTopLevelSeq(t *testing.T) {
	expectEqual(t, y(t, "[1, 2, 3]"), []any{float64(1), float64(2), float64(3)})
}

func TestFlowAtTopLevelMap(t *testing.T) {
	expectEqual(t, y(t, "{a: 1, b: 2}"), map[string]any{"a": float64(1), "b": float64(2)})
}

// ===== COMMENTS =====

func TestLineComment(t *testing.T) {
	expectEqual(t, y(t, "a: 1 # comment\nb: 2"),
		map[string]any{"a": float64(1), "b": float64(2)})
}

func TestFullLineComment(t *testing.T) {
	expectEqual(t, y(t, "# this is a comment\na: 1"), map[string]any{"a": float64(1)})
}

func TestCommentAfterKey(t *testing.T) {
	expectEqual(t, y(t, "a: # comment\n  b: 1"),
		map[string]any{"a": map[string]any{"b": float64(1)}})
}

func TestMultipleComments(t *testing.T) {
	expectEqual(t, y(t, "# first\na: 1\n# second\nb: 2"),
		map[string]any{"a": float64(1), "b": float64(2)})
}

func TestCommentInList(t *testing.T) {
	expectEqual(t, y(t, "- a # comment\n- b"), []any{"a", "b"})
}

// ===== ANCHORS AND ALIASES =====

func TestSimpleAnchorAlias(t *testing.T) {
	expectEqual(t, y(t, "a: &ref hello\nb: *ref"),
		map[string]any{"a": "hello", "b": "hello"})
}

func TestAnchorOnMap(t *testing.T) {
	expectEqual(t, y(t, "defaults: &defaults\n  x: 1\n  y: 2\noverride:\n  <<: *defaults\n  y: 3"),
		map[string]any{
			"defaults": map[string]any{"x": float64(1), "y": float64(2)},
			"override": map[string]any{"x": float64(1), "y": float64(3)},
		})
}

func TestAnchorOnSequence(t *testing.T) {
	expectEqual(t, y(t, "a: &items\n  - 1\n  - 2\nb: *items"),
		map[string]any{
			"a": []any{float64(1), float64(2)},
			"b": []any{float64(1), float64(2)},
		})
}

func TestMultipleAliases(t *testing.T) {
	expectEqual(t, y(t, "a: &x 10\nb: &y 20\nc: *x\nd: *y"),
		map[string]any{"a": float64(10), "b": float64(20), "c": float64(10), "d": float64(20)})
}

// ===== MERGE KEY =====

func TestSimpleMerge(t *testing.T) {
	expectEqual(t, y(t, "defaults: &d\n  a: 1\n  b: 2\nresult:\n  <<: *d\n  c: 3"),
		map[string]any{
			"defaults": map[string]any{"a": float64(1), "b": float64(2)},
			"result":   map[string]any{"a": float64(1), "b": float64(2), "c": float64(3)},
		})
}

func TestMergeOverride(t *testing.T) {
	expectEqual(t, y(t, "base: &b\n  x: 1\n  y: 2\nchild:\n  <<: *b\n  y: 99"),
		map[string]any{
			"base":  map[string]any{"x": float64(1), "y": float64(2)},
			"child": map[string]any{"x": float64(1), "y": float64(99)},
		})
}

// ===== MULTI-DOCUMENT =====

func TestDocumentStartMarker(t *testing.T) {
	expectEqual(t, y(t, "---\na: 1"), map[string]any{"a": float64(1)})
}

func TestDocumentEndMarker(t *testing.T) {
	expectEqual(t, y(t, "a: 1\n..."), map[string]any{"a": float64(1)})
}

func TestTwoDocuments(t *testing.T) {
	expectEqual(t, y(t, "---\na: 1\n---\nb: 2"),
		[]any{map[string]any{"a": float64(1)}, map[string]any{"b": float64(2)}})
}

func TestThreeDocuments(t *testing.T) {
	expectEqual(t, y(t, "---\na: 1\n---\nb: 2\n---\nc: 3"),
		[]any{
			map[string]any{"a": float64(1)},
			map[string]any{"b": float64(2)},
			map[string]any{"c": float64(3)},
		})
}

func TestTwoDocumentsWithEndMarkers(t *testing.T) {
	expectEqual(t, y(t, "---\na: 1\n...\n---\nb: 2"),
		[]any{map[string]any{"a": float64(1)}, map[string]any{"b": float64(2)}})
}

func TestMultiDocMixedShapes(t *testing.T) {
	expectEqual(t, y(t, "---\n- 1\n- 2\n---\na: 1\n---\nfoo"),
		[]any{
			[]any{float64(1), float64(2)},
			map[string]any{"a": float64(1)},
			"foo",
		})
}

func TestMultiDocEmptyDocs(t *testing.T) {
	expectEqual(t, y(t, "---\n---\n---"), []any{nil, nil, nil})
}

func TestMultiDocListOfLists(t *testing.T) {
	expectEqual(t, y(t, "---\n- a\n- b\n---\n- c\n- d"),
		[]any{[]any{"a", "b"}, []any{"c", "d"}})
}

func TestMultiDocWithYamlDirective(t *testing.T) {
	expectEqual(t, y(t, "%YAML 1.2\n---\na: 1"), map[string]any{"a": float64(1)})
}

func TestMultiDocWithTagDirective(t *testing.T) {
	expectEqual(t, y(t, "%TAG !! tag:example.com,2025:\n---\na: 1"),
		map[string]any{"a": float64(1)})
}

// ===== STREAM META OPTION =====

func ymeta(t *testing.T, src string) *MetaResult {
	t.Helper()
	j := MakeJsonic(YamlOptions{Meta: true})
	r, err := j.Parse(src)
	if err != nil {
		t.Fatalf("Parse error: %v\nInput: %q", err, src)
	}
	mr, ok := r.(*MetaResult)
	if !ok {
		t.Fatalf("expected *MetaResult, got %T", r)
	}
	return mr
}

func TestMetaSingleDocImplicit(t *testing.T) {
	r := ymeta(t, "a: 1")
	expectEqual(t, r.Content, map[string]any{"a": float64(1)})
	m, ok := r.Meta.(*DocMeta)
	if !ok {
		t.Fatalf("expected *DocMeta, got %T", r.Meta)
	}
	if m.Explicit != false || m.Ended != false || len(m.Directives) != 0 {
		t.Errorf("unexpected meta: %+v", m)
	}
}

func TestMetaSingleDocExplicitStart(t *testing.T) {
	r := ymeta(t, "---\na: 1")
	m := r.Meta.(*DocMeta)
	if !m.Explicit {
		t.Errorf("expected Explicit=true, got %+v", m)
	}
}

func TestMetaSingleDocExplicitEnd(t *testing.T) {
	r := ymeta(t, "a: 1\n...")
	m := r.Meta.(*DocMeta)
	if !m.Ended {
		t.Errorf("expected Ended=true, got %+v", m)
	}
}

func TestMetaTwoDocsArray(t *testing.T) {
	r := ymeta(t, "---\na: 1\n---\nb: 2")
	expectEqual(t, r.Content, []any{
		map[string]any{"a": float64(1)},
		map[string]any{"b": float64(2)},
	})
	metas, ok := r.Meta.([]*DocMeta)
	if !ok {
		t.Fatalf("expected []*DocMeta, got %T", r.Meta)
	}
	if len(metas) != 2 || !metas[0].Explicit || !metas[1].Explicit {
		t.Errorf("unexpected metas: %+v", metas)
	}
}

func TestMetaTwoDocsEndFlagOnlyOnFirst(t *testing.T) {
	r := ymeta(t, "---\na: 1\n...\n---\nb: 2")
	metas := r.Meta.([]*DocMeta)
	if !metas[0].Ended {
		t.Errorf("expected metas[0].Ended=true, got %+v", metas[0])
	}
	if metas[1].Ended {
		t.Errorf("expected metas[1].Ended=false, got %+v", metas[1])
	}
}

func TestMetaDirectiveCaptured(t *testing.T) {
	r := ymeta(t, "%YAML 1.2\n---\na: 1")
	m := r.Meta.(*DocMeta)
	if len(m.Directives) != 1 || m.Directives[0] != "%YAML 1.2" {
		t.Errorf("unexpected directives: %+v", m.Directives)
	}
	if !m.Explicit {
		t.Errorf("expected Explicit=true")
	}
}

func TestMetaPerDocDirectivesIsolated(t *testing.T) {
	r := ymeta(t, "%YAML 1.2\n---\na: 1\n---\nb: 2")
	metas := r.Meta.([]*DocMeta)
	if len(metas[0].Directives) != 1 || metas[0].Directives[0] != "%YAML 1.2" {
		t.Errorf("metas[0].Directives = %+v", metas[0].Directives)
	}
	if len(metas[1].Directives) != 0 {
		t.Errorf("metas[1].Directives = %+v (want empty)", metas[1].Directives)
	}
}

func TestMetaDisabledReturnsBareContent(t *testing.T) {
	// Default Meta:false: same shape as no plugin option.
	expectEqual(t, y(t, "a: 1"), map[string]any{"a": float64(1)})
	expectEqual(t, y(t, "---\na: 1\n---\nb: 2"),
		[]any{map[string]any{"a": float64(1)}, map[string]any{"b": float64(2)}})
}

// ===== TAGS =====

func TestExplicitStringTag(t *testing.T) {
	expectEqual(t, y(t, "a: !!str 42"), map[string]any{"a": "42"})
}

func TestExplicitIntTag(t *testing.T) {
	expectEqual(t, y(t, `a: !!int "42"`), map[string]any{"a": float64(42)})
}

func TestExplicitFloatTag(t *testing.T) {
	expectEqual(t, y(t, `a: !!float "3.14"`), map[string]any{"a": float64(3.14)})
}

func TestExplicitBoolTag(t *testing.T) {
	expectEqual(t, y(t, `a: !!bool "true"`), map[string]any{"a": true})
}

func TestExplicitNullTag(t *testing.T) {
	expectEqual(t, y(t, `a: !!null ""`), map[string]any{"a": nil})
}

// ===== COMPLEX KEYS =====

func TestExplicitKey(t *testing.T) {
	expectEqual(t, y(t, "? a\n: 1"), map[string]any{"a": float64(1)})
}

func TestNumericKey(t *testing.T) {
	expectEqual(t, y(t, "1: one\n2: two"), map[string]any{"1": "one", "2": "two"})
}

// ===== DIRECTIVES =====

func TestYamlDirective(t *testing.T) {
	// Should not error - directive stripped
	result, err := Parse("%YAML 1.2\n---\na: 1")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	expectEqual(t, result, map[string]any{"a": float64(1)})
}

func TestTagDirective(t *testing.T) {
	result, err := Parse("%TAG ! tag:example.com,2000:\n---\na: 1")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	expectEqual(t, result, map[string]any{"a": float64(1)})
}

// ===== INDENTATION =====

func TestTwoSpaceIndent(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b: 1"), map[string]any{"a": map[string]any{"b": float64(1)}})
}

func TestFourSpaceIndent(t *testing.T) {
	expectEqual(t, y(t, "a:\n    b: 1"), map[string]any{"a": map[string]any{"b": float64(1)}})
}

func TestMixedIndentLevels(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b:\n      c: 1"),
		map[string]any{"a": map[string]any{"b": map[string]any{"c": float64(1)}}})
}

func TestReturnToOuterIndent(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b: 1\n  c: 2\nd: 3"),
		map[string]any{"a": map[string]any{"b": float64(1), "c": float64(2)}, "d": float64(3)})
}

func TestMultipleIndentReturns(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b:\n    c: 1\n  d: 2\ne: 3"),
		map[string]any{"a": map[string]any{"b": map[string]any{"c": float64(1)}, "d": float64(2)}, "e": float64(3)})
}

func TestListIndentUnderMap(t *testing.T) {
	expectEqual(t, y(t, "a:\n  - 1\n  - 2\nb: 3"),
		map[string]any{"a": []any{float64(1), float64(2)}, "b": float64(3)})
}

// ===== MULTILINE PLAIN SCALARS =====

func TestContinuationLine(t *testing.T) {
	expectEqual(t, y(t, "a: this is\n  a long string"),
		map[string]any{"a": "this is a long string"})
}

func TestMultipleContinuationLines(t *testing.T) {
	expectEqual(t, y(t, "a: line one\n  line two\n  line three"),
		map[string]any{"a": "line one line two line three"})
}

// ===== WINDOWS LINE ENDINGS =====

func TestCRLF(t *testing.T) {
	expectEqual(t, y(t, "a: 1\r\nb: 2"),
		map[string]any{"a": float64(1), "b": float64(2)})
}

func TestCRLFNested(t *testing.T) {
	expectEqual(t, y(t, "a:\r\n  b: 1\r\n  c: 2"),
		map[string]any{"a": map[string]any{"b": float64(1), "c": float64(2)}})
}

func TestCRLFList(t *testing.T) {
	expectEqual(t, y(t, "- a\r\n- b"), []any{"a", "b"})
}

// ===== SPECIAL CHARS IN VALUES =====

func TestValueWithHashNotComment(t *testing.T) {
	expectEqual(t, y(t, "a: foo#bar"), map[string]any{"a": "foo#bar"})
}

func TestKeyWithSpaces(t *testing.T) {
	expectEqual(t, y(t, "a long key: value"), map[string]any{"a long key": "value"})
}

// ===== SEQUENCE OF MAPPINGS =====

func TestCompactNotation(t *testing.T) {
	expectEqual(t, y(t, "- name: alice\n  age: 30\n- name: bob\n  age: 25"),
		[]any{
			map[string]any{"name": "alice", "age": float64(30)},
			map[string]any{"name": "bob", "age": float64(25)},
		})
}

func TestSingleKeyPerElement(t *testing.T) {
	expectEqual(t, y(t, "- a: 1\n- b: 2\n- c: 3"),
		[]any{map[string]any{"a": float64(1)}, map[string]any{"b": float64(2)}, map[string]any{"c": float64(3)}})
}

func TestNestedInMap(t *testing.T) {
	expectEqual(t, y(t, "people:\n  - name: alice\n  - name: bob"),
		map[string]any{"people": []any{map[string]any{"name": "alice"}, map[string]any{"name": "bob"}}})
}

// ===== REAL-WORLD YAML PATTERNS =====

func TestDockerComposeLike(t *testing.T) {
	expectEqual(t, y(t, "version: 3\nservices:\n  web:\n    image: nginx\n    ports:\n      - 80\n      - 443"),
		map[string]any{
			"version": float64(3),
			"services": map[string]any{
				"web": map[string]any{
					"image": "nginx",
					"ports": []any{float64(80), float64(443)},
				},
			},
		})
}

func TestGithubActionsLike(t *testing.T) {
	expectEqual(t, y(t, "name: build\non:\n  push:\n    branches:\n      - main\njobs:\n  test:\n    runs-on: ubuntu"),
		map[string]any{
			"name": "build",
			"on":   map[string]any{"push": map[string]any{"branches": []any{"main"}}},
			"jobs": map[string]any{"test": map[string]any{"runs-on": "ubuntu"}},
		})
}

func TestKubernetesLike(t *testing.T) {
	expectEqual(t, y(t, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: myapp\n  labels:\n    app: myapp\nspec:\n  containers:\n    - name: web\n      image: nginx"),
		map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata":   map[string]any{"name": "myapp", "labels": map[string]any{"app": "myapp"}},
			"spec":       map[string]any{"containers": []any{map[string]any{"name": "web", "image": "nginx"}}},
		})
}

func TestAnsibleLike(t *testing.T) {
	expectEqual(t, y(t, "- name: install packages\n  become: true\n- name: start service\n  become: false"),
		[]any{
			map[string]any{"name": "install packages", "become": true},
			map[string]any{"name": "start service", "become": false},
		})
}

func TestConfigFileLike(t *testing.T) {
	expectEqual(t, y(t, "database:\n  host: localhost\n  port: 5432\n  name: mydb\ncache:\n  enabled: true\n  ttl: 3600"),
		map[string]any{
			"database": map[string]any{"host": "localhost", "port": float64(5432), "name": "mydb"},
			"cache":    map[string]any{"enabled": true, "ttl": float64(3600)},
		})
}

// ===== EMPTY INPUT =====

func TestEmptyInput(t *testing.T) {
	result, err := Parse("")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestWhitespaceOnly(t *testing.T) {
	result, err := Parse("   \n  \n  ")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

// ===== HAPPY PATH =====

func TestHappy(t *testing.T) {
	expectEqual(t, y(t, "a: 1\nb: 2\nc:\n  d: 3\n  e: 4\n  f:\n  - g\n  - h\n"),
		map[string]any{
			"a": float64(1),
			"b": float64(2),
			"c": map[string]any{
				"d": float64(3),
				"e": float64(4),
				"f": []any{"g", "h"},
			},
		})
}
