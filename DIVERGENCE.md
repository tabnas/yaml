# Divergences

TypeScript is the canonical implementation; the Go and Rust ports track
it. This file records where a runtime produces a **different result for
the same input**, and why the difference is allowed to stand.

None of these can be written as a row of `test/spec/*.tsv`, which is why
this repository still has no divergence register: one is invisible to the
value those fixtures compare, two concern a diagnostic's position, which
no fixture pins, one needs a nesting depth far past anything a fixture
cell would hold, and two need a lone UTF-16 surrogate in an expected
cell, which a UTF-8 file cannot carry. A divergence a row could
express belongs in a register, with a `rust` column, per
[`AGENTS.md`](AGENTS.md).

Every entry is pinned by a test, so it fails on REPAIR as loudly as on
regression. Repairing one means deleting its entry here and its test in
the same change.

## No document and null are the same value (Go and Rust)

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `""` | `undefined` | `nil` | `Undefined` |
| `"   \n "` | `undefined` | `nil` | `Null` |
| `"..."` | `undefined` | `nil` | `Null` |
| `"# c\n..."` | `undefined` | `nil` | `Null` |
| `"null"` | `null` | `nil` | `Null` |

A YAML stream that accumulates no documents at all has no value, which
TypeScript spells `undefined` and tells apart from a document whose value
is `null`. Go has one `nil` and cannot. The Rust port tells them apart
for an empty source only, because an empty source returns
`options.lex.empty_result` straight from the engine; every other source
goes through the parse loop, whose result passes through
`Value::unwrap_undefined`, and that replaces every `Undefined` in the
tree with `null` before a caller sees it.

`...` and `# c\n...` are the two `UNDEFINED` rows in
`test/spec/multi-document.tsv`, so an `UNDEFINED` row cannot fail in
either port. The allowance is in `go/parity_test.go` and
`rs/tests/parity_test.rs`, and
`rs/tests/undefined_test.rs` pins the table above.

Owner: the engine. The repair is a way for a parse to return `Undefined`
to its caller; when it lands, delete this entry, both allowances and both
tests together.

## A column counts characters, not UTF-16 units (Go and Rust)

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `{a: xx, ]` | column 9 | 9 | 9 |
| `{a: é, ]` | column 8 | 8 | 8 |
| `{a: €, ]` | column 8 | 8 | 8 |
| `{a: 😀, ]` | column 9 | 8 | 8 |

