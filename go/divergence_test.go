// Copyright (c) 2026 Richard Rodger and other contributors, MIT License

package tabnasyaml

// The Go column of ../DIVERGENCE.md, made executable.
//
// Every entry in that file is a row this port answers differently from
// the canonical TypeScript, and a register that is only prose records a
// claim rather than a behaviour: it cannot fail when the port drifts,
// and it cannot fail when the port is REPAIRED, which is the moment the
// entry has to be deleted. The Rust half of each table is pinned in
// rs/tests, and the Go half was not pinned anywhere until this file.
//
// None of these rows can be a shared test/spec/*.tsv fixture. An
// expected cell is UTF-8 text and the TypeScript answers here are lone
// UTF-16 surrogates, which have no UTF-8 spelling; the last test is a
// case the canonical REFUSES and this port accepts, and a fixture row
// carries one answer for all three runtimes.
//
// Measured on 2026-09-22: the TypeScript column by running
// ts/src/yaml.ts under Node 22, the Go column by Parse below, the Rust
// column by tabnas_yaml::parse.
//
// A test here failing because the port now AGREES with the canonical is
// the good direction, and the response is to delete the row, its entry
// in ../DIVERGENCE.md and the matching Rust pin together.

import (
	"encoding/json"
	"testing"
)

// parseJSON parses src and returns the result as JSON text.
//
// JSON text rather than a Go value, because these rows are about which
// CHARACTERS end up in a string, and a %#v of a map hides them behind
// escape spellings that differ between the two ports.
func parseJSON(t *testing.T, src string) string {
	t.Helper()
	value, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	encoded, merr := json.Marshal(value)
	if merr != nil {
		t.Fatalf("marshal %q: %v", src, merr)
	}
	return string(encoded)
}

// TestAnUnpairedSurrogateEscapeFolds pins the first table of "UTF-16
// escapes in a double quoted scalar".
//
// A `\u` escape is String.fromCharCode, which appends a UTF-16 CODE
// UNIT, and a JavaScript string holds an unpaired one. A Go string holds
// Unicode scalars, so this port folds the half to the replacement
// character. The controls are the paired rows, which reach the canonical
// astral character and are shared fixtures besides: without them "every
// escape folds" would also satisfy this test.
func TestAnUnpairedSurrogateEscapeFolds(t *testing.T) {
	for _, row := range []struct {
		src  string
		want string
		ts   string // for the reader: the canonical answer
	}{
		{`a: "\uD83D"`, "{\"a\":\"\ufffd\"}", "a lone high surrogate"},
		{`a: "\uDE00"`, "{\"a\":\"\ufffd\"}", "a lone low surrogate"},
		{`a: "\U0000D83D"`, "{\"a\":\"\ufffd\"}", "a lone high surrogate"},

		// The controls: a pair is one astral character in both ports.
		{`a: "\uD83D\uDE00"`, "{\"a\":\"\U0001F600\"}", "U+1F600"},
		{`a: "\U0000D83D\uDE00"`, "{\"a\":\"\U0001F600\"}", "U+1F600"},
	} {
		if got := parseJSON(t, row.src); got != row.want {
			t.Errorf("%q = %s, want %s (TypeScript gives %s). A repair here "+
				"retires the first table of \"UTF-16 escapes in a double "+
				"quoted scalar\" in ../DIVERGENCE.md", row.src, got, row.want, row.ts)
		}
	}
}

// TestASplitAstralEscapeWindowFoldsTheHalfItLeaves pins the second table
// of the same entry.
//
// The window a `\x`, `\u` or `\U` escape reads is a fixed width counted
// in UTF-16 CODE UNITS, so it can end INSIDE an astral character. The
// canonical keeps the high surrogate inside the window, where it is no
// hexadecimal digit and ends parseInt's prefix, and the scan then
// appends the low surrogate as a character of its own. This port folds
// that half, as it folds every unpaired surrogate.
//
// The controls are the last two rows, and they are the reason this is a
// fold and not a window error: where the escape names a high surrogate
// itself the cut half COMPLETES the pair and the whole astral character
// survives, and a character inside the Basic Multilingual Plane is one
// unit and cannot be cut at all. Both would break if the window or the
// cursor drifted by a unit.
func TestASplitAstralEscapeWindowFoldsTheHalfItLeaves(t *testing.T) {
	for _, row := range []struct {
		src  string
		want string
		ts   string
	}{
		{"a: \"\\xA\U0001F600Z\"", "{\"a\":\"\\n\ufffdZ\"}", "U+000A U+DE00 Z"},
		{"a: \"\\u004\U0001F600Z\"", "{\"a\":\"\\u0004\ufffdZ\"}", "U+0004 U+DE00 Z"},
		{"a: \"\\U0000004\U0001F600Z\"", "{\"a\":\"\\u0004\ufffdZ\"}", "U+0004 U+DE00 Z"},

		// The controls, which agree with the canonical.
		{"a: \"\\U000D83D\U0001F600Z\"", "{\"a\":\"\U0001F600Z\"}", "U+1F600 Z"},
		{"a: \"\\xA\u4E2DZ\"", "{\"a\":\"\\nZ\"}", "U+000A Z"},
	} {
		if got := parseJSON(t, row.src); got != row.want {
			t.Errorf("%q = %s, want %s (TypeScript gives %s). A control row "+
				"failing means the WINDOW or the cursor moved, not the fold",
				row.src, got, row.want, row.ts)
		}
	}
}

