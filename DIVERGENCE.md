# Divergences

TypeScript is the canonical implementation; the Go and Rust ports track
it. This file records where a runtime produces a **different result for
the same input**, and why the difference is allowed to stand.

None of these can be written as a row of `test/spec/*.tsv`, which is why
this repository still has no divergence register: one is invisible to the
value those fixtures compare, two concern a diagnostic's position, which
no fixture pins, one needs a nesting depth far past anything a fixture
cell would hold, and two need a lone UTF-16 surrogate in an expected
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
against the canonical TypeScript, is THIRTEEN documents that all three
runtimes refuse with the same code at a different place. Two of the
thirteen are tabled below as the representatives.

**Two of the thirteen position differences, where all three runtimes
refuse the document with the same code and only the reported place
differs:**

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `[a, b\n{a: 1\n? k\n- x\n` | `unexpected` at 5:1 | 5:1 | 3:1 |
| `---\n{a: 1\na: 1\n  t2\n` | `unexpected` at 5:1 | 5:1 | 4:3 |

**And ONE document the other two runtimes accept and this one refuses,
found by a later sweep over tab placement:**

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `? k\n: <TAB>\n` | `{"k": null}` | the same | `ERROR:unexpected` |
| `? k\n: <TAB>` | `{"k": null}` | the same | `ERROR:unexpected` |
| `? k\n:   <TAB>\n` | `{"k": null}` | the same | `ERROR:unexpected` |
| `? k\n:  \n` | `{"k": null}` | the same | the same |
| `? k\n: <TAB>v\n` | `{"k": "v"}` | the same | the same |

The last two rows are the controls: spaces alone in the same place agree,
and so does a tab with a value after it. A line of nothing but blanks
that contains a TAB is skipped by both matchers, because YAML counts it
blank and the engine refuses a bare tab; the canonical then moves its
point and returns no token, and the engine's own matchers take over at
the end of the source. This port returns `Act::refuse` instead, at the
end-of-source guard in `decide`, and the parse stops one token short of
the value the explicit key owes. Making that guard refuse only when
nothing was consumed is NOT the repair: it was measured, and it makes
`a: !é` reach the engine with an `internal` error, which
`rs/tests/untrusted_test.rs` catches.

`rs/tests/divergence_test.rs` pins all five tabled rows. No shared
fixture pins a diagnostic's position, and none of the first two
documents is valid YAML. The same differential run over 8,408 generated
documents of realistic YAML shape, over the 253 fixture rows, over the
402 conformance inputs and over every string literal in the Go test
files found no difference at all beyond the astral column above; the
`? k` row came from a later sweep of 862 documents that put a tab at
every position of 35 constructs, and it is the only difference that
sweep found.

This entry once carried a value difference as well,
`!!int%TAG !! x:\t\n... ... %YAML 1.20o17`, and it was never the matcher
chain: the typed tag's value scan ended at the colon because a TAB
followed it, where the canonical ends there only before a literal space,
a line end or the end of the source. All three runtimes now read the
first document as the string `!! x:`, the input is a row of
`test/spec/directives.tsv`, and
`rs/tests/divergence_test.rs::a_tag_before_a_directive_line_resolves_the_canonical_way`
keeps the reason beside it. A row tabled here as impossible was a
one-character predicate; the neighbouring position rows are worth
re-measuring in the same spirit.

Owner: this port. The repair is to reproduce the canonical chain's number
matcher exactly rather than standing in for it, which means porting
`Lexer::match_number` and its options; that is a larger change than the
three shapes justify today.

## UTF-16 escapes in a double quoted scalar (Go and Rust)

Written with `U+XXXX` for the characters the table is about, because a
lone surrogate has no UTF-8 spelling and a byte-order mark is invisible.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: "\uD83D\uDE00"` | U+1F600 | U+FFFD U+FFFD | U+1F600 |
| `a: "\U0000D83D\uDE00"` | U+1F600 | U+FFFD U+FFFD | U+1F600 |
| `a: "\uD83D"` | a lone high surrogate | U+FFFD | U+FFFD |
| `a: "\uDE00"` | a lone low surrogate | U+FFFD | U+FFFD |
| `a: "\U0000D83D"` | a lone high surrogate | U+FFFD | U+FFFD |

The canonical handler builds the scalar with `String.fromCharCode` for
`\x` and `\u`, and `String.fromCodePoint` for `\U`. Both take a UTF-16
CODE UNIT for a surrogate value, so a high escape and the low escape
beside it are one astral character rather than two escapes, in either
spelling. A Rust string holds Unicode scalars, so this port pairs the
two before converting (`push_code_point` in `rs/src/lex.rs`) and reaches
the canonical value. The Go port converts each escape on its own with
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

A MALFORMED escape is a Go defect in the same handler, recorded here so
a shared fixture row is not added over it. The canonical window is a
fixed width read with `parseInt`, and what it yields goes to
`fromCharCode`, which takes `ToUint16` and turns a `NaN` into NUL, or to
`fromCodePoint`, which THROWS and so refuses the document. This port
matches both; Go drops the backslash and keeps the rest as text.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: "\x4"` | U+0004 | `"x4"` | U+0004 |
| `a: "\xZZ"` | U+0000 | `"xZZ"` | U+0000 |
| `a: "\u00"` | U+0000 | `"u00"` | U+0000 |
| `a: "\U 0000041"` | `"A"` | `"U 0000041"` | `"A"` |
| `a: "\U0010FFFF"` | U+10FFFF | U+10FFFF | U+10FFFF |
| `a: "\U00110000"` | `ERROR:unexpected` | U+FFFD | `ERROR:unexpected` |
| `a: "\UFFFFFFFF"` | `ERROR:unexpected` | `"UFFFFFFFF"` | `ERROR:unexpected` |

