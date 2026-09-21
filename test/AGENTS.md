# Agents Guide — shared spec fixtures

This directory holds two different things:

- `spec/*.tsv` — the cross-runtime **parity** fixtures described below.
- `yaml-test-suite/` — the official third-party **conformance** corpus,
  vendored verbatim, plus the two shared ledgers that record where this
  parser deviates from it: `yaml-test-suite-lenient.tsv` (must-fail cases
  it accepts) and `yaml-test-suite-unparsed.tsv` (valid parse-only cases it
  rejects). Every runner reads both files, so the runtimes cannot drift.
  Lines are only ever DELETED from a ledger — see each file's header.

`spec/*.tsv` holds the cross-runtime parity fixtures. All three runtimes
auto-discover and run **every** file in this directory, so a change here
affects TypeScript, Go and Rust together — edit with that in mind.

## Format

Tab-separated, one case per line, with a header row naming the columns.
Blank lines are skipped, and so are comment lines — a line starting with
`#` that contains no tab. (A data row always has at least one tab, so a
`#`-leading source such as a C preprocessor directive still works.)

| Column | Meaning |
|---|---|
| `input` | YAML source. Escapes `\n` `\r` `\t` `\\` are decoded. |
| `expected` | A JSON value (the parse result), or `ERROR` / `ERROR:<code>` for inputs that must fail. The code is compared **exactly** — it is the error's code, not a substring of its message. |
| `opts` | Optional JSON object of plugin options (empty means defaults). |

`expected` and `opts` are **not** escape-decoded — they are raw JSON, so
JSON's own escape rules apply (`"a\nb"` is a string containing a newline).
To put a literal backslash in `input`, write `\\`.

YAML's non-finite numbers (`.inf`, `-.inf`, `.nan`) have no JSON spelling, so
at any depth they are written as the marker strings `"@@Infinity"`,
`"@@-Infinity"` and `"@@NaN"`. Every runner rewrites its own result the same
way before comparing. Input that yields no value at all is spelled with the
bare token `UNDEFINED` in the `expected` column.

`UNDEFINED` means the parse yielded no document at all, which is distinct
from a document whose value is null. TS distinguishes them (`undefined` vs
`null`); the Go port returns a bare `nil` for both, and the Rust port
returns `Null` for both, because the engine replaces every `Undefined` in
a finished value with null on the way out. So an `UNDEFINED` fixture
cannot fail in Go or Rust today. That divergence is recorded, not hidden:
`TestUndefinedIsIndistinguishableFromNull` in `go/undefined_test.go`,
`rs/tests/undefined_test.rs` and the entry in `DIVERGENCE.md` pin it, and
fail as soon as a port grows a real undefined result — at which point the
allowance in each runner is deleted too.

Results are compared after a JSON round-trip, so key order and the
`OrderedMap` / null-prototype-object representations do not affect the
comparison.

These fixtures replaced the old `test/*.tsv` files (a `name`/`input`/`expected`
shape with no header, and a second escape-decoding pass over `expected`) and
the cases that used to live inline in `ts/test/yaml.test.ts`.

## Who runs what

- TypeScript: `ts/test/parity.test.ts` — `makeRunner(...).dir(...)`.
- Go: `go/parity_test.go` — `support.Runner{...}.Dir(t, dir)`.
- Rust: `rs/tests/parity_test.rs` — `Runner::new_with_row(...).dir(...)`.

Each is a dozen lines holding only what is specific to yaml: how to build
the parser for a row's options, and the marker encoding for YAML's
non-finite numbers. Everything else — finding `test/spec`, reading the
file, decoding escapes, the `ERROR:` contract, the comparison, the
`<file>:<line>` in a failure message — comes from
[`@tabnas/support`](https://github.com/tabnas/support) and its Go and Rust
halves, so the three loaders cannot drift from each other either.

All three discover files by directory listing: adding a `.tsv` here runs
it in every runtime without touching any runner. An empty fixture, and a
spec directory with no fixtures in it, both **fail** — a runner that
reports green having run nothing is indistinguishable from coverage that
was never there. The Rust runner additionally names every fixture it
expects (`every_fixture_is_present`), so a file renamed out of the
directory is a failure rather than a quiet loss.

## Rules

- Prefer adding a fixture here over a one-off in-language assertion when a
  case is expressible as input → output. That is what keeps the runtimes
  honest against each other.
- TypeScript is canonical. If the runtimes disagree, the TS behaviour is
  the expected value — unless a port has exposed a genuine TS defect, in
  which case fix TS first and pin the corrected behaviour here.
- A new fixture must pass in EVERY runtime: run `go test ./...` (from
  `go/`), `npm test` (from `ts/`) and `cargo test --all-targets` (from
  `rs/`) before considering it done.