// TestAnUnterminatedTypedTagFoldsASplitAstralCharacter pins the entry of
// that name.
//
// An unterminated quoted value after a typed tag runs to the end of the
// source, and the canonical then takes everything up to the last UTF-16
// code unit. An astral character is two of those units and one Go rune,
// so dropping one unit leaves a lone high surrogate, which this port
// folds.
//
// The controls are the last two rows: a Basic Multilingual Plane
// character is one unit, so the drop takes the whole of it, and an
// empty value leaves the substring's indices reversed, where the
// canonical swaps them and yields the quote itself.
func TestAnUnterminatedTypedTagFoldsASplitAstralCharacter(t *testing.T) {
	for _, row := range []struct {
		src  string
		want string
		ts   string
	}{
		{"a: !!str \"\U0001F600", "{\"a\":\"\ufffd\"}", "U+D83D"},
		{"a: !!str \"a\U0001F600", "{\"a\":\"a\ufffd\"}", "a then U+D83D"},
		{"a: !!str '\U0001F600", "{\"a\":\"\ufffd\"}", "U+D83D"},

		// The controls, which agree with the canonical.
		{"a: !!str \"\u4E2D\u6587", "{\"a\":\"\u4E2D\"}", "U+4E2D"},
		{"a: !!str \"", `{"a":"\""}`, `"`},
	} {
		if got := parseJSON(t, row.src); got != row.want {
			t.Errorf("%q = %s, want %s (TypeScript gives %s)",
				row.src, got, row.want, row.ts)
		}
	}
}

// TestATabOnlyTailAtTheEndOfTheSourceIsAccepted pins "A tab-only tail at
// the end of the source".
//
// The canonical's keyword and number branches advance by the TRIMMED
// text, so a tab after such a value stays in the source; the matcher's
// blank-line skip then consumes it, and the document is refused anyway,
// at the end of the source. Which step refuses it is not isolated:
// holding the row and the column still over that skip changes no row of
// the table. This port has no such branch, the tail reaches jsonic's own
// lexer, which reads a tab as a space, and the document ends.
//
// The controls are the rows both runtimes accept: the same tab with a
// newline after it, which is a blank line everywhere, a trailing SPACE,
// which enters no such branch, and a plain scalar, whose own handler has
// consumed the tab before the branch is reached.
func TestATabOnlyTailAtTheEndOfTheSourceIsAccepted(t *testing.T) {
	for _, row := range []struct {
		src  string
		want string
	}{
		{"a: 1\t", `{"a":1}`},
		{"a: true\t", `{"a":true}`},
		{"a: null\t", `{"a":null}`},
		{"a: [1]\t", `{"a":[1]}`},
		{"- 1\t", `[1]`},
		{"a: 1 \t", `{"a":1}`},

		// The controls, which the canonical accepts too.
		{"a: 1\t\nb: 2", `{"a":1,"b":2}`},
		{"a: 1 ", `{"a":1}`},
		{"a: x\t", `{"a":"x"}`},
	} {
		if got := parseJSON(t, row.src); got != row.want {
			t.Errorf("%q = %s, want %s", row.src, got, row.want)
		}
	}

	// The divergence itself: the canonical REFUSES the six rows above,
	// and this port must keep answering them until the canonical is
	// repaired. A control row failing is a plain regression; a first-group
	// row failing means this port now refuses them too, and the entry in
	// ../DIVERGENCE.md is the thing to delete.
	if _, err := Parse("a: 1\t"); err != nil {
		t.Fatalf("`a: 1<TAB>` is refused here now (%v), which is the "+
			"canonical answer: delete \"A tab-only tail at the end of the "+
			"source\" from ../DIVERGENCE.md and this test with it", err)
	}
}