Measured the same way as the table above. The Rust rows are pinned by
`rs/tests/js_semantics_test.rs::a_hexadecimal_escape_window_is_parse_int`
and
`rs/tests/js_semantics_test.rs::an_escape_past_the_last_code_point_is_refused`.
Owner: the Go port.

The window itself is counted in UTF-16 CODE UNITS, and an astral
character fills two of them, so a window can end INSIDE one. The
canonical keeps the high surrogate in the window, where it is no
hexadecimal digit and ends `parseInt`'s prefix, and the cursor then
lands on the low surrogate, which the scan appends as a character of its
own. That is one more unpaired surrogate this port cannot hold, and it
folds the same way. Only the fold differs: the window and the cursor are
the canonical ones.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: "\xA` + U+1F600 + `Z"` | U+000A U+DE00 `Z` | `"xA"` + U+1F600 + `Z` | U+000A U+FFFD `Z` |
| `a: "\u004` + U+1F600 + `Z"` | U+0004 U+DE00 `Z` | `"u004"` + U+1F600 + `Z` | U+0004 U+FFFD `Z` |
| `a: "\U0000004` + U+1F600 + `Z"` | U+0004 U+DE00 `Z` | `"U0000004"` + U+1F600 + `Z` | U+0004 U+FFFD `Z` |
| `a: "\U000D83D` + U+1F600 + `Z"` | U+1F600 `Z` | `"U000D83D"` + U+1F600 + `Z` | U+1F600 `Z` |
| `a: "\xA` + U+4E2D + `Z"` | U+000A `Z` | `"xA"` + U+4E2D + `Z` | U+000A `Z` |

The last two rows are the controls, and the first of them is the one
that is not a trade at all: where the escape itself names a high
surrogate, the half the cut leaves completes the pair and both runtimes
reach the whole astral character. The second shows that only an ASTRAL
character can be cut, since a character inside the Basic Multilingual
Plane is one unit and one scalar. Measured the same way as the tables
above, and pinned by
`rs/tests/escape_window_test.rs::a_window_cut_through_an_astral_character_folds_the_half_it_leaves`,
whose control rows fail if the window or the cursor drifts and whose
first three rows fail if the fold changes. Owner: the string model, as
upstream, for the Rust column; the Go port for the Go column.

## A tab after a document marker inside a block scalar (Go)

The canonical writes its `---` and `...` test out four times, and three
of the four take a tab after the marker. The fourth, the one that stops
a BLOCK SCALAR (`ts/src/yaml.ts` line 576), takes only a space, a line
end or the end of the source, so a `---<TAB>` line stays inside the
scalar there and ends the document everywhere else. The Go port routes
all four through one `isDocMarker` helper, and that helper takes a tab.

Each cell of the input column is one line, with a newline after each.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `\|`, `x`, `---<TAB>y` | `"x\n---\ty\n"` | `["x\n", "y"]` | `"x\n---\ty\n"` |
| `\|`, `---<TAB>y` | `"---\ty\n"` | `["", "y"]` | `"---\ty\n"` |
| `\|`, `...<TAB>y` | `"...\ty\n"` | `["", "y"]` | `"...\ty\n"` |
| `\|`, `--- y` | `["", "y"]` | the same | the same |
| `a: 1`, `---<TAB>b: 2` | `[{"a":1},{"b":2}]` | the same | the same |

The last two rows are the controls: a SPACE after the marker ends the
scalar in all three, and the marker test OUTSIDE a block scalar takes a
tab in all three, so the repair moves one site and not four.

Provenance: measured 2026-09-21, the TypeScript column by running
`ts/src/yaml.ts` under Node 22, the Go column by `tabnasyaml.Parse` in
`go/`, the Rust column by `tabnas_yaml::parse`. Not a shared fixture
row, because Go is red on the first three; the controls ARE expressible
and agree, and they sit beside the divergent rows in
`rs/tests/blank_predicates_test.rs::a_tab_after_a_document_marker_stays_inside_a_block_scalar`,
which fails on repair as loudly as on regression.

Owner: the Go port. The repair is a second helper beside `isDocMarker`
that leaves the tab out, used at the one call site in the block-scalar
handler (`go/yaml.go` line 1725). Delete this entry and move its rows
into `test/spec/block-scalars.tsv` when that lands. A neighbouring Go
site, the end-of-scalar lookahead at `go/yaml.go` line 1775, is a
DIFFERENT defect of the same family: the canonical tests only the three
marker characters there and says nothing about what follows, which is
what `rs/src/text.rs` already does.

## An unterminated typed tag folds a split astral character (Rust)

Written with `U+XXXX` again, for the same reason.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: !!str "` + U+1F600 | U+D83D | three U+FFFD | U+FFFD |
| `a: !!str "a` + U+1F600 | `a` then U+D83D | `a` then three U+FFFD | `a` then U+FFFD |
| `a: !!str '` + U+1F600 | U+D83D | three U+FFFD | U+FFFD |
| `a: !!str "` + U+4E2D U+6587 | U+4E2D | U+4E2D then two U+FFFD | U+4E2D |
| `a: !!str "` | `"` | `ERROR:internal` | `"` |

