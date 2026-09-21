# Divergences

TypeScript is the canonical implementation; the Go and Rust ports track
it. This file records where a runtime produces a **different result for
the same input**, and why the difference is allowed to stand.

None of these can be written as a row of `test/spec/*.tsv`, which is why
this repository still has no divergence register: one is invisible to the
value those fixtures compare, two concern a diagnostic's position, which
no fixture pins, and one needs a nesting depth far past anything a
fixture cell would hold. A divergence a row could express belongs in a
register, with a `rust` column, per [`AGENTS.md`](AGENTS.md).

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
