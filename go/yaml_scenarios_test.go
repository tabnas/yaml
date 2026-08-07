/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

// Scenario tests ported from ts/test/yaml.test.ts that previously had no
// Go counterpart. Test names and expectations mirror the TS test names
// (e.g. 'double-quoted-with-newline-escape' -> TestDoubleQuotedWithNewlineEscape).

package tabnasyaml

import (
	"reflect"
	"testing"
)

// The `baseline` helper used to live here. It mirrored the TS pattern
//
//	try { result = y(src) } catch (e) { result = 'ERROR' }
//	assert.ok(result != null)
//
// which accepted a parse error OR any non-nil value — i.e. it could not fail
// for any input, in either runtime. Both sides now assert the exact observed
// value instead, so a behaviour change shows up as a diff.

// ===== BLOCK MAPPINGS =====

// TS: colon-space-required — no space after the colon, so the whole line is
// one plain scalar.
func TestColonSpaceRequired(t *testing.T) {
	expectEqual(t, y(t, "a:b"), "a:b")
}

// TS: multiple-trailing-newlines
func TestMultipleTrailingNewlines(t *testing.T) {
	expectEqual(t, y(t, "a: 1\n\n\n"), map[string]any{"a": float64(1)})
}

// ===== SCALAR TYPES =====

// TS: string-with-special-chars
func TestStringWithSpecialChars(t *testing.T) {
	expectEqual(t, y(t, "a: hello, world!"), map[string]any{"a": "hello, world!"})
}

// TS: timestamp-date — this subset does not resolve the timestamp type: the
// value stays a plain string. Pinned exactly rather than just "not nil".
func TestTimestampDate(t *testing.T) {
	expectEqual(t, y(t, "a: 2024-01-15"), map[string]any{"a": "2024-01-15"})
}

// TS: timestamp-datetime
func TestTimestampDatetime(t *testing.T) {
	expectEqual(t, y(t, "a: 2024-01-15T10:30:00Z"),
		map[string]any{"a": "2024-01-15T10:30:00Z"})
}

// ===== QUOTED STRINGS =====

// TS: double-quoted-with-newline-escape
func TestDoubleQuotedWithNewlineEscape(t *testing.T) {
	expectEqual(t, y(t, "a: \"line1\\nline2\""), map[string]any{"a": "line1\nline2"})
}

// TS: double-quoted-with-tab-escape
func TestDoubleQuotedWithTabEscape(t *testing.T) {
	expectEqual(t, y(t, "a: \"col1\\tcol2\""), map[string]any{"a": "col1\tcol2"})
}

// TS: single-quoted-no-escapes — single-quoted strings don't process escapes.
func TestSingleQuotedNoEscapes(t *testing.T) {
	expectEqual(t, y(t, `a: 'line1\nline2'`), map[string]any{"a": `line1\nline2`})
}

// TS: quoted-key
func TestQuotedKey(t *testing.T) {
	expectEqual(t, y(t, `"a b": 1`), map[string]any{"a b": float64(1)})
}

// ===== BLOCK SCALARS =====

// TS: literal-block-with-indent
func TestLiteralBlockWithIndent(t *testing.T) {
	expectEqual(t, y(t, "a:\n  b: |\n    indented\n    text"),
		map[string]any{"a": map[string]any{"b": "indented\ntext\n"}})
}

// ===== FLOW COLLECTIONS =====

// TS: flow-map-in-flow-seq
func TestFlowMapInFlowSeq(t *testing.T) {
	expectEqual(t, y(t, "a: [{x: 1}, {y: 2}]"),
		map[string]any{"a": []any{
			map[string]any{"x": float64(1)},
			map[string]any{"y": float64(2)},
		}})
}

// ===== COMMENTS =====

// TS: comment-only-line-between-pairs
func TestCommentOnlyLineBetweenPairs(t *testing.T) {
	expectEqual(t, y(t, "a: 1\n# middle\nb: 2"),
		map[string]any{"a": float64(1), "b": float64(2)})
}

// ===== MERGE KEY =====

// TS: merge-multiple — merge a sequence of aliases.
func TestMergeMultiple(t *testing.T) {
	expectEqual(t, y(t, "a: &a\n  x: 1\nb: &b\n  y: 2\nc:\n  <<: [*a, *b]\n  z: 3"),
		map[string]any{
			"a": map[string]any{"x": float64(1)},
			"b": map[string]any{"y": float64(2)},
			"c": map[string]any{"x": float64(1), "y": float64(2), "z": float64(3)},
		})
}