An unterminated quoted value after a typed tag ends at the end of the
source, and the canonical handler then takes
`fwd.substring(valStart + 1, valEnd - 1)`: everything up to the last
UTF-16 CODE UNIT. An astral character is two of those units and one Rust
scalar, so dropping one unit leaves a LONE HIGH SURROGATE, which a Rust
string has no place for. This port folds it to the replacement
character, which is what it does with every other unpaired surrogate.
Only the astral rows differ; the rest of the table is the control, the
reversed-index row included, where `substring` swaps its arguments and
yields the quote itself.

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

The Go column is a DEFECT of the same class and not a trade. Go steps
back one BYTE rather than one unit, which cuts a multibyte character in
half and leaves invalid UTF-8 in the value, and where the canonical
`substring` would swap a reversed pair Go panics on the slice and the
engine reports `internal`. It is written down here so a shared fixture
row is not added over it before the repair lands.

Provenance: the TypeScript column was produced by running
`ts/src/yaml.ts` under Node 22; the Go column by `tabnasyaml.Parse` in
`go/`; the Rust column by `tabnas_yaml::parse`. No row of
`test/spec/*.tsv` can carry the TypeScript answers, because an expected
cell is UTF-8 text and a lone surrogate has no UTF-8 encoding. Pinned by
`rs/tests/divergence_test.rs::an_unterminated_typed_tag_folds_a_split_astral_character`,
whose control rows fail if the Basic Multilingual Plane cases drift and
whose astral rows fail if the fold changes.

Owner: the string model, as upstream, for the Rust rows; the Go port for
the Go column.

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

## Trailing text after digits inside a flow collection (Go)

A value that starts with a digit and is followed by a space and more
text is one plain scalar in the canonical, whether or not it sits inside
a flow collection. The Go port applies that rule only in block context:
inside a flow collection it falls through to the number matcher, which
takes the digits and leaves the rest to the grammar.

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: [12 x]` | `{"a":["12 x"]}` | `{"a":[12,"x"]}` | `{"a":["12 x"]}` |
| `a: {b: 12 x}` | `{"a":{"b":"12 x"}}` | `{"a":{"b":12,"x":null}}` | `{"a":{"b":"12 x"}}` |
| `a: [12, 3]` | `{"a":[12,3]}` | the same | the same |
| `a: 12 x` | `{"a":"12 x"}` | the same | the same |

The last two rows are the controls: a comma inside a flow collection is
still a separator everywhere, and the same trailing text in BLOCK
context agrees in all three.

The cause is one guard. The canonical takes the trailing-text branch
unconditionally, and `go/yaml.go` reaches it only when the flow depth is
zero. That guard predates
[#54](https://github.com/tabnas/yaml/pull/54), which moved the branch
ahead of the comma branch in both runtimes and preserved each side's
existing condition, so this difference is older than that change rather
than introduced by it.

Provenance: measured 2026-09-21, the TypeScript column by running
`ts/src/yaml.ts` under Node 22 against `@tabnas/parser` 0.10.0 and
`@tabnas/jsonic` 0.6.7, the Go column by `tabnasyaml.Parse` in `go/`,
and the Rust column by `tabnas_yaml::parse`. Not a shared fixture row,
because Go is red on the first two; the block-context controls ARE
shared rows, in `test/spec/real-world-regressions.tsv` and
`test/spec/flow-collections.tsv`. Pinned on the Rust side by
`digits_then_text_inside_a_flow_collection_stay_one_scalar` in
`rs/tests/js_semantics_test.rs`.

Owner: the Go port. Dropping the `flowState.depth == 0` guard in
`handleNumericColon` is the whole repair. Delete this entry and move its
first two rows into `test/spec/flow-collections.tsv` when that lands.
