# Divergences

TypeScript is the canonical implementation; the Go and Rust ports track
it. This file records where a runtime produces a **different result for
the same input**, and why the difference is allowed to stand.

None of these can be written as a row of `test/spec/*.tsv`, which is why
this repository still has no divergence register: one is invisible to the
value those fixtures compare, two concern a diagnostic's position, which
no fixture pins, one needs a nesting depth far past anything a fixture
cell would hold, two need a lone UTF-16 surrogate in an expected
cell, which a UTF-8 file cannot carry, and one is an input the runtimes
disagree about accepting at all, where a row carries one expected answer
for all three. A divergence a row could
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
| `a: "\uD83D\uDE00"` | U+1F600 | U+1F600 | U+1F600 |
| `a: "\U0000D83D\uDE00"` | U+1F600 | U+1F600 | U+1F600 |
| `a: "\uD83D"` | a lone high surrogate | U+FFFD | U+FFFD |
| `a: "\uDE00"` | a lone low surrogate | U+FFFD | U+FFFD |
| `a: "\U0000D83D"` | a lone high surrogate | U+FFFD | U+FFFD |

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

Provenance: the TypeScript column was produced by running
`ts/src/yaml.ts` under Node 22; the Go column by `tabnasyaml.Parse` in
`go/`; the Rust column by `tabnas_yaml::parse`. No row of
`test/spec/*.tsv` can carry the TypeScript answers for the unpaired
rows, because an expected cell is UTF-8 text and a lone surrogate has
no UTF-8 encoding. They are pinned by
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

## A tab-only tail at the end of the source (Go)

| input | TypeScript | Go | Rust |
|---|---|---|---|
| `a: 1<TAB>` | `ERROR:unexpected` | `{"a":1}` | `ERROR:unexpected` |
| `a: true<TAB>` | `ERROR:unexpected` | `{"a":true}` | `ERROR:unexpected` |
| `a: null<TAB>` | `ERROR:unexpected` | `{"a":null}` | `ERROR:unexpected` |
| `a: [1]<TAB>` | `ERROR:unexpected` | `{"a":[1]}` | `ERROR:unexpected` |
| `- 1<TAB>` | `ERROR:unexpected` | `[1]` | `ERROR:unexpected` |
| `a: 1<TAB>\nb: 2` | `{"a":1,"b":2}` | same | same |
| `a: 1 ` | `{"a":1}` | same | same |
| `a: x<TAB>` | `{"a":"x"}` | same | same |

What is established: the canonical's keyword and number branches in
the text check advance by the TRIMMED text, so a tab sitting after such
a value stays in the source, where the plain-scalar branch would have
advanced over the whole run it consumed. The matcher's next round
reaches that tab through the branch that skips a blank line carrying
one, and the branch does consume it, which instrumenting the branch
shows. The document is refused anyway, at the end of the source.

What was NOT isolated is the step that refuses it. The obvious
candidate is that same branch charging a row and resetting the column
although no newline followed (`skip` is `lineEnd + 1` when there is a
newline and the line's own length when there is not, so the point
moves to a line that does not exist), but holding the row and the
column still over that branch leaves every row of the table where it
is. The Rust port carries the canonical arithmetic and the canonical
refusal. The Go matcher has no such branch, so a tail of blanks
reaches jsonic's own lexer, which reads a tab as a space, and the
document ends.

The last three rows are the controls, and they say what the entry is
NOT about: the same tab followed by a newline is a blank line in every
runtime, a trailing SPACE never enters the branch, and a plain
scalar's own handler has already consumed the tab before the branch is
reached. Bare jsonic accepts `a: 1<TAB>` in both languages, so the
refusal belongs to this plugin and not to the grammar underneath it.

Provenance: the TypeScript column was produced by running
`ts/src/yaml.ts` under Node 22; the Go column by `tabnasyaml.Parse` in
`go/`; the Rust column by `tabnas_yaml::parse`. No row of
`test/spec/*.tsv` can carry this, because a row's expected cell is one
answer for all three runtimes and these two answers differ in kind.
Pinned by
`go/divergence_test.go::TestATabOnlyTailAtTheEndOfTheSourceIsAccepted`.

Owner: the CANONICAL, and the Go column is the answer to keep. A
document whose last character is a tab is valid YAML, and a refusal
that depends on whether the value was a number, a keyword or a plain
scalar is an artifact rather than a rule. The repair belongs in
`ts/src/yaml.ts`, and then in `rs/src/lex.rs`, and it starts by
finding the step named above; it may free cases in
`test/yaml-test-suite-unparsed.tsv`, which is the direction that
ledger moves in. Until it lands, Go is recorded as differing rather
than made to copy the wart, because a port never takes on a canonical
defect (ADR-13).
