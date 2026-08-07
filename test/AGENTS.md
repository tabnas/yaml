# Agents Guide — shared spec fixtures

This directory holds two different things:

- `spec/*.tsv` — the cross-runtime **parity** fixtures described below.
- `yaml-test-suite/` — the official third-party **conformance** corpus. It
  is **gitignored and never committed**; `scripts/fetch-yaml-test-suite.sh`
  fetches it at a pinned commit. See the repo `AGENTS.md` section "The
  conformance corpus is fetched, never committed".

`spec/*.tsv` holds the cross-runtime conformance fixtures. Both runtimes
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
| `expected` | A JSON value (the parse result), or `ERROR` / `ERROR:<substring>` for inputs that must fail. |
| `opts` | Optional JSON object of plugin options (empty means defaults). |

`ERROR` means the input must be rejected; `UNDEFINED` means the parse must
yield no value at all — in Go that is `jsonic.IsUndefined`, not merely `nil`
(a bare `nil` used to satisfy both `UNDEFINED` and `null`, so such a fixture
could not fail).

`expected` and `opts` are **not** escape-decoded — they are raw JSON, so
JSON's own escape rules apply (`"a\nb"` is a string containing a newline).
To put a literal backslash in `input`, write `\\`.

YAML's non-finite numbers (`.inf`, `-.inf`, `.nan`) have no JSON spelling, so
at any depth they are written as the marker strings `"@@Infinity"`,
`"@@-Infinity"` and `"@@NaN"`. Both runners rewrite their own result the same
way before comparing. Input that yields no value at all is spelled with the
bare token `UNDEFINED` in the `expected` column.

Results are compared after a JSON round-trip, so key order and the
`OrderedMap` / null-prototype-object representations do not affect the
comparison.

These fixtures replaced the old `test/*.tsv` files (a `name`/`input`/`expected`
shape with no header, and a second escape-decoding pass over `expected`) and
the cases that used to live inline in `ts/test/yaml.test.ts`.

## Who runs what

- TypeScript: `ts/test/parity.test.ts` — reads `../../test/spec` at runtime
  from `dist-test/`, one `describe` per file.
- Go: `go/parity_test.go` — `TestSpec` globs `../test/spec/*.tsv`.

Both discover files by directory listing: adding a `.tsv` here runs it in
both runtimes without touching either runner.

## Rules

- Prefer adding a fixture here over a one-off in-language assertion when a
  case is expressible as input → output. That is what keeps the two
  runtimes honest against each other.
- TypeScript is canonical. If the two runtimes disagree, the TS behaviour is
  the expected value — unless Go has exposed a genuine TS defect, in which
  case fix TS first and pin the corrected behaviour here.
- A new fixture must pass in BOTH runtimes: run `go test ./...` (from `go/`)
  and `npm test` (from `ts/`) before considering it done.
