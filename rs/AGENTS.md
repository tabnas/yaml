# Agents Guide: rs/

The Rust port of the canonical TypeScript in [`../ts`](../ts). Read
[`../AGENTS.md`](../AGENTS.md) first: it holds the cross-runtime rules,
and this file only covers what is specific to this crate.

## Layout

| Path | |
|---|---|
| `src/lib.rs` | the option struct, the embedded grammar, every closure the grammar names, the `stream` rule, the rule lifecycle wiring, `yaml`, `plugin`, `make`, `make_with`, `parse`, `VERSION` |
| `src/lex.rs` | the YAML lexer matcher: indentation, block sequence markers, document frames, anchors, aliases, tags, explicit keys, both quoted forms, flow punctuation |
| `src/text.rs` | what the canonical port puts in `options.text.check`: block scalars and plain scalars, plus the typed-tag handler `lex.rs` calls |
| `src/state.rs` | the per-parse state, in the context's `u` bag |
| `tests/parity_test.rs` | every `../test/spec/*.tsv` fixture through `tabnas_support::Runner`, plus a census of the files |
| `tests/yaml_test_suite_test.rs` | the whole vendored YAML Test Suite, its two ledgers and the bucket census |
| `tests/yaml_test.rs` | in-language behaviour, mirroring `ts/test/yaml.test.ts` and the two Go unit files |
| `tests/parity_regression_test.rs` | the TypeScript/Go parity regressions captured from real OpenAPI and Swagger files |
| `tests/column_units_test.rs` | the sixteen error-column cases the other two runtimes assert |
| `tests/untrusted_test.rs` | deep nesting, long input, unterminated constructs, control characters, odd Unicode |
| `tests/divergence_test.rs` | every entry in `../DIVERGENCE.md` a fixture cannot express |
| `tests/undefined_test.rs` | the UNDEFINED against null divergence |
| `tests/grammar_test.rs` | the embedded grammar is still the file on disk, in all three runtimes |
| `tests/perf_test.rs` | `parse` reuses its instance; parse time grows about linearly |
| `tests/version_test.rs` | `Cargo.toml` == `VERSION` == `ts/package.json` == `go/yaml.go` |
| `tests/common/mod.rs` | shared helpers: the spec directory, the repository root, value conversion |
| `README.md` | the crate front page; its `rust` fences are doctests of this crate |

Crate `tabnas-yaml`, library `tabnas_yaml`. The engine (`tabnas`), the
jsonic grammar (`tabnas-jsonic`) and the fixture runner
(`tabnas-support`, dev only) are **path dependencies on sibling
checkouts** (`../../parser/rs`, `../../jsonic/rs`, `../../support/rs`),
and jsonic takes `../../json/rs` in turn. None is published, so there is
no registry version to fall back on.

```bash
cargo build --all-targets
cargo test --all-targets && cargo test --doc
cargo clippy --all-targets --all-features -- -D warnings
cargo fmt
```

`make test-rs` from the repository root is the fast loop; `ci/rust/run.sh`
is the full gate and adds `fmt --check`, the sibling-checkout check, the
lockfile check and the MSRV pin.

## Per-parse state lives in the context

The TypeScript plugin closes over thirteen variables and resets them on
the first lexer call of a parse, and the Go port carries the same
thirteen per instance. Neither can be done here: `Tabnas::parse` takes
`&self` and the instance is `Send + Sync`, so anything written during a
parse has to belong to that parse.

Every one of them lives in `Context::u` instead, under a `yaml` prefix,
through the accessors in `src/state.rs`: the anchors, the pending
anchors, the pending tokens, the `%TAG` handles, the stream's document
and metadata accumulators, and the incremental flow-depth cache. There
is nothing to reset, which is what
`reparsing_the_same_source_is_idempotent`,
`anchors_do_not_leak_between_parses` and
`the_default_parser_is_shared_across_threads` measure.

The bag holds engine `Value`s, so a pending token is stored as an object
and rebuilt on the way out (`lex::encode_token` and `decode_token`).

## The virtual cursor is load-bearing

The canonical matcher mutates the lexer's own `Point`, and not always to
the position natural advancement would give. After a standalone anchor or
tag line it assigns `pnt.cI = spaces` rather than `spaces + 1`; before a
document marker it assigns `0`; and the key-separator branch charges two
columns while advancing one character, so from a `: ` onwards the
canonical column runs one ahead of the true one.

Those are quirks, but they decide parses. `@val-set-el-in` computes a
block sequence's indent as `#EL`'s column minus one, so a port that
advanced naturally would accept `&a\n- 1\n- 2`, which the canonical
parser rejects. This engine's `Lexer` exposes advancement, not
assignment, so `src/lex.rs` carries a VIRTUAL cursor beside the real one,
in the context bag. Every token this plugin emits is built at the virtual
point; the real cursor moves by the same number of characters, so the
byte offsets never part; and when another matcher has consumed a token in
between, the virtual point is carried over that span the way the engine
carries its own.

That is also why this plugin emits the five flow punctuation tokens
itself rather than leaving them to the fixed-token matcher: a token built
at the engine's cursor would report a column one to the left of the
canonical one, which `tests/column_units_test.rs` measures.

## The matcher chain after a cursor move

Both engines dispatch their lexer's matcher chain on the FIRST character
of a call. A plugin matcher that moves the cursor and then produces
nothing therefore leaves the new position to a matcher set chosen for the
old one. The canonical matcher relies on this: it emits a flow
collection's opener itself after an anchor or a tag precisely because the
fixed-token matcher will not get a clean look at the advanced position.

The two chains are not built the same way, so two compensations are
needed here, and both are deliberate:

- `lex::claimable_after_move` refuses a character the canonical chain
  would have left unclaimed. Without it this port accepts `# c\n{a: 1}`,
  which the canonical parser rejects.
- `text::number_run` stands in for the number matcher, which the
  canonical chain still reaches after a move and this one does not (the
  engine gates that matcher on a character captured before the custom
  matchers ran). Without it `a: &an 1\n%YAML 1.2` folds the directive
  line into the scalar.

What is left of the difference is fourteen documents out of a 4,348-case
adversarial corpus: one value, and thirteen places a refusal all three
runtimes make is reported. They are recorded in `../DIVERGENCE.md` and
pinned in `tests/divergence_test.rs`.

## Scanning is by byte, columns count characters

Every character YAML gives syntactic meaning is ASCII, and UTF-8 never
puts an ASCII byte inside a multi-byte sequence, so a byte comparison is
a character comparison and a byte offset that stopped at one is a
character boundary. That is how the Go port scans and how this one does.

Columns are the exception: they count CHARACTERS, so `advance_cols`
converts the consumed span with `chars().count()`. Conflating the two is
the defect `go/column_units_test.go` was written for, and a slice taken
at a byte offset that is not a boundary panics, which is what
`is_doc_marker` and `is_struct_tag` compare byte by byte to avoid.

## Two matchers, not a check hook

The canonical plugin puts block scalars and plain scalars in
`options.text.check`. This engine's check hook sees the lexer alone, and
a YAML plain scalar needs the per-parse flow-depth cache, so the same
work is registered as a SECOND custom matcher, `@yaml-text`, at order
`7.5e6`. That band runs after the number matcher and before the text
matcher, which is exactly where `text.check` runs. The main matcher,
`@yaml-matcher`, is at `5e5`, below the first built-in band.

The canonical `number.check` hook and its `skipNumberMatch` flag have no
counterpart: where the canonical matcher sets the flag and returns
nothing, this one calls the plain-scalar handler directly, which reaches
the same token and cannot leave a flag set for the next parse.

## The node cell

A pushed or replaced rule SHARES its parent's `Rc<RefCell<Value>>`, so
`*rule.node.borrow_mut() = v` overwrites the parent's node too. An
assignment, `r.node = v` in TypeScript, installs a fresh cell here
(`set_node`). `yamlBlockList` and the `indent` path through `list` both
assign, because the canonical port assigns a fresh array there and passes
it along in `k`; every rotation of those rules then shares the fresh cell,
which is what makes the elements land in one list without `k` carrying
the array at all.

The one deliberate write THROUGH a shared cell is `finalize_stream`:
a rotation through `r: stream` shares the start rule's cell, which is the
parse root, so writing through it is the Rust spelling of the canonical
`ctx.root().node = result`.

## `undefined` in a mapping

`yamlElemMap` assigns `rule.node[key] = rule.child.node` in the canonical
port, and a `child.node` of `undefined` there makes the key present but
drops it from every serialization. This engine turns `Undefined` into
null on the way out, so the entry is left out instead (`store_elem_pair`)
and the serialized map agrees. `- i: ` at end of source is the shape.

## The grammar is generated

`src/lib.rs` carries a copy of `../yaml-grammar.jsonic` between
`// --- BEGIN EMBEDDED …` markers, written by `../ts/embed-grammar.js`,
which now has a Rust target guarded on `rs/src/lib.rs` existing. Edit the
`.jsonic` and run `npm run embed` in `ts/`; never hand-edit between the
markers. `tests/grammar_test.rs` compares all three embedded copies
against the file.

The text is parsed at install time with `tabnas_jsonic::parse`, exactly
as the TypeScript and Go ports parse their copies, and then converted to
the engine's grammar document. jsonic's numbers are doubles, so
`integral_numbers` puts whole ones back into integer form before the
alternate decoder reads `b: 2` as a backtrack count.

## The docs are gated, and this crate's page is not yet in the set

`README.md` is written to the published-set rules (no em dash, no first
person, no links to any `AGENTS.md`, no project history), but it is NOT
yet listed in `ts/scripts/gated-docs.cjs`, so Vale and
`ts/test/docs.test.js` do not see it. Adding it takes three changes in
`ts/` and `.github/`, which this port was not scoped to make: the page
list, the re-measured counts in `.vale.ini` and `docs/STYLE-GUIDE.md`
(`node ts/scripts/vale-counts.cjs --write`), and `rs/README.md` in both
`paths:` lists of the docs workflow. This file is internal and may be
blunt.

## The README is doctested

`src/lib.rs` includes `README.md` as rustdoc under `#[cfg(doctest)]`, so
every `rust` fence in it runs on `cargo test --doc` (they show up as
`readme_examples (line N)`). rustdoc runs each fence as written, so a
fence must be a complete program: wrap it in
`fn main() -> Result<(), Box<dyn std::error::Error>> { ... Ok(()) }`
rather than using `?` at the top level, and never use hidden `# ` lines,
which render as garbage on GitHub. The `toml` and `bash` fences are not
run.

## How this port was checked

Beyond the shared fixtures and the conformance suite, the port was
compared against the canonical TypeScript by running both over one corpus
and diffing.

| corpus | inputs | differing |
|---|---|---|
| fixture rows, suite inputs and every string literal in the Go tests | 822 | 1, the astral column |
| generated documents of realistic YAML shape | 8,408 | 0 |
| generated adversarial token soup | 4,348 | 14 |

Of the fourteen, one is a value and thirteen are the place a refusal all
three runtimes make is reported. All of it is in `../DIVERGENCE.md`,
pinned by `tests/divergence_test.rs`. A change to the lexer or the
grammar deserves the same treatment before it is called done: build the
canonical TypeScript beside a copy of this crate, run both over a
generated corpus, and diff.