// ===== MULTI-DOCUMENT =====

// TS: reparse-same-source-is-idempotent — parsing the same source twice on
// one parser instance must produce identical output (regression for
// per-parse state reset gated by a source-string identity check).
func TestReparseSameSourceIsIdempotent(t *testing.T) {
	j := MakeJsonic()
	src := "openapi: 3.0\npaths:\n  /a:\n    get: {}"
	a, err := j.Parse(src)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	b, err := j.Parse(src)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("reparse mismatch:\nfirst:  %#v\nsecond: %#v", a, b)
	}
}

// ===== STREAM META OPTION =====

// TS: multiple-directives-captured
func TestMetaMultipleDirectivesCaptured(t *testing.T) {
	r := ymeta(t, "%YAML 1.2\n%TAG !! tag:foo.com,2025:\n---\na: 1")
	m, ok := r.Meta.(*DocMeta)
	if !ok {
		t.Fatalf("expected *DocMeta, got %T", r.Meta)
	}
	want := []string{"%YAML 1.2", "%TAG !! tag:foo.com,2025:"}
	if !reflect.DeepEqual(m.Directives, want) {
		t.Errorf("directives: want %v, got %v", want, m.Directives)
	}
}

// TS: meta-explicitly-disabled — Meta:false matches meta-not-passed.
func TestMetaExplicitlyDisabled(t *testing.T) {
	j := MakeJsonic(YamlOptions{Meta: false})
	result, err := j.Parse("a: 1")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	expectEqual(t, result, map[string]any{"a": float64(1)})
}

// ===== TAGS =====

// TS: explicit-seq-tag
func TestExplicitSeqTag(t *testing.T) {
	expectEqual(t, y(t, "a: !!seq\n  - 1\n  - 2"),
		map[string]any{"a": []any{float64(1), float64(2)}})
}

// TS: explicit-map-tag
func TestExplicitMapTag(t *testing.T) {
	expectEqual(t, y(t, "a: !!map\n  x: 1"),
		map[string]any{"a": map[string]any{"x": float64(1)}})
}

// ===== COMPLEX KEYS =====

// TS: multiline-key
func TestMultilineKey(t *testing.T) {
	expectEqual(t, y(t, "? a b\n: 1"), map[string]any{"a b": float64(1)})
}

// ===== INDENTATION EDGE CASES =====

// TS: tab-indentation-not-rejected (KNOWN NON-CONFORMANCE) — YAML forbids a
// tab as indentation, so this document must be REJECTED. It is not: the tab
// is swallowed and `b` lands at the top level. Pinned as observed so the gap
// is visible; the conformance dial for it is the yaml-test-suite must-fail
// group (yaml_test_suite_test.go).
func TestTabIndentationRejected(t *testing.T) {
	expectEqual(t, y(t, "a:\n\tb: 1"),
		map[string]any{"a": nil, "b": float64(1)})
}

// TS: blank-lines-between-pairs
func TestBlankLinesBetweenPairs(t *testing.T) {
	expectEqual(t, y(t, "a: 1\n\nb: 2"),
		map[string]any{"a": float64(1), "b": float64(2)})
}

// TS: blank-lines-in-list
func TestBlankLinesInList(t *testing.T) {
	expectEqual(t, y(t, "- a\n\n- b"), []any{"a", "b"})
}

// ===== SPECIAL CHARS IN VALUES =====

// TS: value-with-colon-no-space — e.g. URLs.
func TestValueWithColonNoSpace(t *testing.T) {
	expectEqual(t, y(t, "url: http://example.com"),
		map[string]any{"url": "http://example.com"})
}

// TS: value-with-brackets — plain scalar containing brackets.
func TestValueWithBrackets(t *testing.T) {
	expectEqual(t, y(t, "a: some [text] here"),
		map[string]any{"a": "some [text] here"})
}

// TS: value-with-braces — plain scalar containing braces.
func TestValueWithBraces(t *testing.T) {
	expectEqual(t, y(t, "a: some {text} here"),
		map[string]any{"a": "some {text} here"})
}
