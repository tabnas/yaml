# Agents Guide — shared spec fixtures

This directory holds two different things:

- `spec/*.tsv` — the cross-runtime **parity** fixtures described below.
- `yaml-test-suite/` — the official third-party **conformance** corpus,
  vendored verbatim, plus the two shared ledgers that record where this
  parser deviates from it: `yaml-test-suite-lenient.tsv` (must-fail cases
  it accepts) and `yaml-test-suite-unparsed.tsv` (valid parse-only cases it
  rejects). Both runners read both files, so the two runtimes cannot drift.
  Lines are only ever DELETED from a ledger — see each file's header.

`spec/*.tsv` holds the cross-runtime parity fixtures. Both runtimes
auto-discover and run **every** file in this directory, so a change here
affects TypeScript and Go together — edit with that in mind.

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
`"@@-Infinity"` and `"@@NaN"`. Both runners rewrite their own result the same
way before comparing. Input that yields no value at all is spelled with the
bare token `UNDEFINED` in the `expected` column.

`UNDEFINED` means the parse yielded no document at all, which is distinct
from a document whose value is null. TS distinguishes them (`undefined` vs
`null`); the Go port currently returns a bare `nil` for both, so an
`UNDEFINED` fixture cannot fail in Go today. That divergence is recorded,
not hidden: `TestUndefinedIsIndistinguishableFromNull` in
`go/parity_test.go` pins it, and fails as soon as Go grows a real undefined
result — at which point the allowance in `runSpecFile` is deleted too.

Results are compared after a JSON round-trip, so key order and the
`OrderedMap` / null-prototype-object representations do not affect the
comparison.

These fixtures replaced the old `test/*.tsv` files (a `name`/`input`/`expected`
shape with no header, and a second escape-decoding pass over `expected`) and
the cases that used to live inline in `ts/test/yaml.test.ts`.

## Who runs what

- TypeScript: `ts/test/parity.test.ts` — `makeRunner(...).dir(...)`.
- Go: `go/parity_test.go` — `support.Runner{...}.Dir(t, dir)`.

Both are a dozen lines holding only what is specific to yaml: how to build
the parser for a row's options, and the marker encoding for YAML's
non-finite numbers. Everything else — finding `test/spec`, reading the
file, decoding escapes, the `ERROR:` contract, the comparison, the
`<file>:<line>` in a failure message — comes from
[`@tabnas/support`](https://github.com/tabnas/support) and its Go half, so
the two loaders cannot drift from each other either.

Both discover files by directory listing: adding a `.tsv` here runs it in
both runtimes without touching either runner. An empty fixture, and a spec
directory with no fixtures in it, both **fail** — a runner that reports
green having run nothing is indistinguishable from coverage that was never
there.

## Rules

- Prefer adding a fixture here over a one-off in-language assertion when a
  case is expressible as input → output. That is what keeps the two
  runtimes honest against each other.
- TypeScript is canonical. If the two runtimes disagree, the TS behaviour is
  the expected value — unless Go has exposed a genuine TS defect, in which
  case fix TS first and pin the corrected behaviour here.
- A new fixture must pass in BOTH runtimes: run `go test ./...` (from `go/`)
  and `npm test` (from `ts/`) before considering it done.
