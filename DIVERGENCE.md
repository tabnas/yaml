# Divergences

TypeScript is the canonical implementation; the Go and Rust ports track
it. This file records where a runtime produces a **different result for
the same input**, and why the difference is allowed to stand.

None of these can be written as a row of `test/spec/*.tsv`, which is why
this repository still has no divergence register: one is invisible to the
value those fixtures compare, two concern a diagnostic's position, which
no fixture pins, one needs a nesting depth far past anything a fixture
cell would hold, and one needs a lone UTF-16 surrogate in an expected
cell, which a UTF-8 file cannot carry. The last entry is a Go defect
rather than a standing difference: it is written down so a shared row is
not added over it before the repair lands. A divergence a row could
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
| `{a: xx, ]` | column 10 | 10 | 10 |
| `{a: é, ]` | column 9 | 9 | 9 |
| `{a: €, ]` | column 9 | 9 | 9 |
| `{a: 😀, ]` | column 10 | 9 | 9 |

Every diagnostic column in this plugin agrees with the canonical one
except after a character outside the Basic Multilingual Plane, which
TypeScript counts as two columns and the other two runtimes as one. It is
recorded upstream in `parser/DIVERGENCE.md` ("Column positions for astral
characters") and pinned here by `go/column_units_test.go`,
`ts/test/column-units.test.ts` and `rs/tests/column_units_test.rs`, all on
the same sixteen cases.

Owner: the engine. Nothing in this repository can repair it.

## Nesting past 127 containers is refused (Rust)

| input | TypeScript | Go | Rust |
|---|---|---|---|
| 127 nested `[` | a 127-deep list | the same | the same |
| 128 nested `[` | a 128-deep list | the same | `ERROR:cancel` |
| 1,000 nested block mappings | a 1,000-deep mapping | the same | `ERROR:cancel` |

`tabnas-jsonic` installs a parse budget that refuses nesting past 127
containers, and this plugin inherits it unchanged. The engine parses
iteratively, but displaying, converting or dropping a `tabnas::Value`
walks the tree with the call stack, so an unbounded document ends the
caller's process rather than returning an error. TypeScript and Go have
no limit, because neither runtime's value type has that problem.

`rs/tests/untrusted_test.rs` and
`rs/tests/divergence_test.rs::nesting_past_the_depth_limit_is_refused`
pin both sides of the boundary.

Owner: `tabnas-jsonic`. Read its own register for the measurements; this
plugin neither raises nor lowers the limit.

## The matcher chain after a cursor move (Rust)

Both engines dispatch their lexer's matcher chain on the FIRST character
of a call: only the matchers that could produce a token starting there
are run. A plugin matcher that moves the cursor and then produces nothing
therefore leaves the new position to a matcher set chosen for the old
one. That is not incidental: the canonical matcher emits a flow
collection's opener itself after an anchor or a tag for exactly this
reason. But the two chains are not built the same way, so what remains
available differs.

Most of the difference is compensated for: `rs/src/lex.rs` refuses a
character the canonical chain would have left unclaimed
(`claimable_after_move`), and `rs/src/text.rs` stands in for the number
matcher, which the canonical chain reaches after a move and this one does
not.

What is left, over a differential run of about 12,700 generated documents
against the canonical TypeScript, is ONE document whose value differs and
THIRTEEN that all three runtimes refuse with the same code at a different
place. Two of the thirteen are tabled below as the representatives.

**The value difference:**

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `!!int%TAG !! x:\t\n... ... %YAML 1.20o17 ` | `["!! x:", "... %YAML 1.20o17"]` | the same | `[{"!! x": null}, "... %YAML 1.20o17"]` |

**Two of the thirteen position differences, where all three runtimes
refuse the document with the same code and only the reported place
differs:**

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `[a, b\n{a: 1\n? k\n- x\n` | `unexpected` at 5:1 | 5:1 | 3:1 |
| `---\n{a: 1\na: 1\n  t2\n` | `unexpected` at 5:1 | 5:1 | 4:3 |

`rs/tests/divergence_test.rs` pins all three tabled rows. No shared
fixture pins a diagnostic's position, and none of these documents is
valid YAML. The same differential run over 8,408 generated documents of
realistic YAML shape, over the 253 fixture rows, over the 402 conformance
inputs and over every string literal in the Go test files found no
difference at all beyond the astral column above.

Owner: this port. The repair is to reproduce the canonical chain's number
matcher exactly rather than standing in for it, which means porting
`Lexer::match_number` and its options; that is a larger change than the
two shapes justify today.

## UTF-16 escapes in a double quoted scalar (Go and Rust)

Written with `U+XXXX` for the characters the table is about, because a
lone surrogate has no UTF-8 spelling and a byte-order mark is invisible.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: "\uD83D\uDE00"` | U+1F600 | U+FFFD U+FFFD | U+1F600 |
| `a: "\U0000D83D\uDE00"` | U+1F600 | U+FFFD U+FFFD | U+1F600 |
| `a: "\uD83D"` | a lone high surrogate | U+FFFD | U+FFFD |
| `a: "\uDE00"` | a lone low surrogate | U+FFFD | U+FFFD |

The canonical handler builds the scalar with `String.fromCharCode`,
which appends one UTF-16 CODE UNIT, so a high escape and the low escape
beside it are one astral character rather than two escapes. A Rust
string holds Unicode scalars, so this port pairs the two before
converting (`push_code_point` in `rs/src/lex.rs`) and reaches the
canonical value. The Go port converts each escape on its own with
`string(rune(n))`, which folds every surrogate to U+FFFD, so the pair is
lost there.

An UNPAIRED surrogate is the part neither port can reach. TypeScript
keeps the code unit, because a JavaScript string is UTF-16 and permits
one; Go and Rust fold it to the replacement character, because neither
string model has a place to put it. That is the same trade the engine
records for its own quoted strings, under "Lone surrogates in quoted
strings" in `parser/DIVERGENCE.md`, and the string model decides it
rather than this plugin.

Provenance: the TypeScript column was produced by running
`ts/src/yaml.ts` under Node 22; the Go column by `tabnasyaml.Parse` in
`go/`; the Rust column by `tabnas_yaml::parse`. No row of
`test/spec/*.tsv` can carry the TypeScript answers, because an expected
cell is UTF-8 text and a lone surrogate has no UTF-8 encoding. The pair
rows are pinned by
`rs/tests/js_semantics_test.rs::a_surrogate_pair_escape_is_one_character`
and the unpaired rows by
`rs/tests/js_semantics_test.rs::an_unpaired_surrogate_escape_folds`.

Owner for the unpaired rows: the string model, as upstream. Owner for
the pair rows: the Go port, where they are a defect and not a trade.

## JavaScript whitespace and word characters (Go)

`NL` below is the newline a source carries, and `U+XXXX` again stands
for a character the table is about.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: !!float U+FEFF 1` | `1` | `"U+FEFF 1"` | `1` |
| `a: !!float U+0085 1` | `NaN` | `"1"` | `NaN` |
| `a: !!python/ [1]` | `ERROR:unexpected` | `{"a":[1]}` | `ERROR:unexpected` |
| `U+FEFF` alone | `null` | `"U+FEFF"` | `null` |
| `U+0085` alone | `"U+0085"` | `null` | `"U+0085"` |
| `&n U+FEFF v: 1 NL b: *n` | `{"U+FEFF v":1,"b":"v"}` | `{"U+FEFF v":1,"b":"U+FEFF v"}` | `{"U+FEFF v":1,"b":"v"}` |
| `%TAG U+FEFF !! t: NL --- !!int 007` | `"007"` | `7` | `"007"` |

The canonical plugin is JavaScript, so its `\s` is the `White_Space`
property MINUS U+0085 and PLUS U+FEFF, and its `\w` and `\b` are ASCII.
Go's `unicode.IsSpace` and `strings.TrimSpace` answer the other way on
both characters, which is what every row above measures: a tagged
number's `parseFloat`, the `\b` on the `python/` structural tag, the
whitespace-only source test, the trim on an inline anchor's scalar, and
the `^%TAG\s+(\S+)\s+(\S+)` split. This port spells the JavaScript rule
out as `crate::js_space` and reaches the canonical answer in each.

Provenance: the TypeScript column was produced by running
`ts/src/yaml.ts` under Node 22; the Go column by `tabnasyaml.Parse` in
`go/`; the Rust column by `tabnas_yaml::parse`. Pinned by
`rs/tests/js_semantics_test.rs`, one test per row group. The one case of
this shape the Go port already answers correctly, an explicit key tagged
`!!` plus a non-ASCII name, is a shared fixture row instead, in
`test/spec/complex-keys.tsv`.

Owner: the Go port. These are defects there, not trades, and the entry
exists so a shared fixture row is not added over them before the repair
lands. Delete this entry, and move its rows into `test/spec/*.tsv`, when
Go answers them the canonical way.
