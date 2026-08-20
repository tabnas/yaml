// Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License

package tabnasyaml

// Error COLUMNS after a non-ASCII character.
//
// This plugin brings its own matchers, and a plugin that does owns the
// arithmetic the engine's matchers do for it: SI advances in BYTES and CI
// in CHARACTERS. Forty `pnt.CI` sites here advanced it by a byte
// quantity, so a 2-byte `é` charged two columns, a 3-byte `€` three and
// an astral character four — every diagnostic after a non-ASCII
// character reported a column past where the problem was.
//
// The TypeScript half writes the SAME EXPRESSIONS over UTF-16 indices,
// where they do count characters:
//
//	pnt.CI += nameEnd     // Go: nameEnd is a byte index
//	pnt.cI += nameEnd     // TS: nameEnd is a UTF-16 index
//
// The two lines look identical, which is why this survived a port
// review. A transliteration is not a port when the two languages index
// strings differently.
//
// Found by the fleet parity probe; the same defect was repaired in
// tabnas/toml#51 and tabnas/xml#44.
//
// ts/test/column-units.test.ts asserts the same sixteen cases. Fifteen
// of them agree exactly; the astral row is the recorded engine
// divergence — TypeScript counts UTF-16 units (an astral character is
// 2), Go counts runes (1). See parser/DIVERGENCE.md, "Column positions
// for astral characters". Note that BEFORE this repair that row was off
// by three rather than one: a live defect hiding behind a recorded one.

import (
	"encoding/json"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
)

func TestErrorColumnsCountCharactersNotBytes(t *testing.T) {
	for _, c := range []struct {
		label  string
		src    string
		col    int // this port
		ts     int // the TypeScript half, for the reader
		before int // what this port answered before the repair
	}{
		// Controls: ASCII only, where bytes and characters coincide.
		// Without them, "columns count characters" is also satisfied by
		// never counting.
		{"inline-ascii", "{a: xx, ]", 10, 10, 10},
		{"block-key-ascii", "a: 1\n]", 1, 1, 1},

		// The plain-scalar path — where the bug actually lived, traced by
		// instrumenting every CI site rather than guessed.
		{"inline-latin1", "{a: é, ]", 9, 9, 10},
		{"inline-bmp", "{a: €, ]", 9, 9, 11},

		// A non-ASCII KEY, which reaches a different site again.
		{"key-latin1", "{é: 1, ]", 9, 9, 10},
		{"key-bmp", "{€€: 1, ]", 10, 10, 14},

		// Quoted scalars: their own two handlers, each with its own
		// column arithmetic.
		{"dq-latin1", `{a: "é", ]`, 11, 11, 12},
		{"dq-bmp", `{a: "€€", ]`, 12, 12, 16},
		{"sq-latin1", "{a: 'é', ]", 11, 11, 12},

		// Anchors and tags: two more handlers.
		{"anchor-latin1", "{a: &x é, ]", 12, 12, 13},
		{"tag-latin1", "{a: !!str é, ]", 15, 15, 16},

		// The flow-collection newline skip. Not a unit error but a
		// CONSTANT: this port set the column to 0 — not a valid 1-based
		// column at all — where TypeScript computes the characters since
		// the last newline. A newline followed by three spaces puts the
		// next token at column 4, and this port said 1 whatever the
		// indent.
		{"flow-nl-1sp", "[a,\n }", 2, 2, 1},
		{"flow-nl-3sp", "[a,\n   }", 4, 4, 1},
		{"flow-nl-latin1", "[é,\n }", 2, 2, 1},

		// The recorded divergence, and the ONLY row where the two ports
		// still differ. Before the repair it was off by three, so the
		// real defect was hidden inside a difference the register
		// already excused.
		{"inline-astral", "{a: \U0001F600, ]", 9, 10, 12},
	} {
		j := jsonic.Make(jsonic.Options{})
		if err := j.Use(Yaml, map[string]any{}); err != nil {
			t.Fatalf("%s: use: %v", c.label, err)
		}
		_, perr := j.Parse(c.src)
		if perr == nil {
			t.Errorf("%s: %q parsed, expected a diagnostic", c.label, c.src)
			continue
		}
		b, mErr := json.Marshal(perr)
		if mErr != nil {
			t.Fatalf("%s: marshal: %v", c.label, mErr)
		}
		var o struct {
			Col int `json:"col"`
		}
		if uErr := json.Unmarshal(b, &o); uErr != nil {
			t.Fatalf("%s: unmarshal: %v", c.label, uErr)
		}
		if o.Col != c.col {
			t.Errorf("%s: %q col = %d, want %d (TypeScript says %d; this "+
				"port said %d before the repair). A column ahead of the "+
				"want by the character's extra BYTES means a CI site is "+
				"counting bytes again.",
				c.label, c.src, o.Col, c.col, c.ts, c.before)
		}
	}
}