Every diagnostic column in this plugin agrees with the canonical one
except after a character outside the Basic Multilingual Plane, which
TypeScript counts as two columns and the other two runtimes as one. It is
recorded upstream in `parser/DIVERGENCE.md` ("Column positions for astral
characters") and pinned here by `go/column_units_test.go`,
`ts/test/column-units.test.ts` and `rs/tests/column_units_test.rs`, all on
the same fifteen cases. A column is the one the token's offset gives: how
far the token stands past its line's start, from 1. Each runtime once
kept its columns up token by token, and from a `: ` onwards every one of
them ran one ahead, which is why this table once read a column higher in
every cell.

Owner: the engine. Nothing in this repository can repair it.

## Nesting past 127 containers is refused (Rust)

| input | TypeScript | Go | Rust |
|---|---|---|---|
| 127 nested `[` | a 127-deep list | the same | the same |
| 128 nested `[` | a 128-deep list | the same | `ERROR:cancel` |
| 1,000 nested block mappings | a 1,000-deep mapping | the same | `ERROR:cancel` |
| 128 compact sequences, `- - - x` | a 128-deep list | the same | `ERROR:cancel` |
| 128 sequences, each in the second entry of the one above | a 128-deep list | the same | `ERROR:cancel` |
| 64 levels of a mapping in a sequence, each in the second pair of the one above | 128 containers deep | the same | `ERROR:cancel` |

This plugin installs a parse guard that refuses nesting past 127
containers, jsonic's number, in place of the one `tabnas-jsonic`
installs under the same name. jsonic's counts only its own `map` and
`list` rules, so a compact block sequence, which nests through YAML's
own `yamlBlockList`, went unbounded under it. This one counts YAML's
block collections too, under whichever rule holds each one: a block
sequence opens as `yamlBlockList` and continues as `yamlBlockElem`, and
a mapping that starts in a sequence entry opens as `yamlElemMap` and
continues as `yamlElemPair`. A guard holds whatever parse budget the
caller sets. The engine parses iteratively, but displaying, converting
or dropping a `tabnas::Value` walks the tree with the call stack, so an
unbounded document ends the caller's process rather than returning an
error. TypeScript and Go have no limit, because neither runtime's value
type has that problem.

`rs/tests/untrusted_test.rs` and
`rs/tests/divergence_test.rs::nesting_past_the_depth_limit_is_refused`
pin both sides of the boundary.

Owner: this plugin, for the count; `tabnas-jsonic`, for the number. Read
jsonic's register for the measurements.

## A row after a carriage return by itself, on a number the engine cuts (Rust)

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: 1\rb: "x" 3` | `unexpected` at 2:8 | the same | at 1:13 |
| `a: 1\rb: [1] 3` | `unexpected` at 2:8 | the same | at 1:13 |
| `a: 1\r\rb: 'x' 3` | `unexpected` at 3:8 | the same | at 1:14 |
| `a: 1\nb: "x" 3` | `unexpected` at 2:8 | the same | the same |
| `a: 1\r\nb: "x" 3` | `unexpected` at 2:8 | the same | the same |
| `a: 1\rb: "x" y` | `unexpected` at 2:8 | the same | the same |
| `a: 1\rb: [1, }` | `unexpected` at 2:8 | the same | the same |

A token's row and column are the ones its offset gives, in every runtime,
for every token the plugin builds. A number that follows a quoted scalar
or a closed flow collection on its line is cut by the engine's own number
matcher, at the engine's cursor. TypeScript and Go set that cursor's row
and column from the offset before the engine's matchers run. The Rust
engine's lexer gives a matcher advancement and no assignment, and it
counts a row at each character of its `line.rowChars`, which is `\n`, so
after a carriage return by itself its cursor is still on row 1, with the
column counted from the source's start. Where such a number is what a
parse refuses, the diagnostic names the right code and the right offset,
and the engine's row and column.

The last four rows are the controls: the other two line breaks, where the
cursor counts the row, and tokens the plugin cuts itself after the same
carriage return. The value a document reads as is not affected, and a
document with any other line break is not affected at all.

Measured over 1,872 texts generated for the purpose, 12 openings each
followed by one of 52 last lines under each of the three line breaks, of
which the canonical implementation refuses 951: Go reports the canonical
place for all 951, and Rust for all but 122, every one of them a number
the engine cuts after a carriage return by itself. Over the texts this
repository holds, the fixture rows, the YAML Test Suite cases and the
schema inputs, as written and with their line breaks made `\r\n` and
`\r`, the 254 refusals stand at the canonical place in both.

`rs/tests/divergence_test.rs` pins all seven tabled rows.

Owner: the engine. The repair is a way for a matcher to place the Rust
lexer's cursor, as the other two engines allow. Standing in for the
engine's number matcher at every position instead would risk the values
numbers read as for the sake of a diagnostic's place, which is the worse
trade.

## UTF-16 escapes in a double quoted scalar (Go and Rust)

Written with `U+XXXX` for the characters the table is about, because a
lone surrogate has no UTF-8 spelling and a byte-order mark is invisible.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: "\uD83D\uDE00"` | U+1F600 | U+1F600 | U+1F600 |
| `a: "\U0000D83D\uDE00"` | U+1F600 | U+1F600 | U+1F600 |
| `a: "\uD83D"` | a lone high surrogate | U+FFFD | U+FFFD |
| `a: "\uDE00"` | a lone low surrogate | U+FFFD | U+FFFD |
| `a: "\U0000D83D"` | a lone high surrogate | U+FFFD | U+FFFD |
| `"\uD83D": 1`, the key | a lone high surrogate | U+FFFD | U+FFFD |
| `? "\uD83D"` then `: 1`, the key | a lone high surrogate | U+FFFD | U+FFFD |

The canonical handler builds the scalar with `String.fromCharCode` for
`\x` and `\u`, and `String.fromCodePoint` for `\U`. Both take a UTF-16
CODE UNIT for a surrogate value, so a high escape and the low escape
beside it are one astral character rather than two escapes, in either
spelling. Neither port's string type holds a code unit, so both pair
the two before converting (`push_code_point` in `rs/src/lex.rs`,
`pushCodeUnit` in `go/yaml.go`) and reach the canonical value. The
first two rows are the controls, and they are shared fixture rows in
`test/spec/quoted-strings.tsv`.

An UNPAIRED surrogate is the part neither port can reach. TypeScript
keeps the code unit, because a JavaScript string is UTF-16 and permits
one; Go and Rust fold it to the replacement character, because neither
string model has a place to put it. That is the same trade the engine
records for its own quoted strings, under "Lone surrogates in quoted
strings" in `parser/DIVERGENCE.md`, and the string model decides it
rather than this plugin.

A key is the same scalar, so it reads the same way, an implicit key
and an explicit one (`? "..."`) alike. The explicit key was once the
exception in every runtime: an escape that named half a pair left the
key as its quoted text, quotes and all, so that the three could share a
fixture row. It reads as the scalar now, and is recorded here with the
rest.

Provenance: the TypeScript column was produced by running
`ts/src/yaml.ts` under Node 22, and for the two key rows under Node 24;
the Go column by `tabnasyaml.Parse` in `go/`; the Rust column by
`tabnas_yaml::parse`. No row of
`test/spec/*.tsv` can carry the TypeScript answers for the unpaired
rows, because an expected cell is UTF-8 text and a lone surrogate has
no UTF-8 encoding. They are pinned by
`ts/test/yaml.test.ts` (`an-unpaired-surrogate-escape-is-its-code-unit`),
`rs/tests/js_semantics_test.rs::an_unpaired_surrogate_escape_folds` and,
for the Go column,
`go/divergence_test.go::TestAnUnpairedSurrogateEscapeFolds`.

Owner for the unpaired rows: the string model, as upstream.

The window itself is a fixed width read with `parseInt`, counted in
UTF-16 CODE UNITS, and an astral character fills two of them, so a
window can end INSIDE one. The canonical keeps the high surrogate in the
window, where it is no hexadecimal digit and ends `parseInt`'s prefix,
and the cursor then lands on the low surrogate, which the scan appends
as a character of its own. That is one more unpaired surrogate neither
port can hold, and both fold it the same way (`take_units` in
`rs/src/lex.rs`, `utf16Window` in `go/yaml.go`). Only the fold differs:
the window, the cursor and every window that ends on a character
boundary are the canonical ones, and those are shared fixture rows in
`test/spec/quoted-strings.tsv`, the refusals `String.fromCodePoint`
throws for included.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: "\xA` + U+1F600 + `Z"` | U+000A U+DE00 `Z` | U+000A U+FFFD `Z` | U+000A U+FFFD `Z` |
| `a: "\u004` + U+1F600 + `Z"` | U+0004 U+DE00 `Z` | U+0004 U+FFFD `Z` | U+0004 U+FFFD `Z` |
| `a: "\U0000004` + U+1F600 + `Z"` | U+0004 U+DE00 `Z` | U+0004 U+FFFD `Z` | U+0004 U+FFFD `Z` |
| `a: "\U000D83D` + U+1F600 + `Z"` | U+1F600 `Z` | U+1F600 `Z` | U+1F600 `Z` |
| `a: "\xA` + U+4E2D + `Z"` | U+000A `Z` | U+000A `Z` | U+000A `Z` |

The last two rows are the controls, and the first of them is the one
that is not a trade at all: where the escape itself names a high
surrogate, the half the cut leaves completes the pair and both runtimes
reach the whole astral character. The second shows that only an ASTRAL
character can be cut, since a character inside the Basic Multilingual
Plane is one unit and one scalar. Measured the same way as the tables
above, and pinned by
`rs/tests/escape_window_test.rs::a_window_cut_through_an_astral_character_folds_the_half_it_leaves`
and, for the Go column,
`go/divergence_test.go::TestASplitAstralEscapeWindowFoldsTheHalfItLeaves`,
whose control rows fail if the window or the cursor drifts and whose
first three rows fail if the fold changes. Owner: the string model, as
upstream, for both ports.

## An unterminated typed tag folds a split astral character (Go and Rust)

Written with `U+XXXX` again, for the same reason.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: !!str "` + U+1F600 | U+D83D | U+FFFD | U+FFFD |
| `a: !!str "a` + U+1F600 | `a` then U+D83D | `a` then U+FFFD | `a` then U+FFFD |
| `a: !!str '` + U+1F600 | U+D83D | U+FFFD | U+FFFD |
| `a: !!str "` + U+4E2D U+6587 | U+4E2D | U+4E2D | U+4E2D |
| `a: !!str "` | `"` | `"` | `"` |

An unterminated quoted value after a typed tag ends at the end of the
source, and the canonical handler then takes
`fwd.substring(valStart + 1, valEnd - 1)`: everything up to the last
UTF-16 CODE UNIT. An astral character is two of those units and one Rust
scalar or Go rune, so dropping one unit leaves a LONE HIGH SURROGATE,
which neither string type has a place for. Both ports fold it to the
replacement character, which is what they do with every other unpaired
surrogate (`js_substring_less_one_unit` in `rs/src/lex.rs`,
`jsSubstringLessOneUnit` in `go/yaml.go`). Only the astral rows differ;
the rest of the table is the control, the reversed-index row included,
where `substring` swaps its arguments and yields the quote itself, and
the control rows are shared fixture rows in `test/spec/tags.tsv`.

This is the same trade as "UTF-16 escapes in a double quoted scalar"
above, in a different handler, and it has the same owner: the string
model, as upstream.

There is a second repair, and it is the better one: the canonical
truncation is itself hard to defend. Nothing about an unterminated
`!!str "abc` calls for the value `ab`, and the `valEnd - 1` that
produces it is the same arithmetic whether or not a closing quote was
found. Fixing the CANONICAL to drop the final unit only when it closed
the value would retire this entry outright, and the astral half
character with it. That is a change to `ts/src/yaml.ts` and to every
port, so it is named here rather than made here.

Provenance: the TypeScript column was produced by running
`ts/src/yaml.ts` under Node 22; the Go column by `tabnasyaml.Parse` in
`go/`; the Rust column by `tabnas_yaml::parse`. No row of
`test/spec/*.tsv` can carry the TypeScript answers, because an expected
cell is UTF-8 text and a lone surrogate has no UTF-8 encoding. Pinned by
`rs/tests/divergence_test.rs::an_unterminated_typed_tag_folds_a_split_astral_character`
and, for the Go column,
`go/divergence_test.go::TestAnUnterminatedTypedTagFoldsASplitAstralCharacter`,
whose control rows fail if the Basic Multilingual Plane cases drift and
whose astral rows fail if the fold changes.

Owner: the string model, as upstream, for both ports.
