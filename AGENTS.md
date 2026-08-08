# Agents Guide — yaml

## What this project is

`@tabnas/yaml` is a **grammar plugin that parses a core subset of YAML**
into plain JavaScript objects (TS/JS) or Go values. It covers block
mappings and sequences (indentation-based), flow collections
(`{a: 1}`, `[1, 2, 3]`), single/double quoted scalars (including
multiline), block scalars (literal `|` / folded `>`, with chomping),
anchors (`&name`) / aliases (`*name`) / merge keys (`<<`), multi-document
streams (`---` / `...`), YAML value keywords
(`true`/`false`/`yes`/`no`/`on`/`off`, `null`/`~`, `.inf`, `.nan`),
comments (`#`), tags and `%TAG` directives, and hex/octal/binary integer
literals.

### The conformance bar, measured

It is **not** a full YAML 1.2 parser. The bar is the documented feature
set above, verified against the **complete** official
[YAML Test Suite](https://github.com/yaml/yaml-test-suite) (`data`
branch) — vendored byte-identical at
[`test/yaml-test-suite/`](test/yaml-test-suite/) and run by **both**
runtimes. Exactly what that suite measures here:

| Suite bucket | Cases | Result |
|---|---|---|
| Valid parse (case has `in.json`) | 279 | **279 pass** — compared across *every* document in the stream, not just the first |
| Expected error (case has `error`) | 94 | **24 rejected**; the other **70 are accepted** (see below) |
| No expected output (neither file) | 29 | Gathered but not asserted — the suite states no result to compare |
| **Total** | **402** | |

Those 70 accepted-but-spec-invalid cases are the plugin's **leniency
boundary**, and it is a checked one rather than a hand-wave: every id is
listed in
[`test/yaml-test-suite-lenient.tsv`](test/yaml-test-suite-lenient.tsv),
both runners read that one file, and the error-case test fails if a
listed case starts being *rejected* or if an unlisted `error` case is
*accepted*. Tightening the parser therefore means deleting lines from
that file; nothing can drift silently, and the two runtimes cannot
disagree about which inputs are errors. The leniency is inherited by
design — this plugin layers on jsonic's deliberately relaxed grammar,
which does not reject every construct YAML 1.2 forbids.

Unlike most tabnas grammar plugins, this one is **layered on top of
jsonic, not the bare engine**: it is a plugin for the
[`@tabnas/jsonic`](https://github.com/tabnas/jsonic) relaxed-JSON
grammar, which in turn runs on the
[`@tabnas/parser`](https://github.com/tabnas/parser) engine. You install
it on a jsonic-enabled instance:

```js
const { Tabnas } = require('@tabnas/parser')
const { jsonic } = require('@tabnas/jsonic')
const { Yaml } = require('@tabnas/yaml')

const j = new Tabnas().use(jsonic).use(Yaml)
j.parse('name: Alice\nitems:\n  - one\n  - two\n')
// => { name: 'Alice', items: ['one', 'two'] }
```

## Repository map

| Path | What it is |
|---|---|
| [`ts/`](ts/) | **Canonical** TypeScript implementation — the `@tabnas/yaml` package. The entire plugin (lexer matcher + grammar wiring + scalar/anchor/tag handling) lives in the single large [`ts/src/yaml.ts`](ts/src/yaml.ts). Depends on `@tabnas/jsonic` and `@tabnas/parser`. |
| [`go/`](go/) | Go port — `github.com/tabnas/yaml/go`. The whole plugin is in [`go/yaml.go`](go/yaml.go); the package's `const VERSION` lives there too. Module path is `github.com/tabnas/yaml/go`, but its only tabnas dependency is **jsonic** (see below). |
| [`yaml-grammar.jsonic`](yaml-grammar.jsonic) | **Single source of truth for the grammar**, written in jsonic syntax. Lives at the **repo root** and is embedded verbatim into `ts/src/yaml.ts` and `go/yaml.go` by [`ts/embed-grammar.js`](ts/embed-grammar.js). Do not edit the embedded copies by hand — edit the `.jsonic` and re-run the embed. |
| [`test/spec/`](test/spec/) | **Repo-root shared fixtures**, auto-discovered and run by both runtimes: `*.tsv` files with an `input`/`expected`/`opts` header row. See [`test/AGENTS.md`](test/AGENTS.md) for the exact format. |
| [`test/yaml-test-suite/`](test/yaml-test-suite/) | The upstream YAML Test Suite corpus, vendored verbatim and run by **both** runtimes, plus [`test/yaml-test-suite-lenient.tsv`](test/yaml-test-suite-lenient.tsv), the shared ledger of `error` cases this parser accepts. |
| [`ts/test/`](ts/test/) | TS `*.test.ts` suites (compiled to `dist-test/`): `yaml.test.ts` (unit), `parity.test.ts` (the shared `test/spec/*.tsv` fixtures), `yaml-test-suite.test.ts` (official corpus), `doc-examples.test.ts`, `debug-model.test.ts` (the `@tabnas/debug` composition test). |
| [`go/`](go/) `*_test.go` | Go suites: `yaml_test.go` + `yaml_scenarios_test.go` (unit), `parity_test.go` (`TestSpec` runs the shared `test/spec/*.tsv` fixtures), `parity_regression_test.go` (TS/Go parity regressions), `yaml_test_suite_test.go` (official corpus), plus `bench_test.go` / `perf_test.go` / `scaling_test.go` (performance). |
| [`ts/doc/`](ts/doc/), [`go/doc/`](go/doc/) | Per-runtime Diataxis guides (`yaml-ts.md`, `yaml-go.md`) and the generated railroad diagram (`ts/doc/grammar.{svg,txt}`). |
| [`bench/`](bench/) | TS benchmark harness (`bench/ts/*.mjs`, fixtures generated by `bench/fixtures/generate.mjs`); the Go side benches via `go/bench_test.go`. |

## The grammar is embedded — edit `yaml-grammar.jsonic`

The grammar (rule alts, refs, token wiring) is authored once in
[`yaml-grammar.jsonic`](yaml-grammar.jsonic) at the repo root and injected between
`// --- BEGIN EMBEDDED yaml-grammar.jsonic ---` /
`// --- END EMBEDDED yaml-grammar.jsonic ---` markers in both `src/yaml.ts` (as a TS template
literal) and `go/yaml.go` (as a Go raw string). `embed-grammar.js`
escapes backslashes/backticks/`${` for the TS literal and rejects any
backtick in the file (it would break the Go raw string). Workflow:

1. Edit `yaml-grammar.jsonic` (repo root).
2. Run `npm run embed` in `ts/` (or `make embed` from `ts/`, i.e.
   `make -C ts embed`) to re-sync both copies.
3. Build/test both sides.

`npm run build` runs the embed first (`node embed-grammar.js && tsc
--build src test`), so a plain build re-syncs the embedded text. The
state handlers (`bo`/`ao`/`bc`/`ac`) and `@`-prefixed function refs in
the grammar resolve to closures wired in the source code, not in the
`.jsonic` file.

## The tabnas dependencies (sibling checkout)

This plugin sits on top of jsonic, which sits on the parser engine. The
tabnas packages are unpublished, so both runtimes resolve them via
**sibling checkouts**:

- **TypeScript** (`ts/package.json`): `@tabnas/jsonic` and
  `@tabnas/parser` are `peerDependencies` (`">=2"`), mirrored as
  `file:../../jsonic/ts` and `file:../../parser/ts` devDependencies for
  local builds (npm >=7 / Node >=24 auto-installs peers;
  `engines.node` is `">=24"`). `@tabnas/debug` and `@tabnas/railroad`
  are **dev-only** `file:` devDependencies — debug for the
  `debug-model` composition test, railroad to regenerate
  `ts/doc/grammar.{svg,txt}`.
- **Go** (`go/go.mod`): the module is `github.com/tabnas/yaml/go`, and
  its **only** require is `github.com/tabnas/jsonic/go`, resolved with
  `replace github.com/tabnas/jsonic/go => ../../jsonic/go` (a sibling
  checkout). The Go plugin imports `jsonic` directly and never imports
  the parser engine — jsonic re-exports the engine surface it needs.

Clone `https://github.com/tabnas/jsonic` and
`https://github.com/tabnas/parser` (plus `debug`/`railroad` for the
composition test and diagram) as siblings of this repo, build their TS
(`npm install && npm run build` in each, in dependency order), then work
here. CI checks the whole closure out and builds it first.

## Authority and alignment rules

1. **TypeScript is canonical.** When TS and Go disagree on parse
   behavior, TS wins; change Go to match, and add or extend a shared
   `.tsv` fixture when the behavior is expressible as `input → output`.
2. The shared fixtures in [`test/spec/*.tsv`](test/spec/) are the parity
   contract. Both suites auto-discover every file in that directory and
   both must stay green. TS reads it from `dist-test/` at
   `../../test/spec` (`ts/test/parity.test.ts`); Go globs
   `../test/spec/*.tsv` (`go/parity_test.go` `TestSpec`). Line 1 is a
   header naming the columns `input`/`expected`/`opts`; `\n`, `\r`,
   `\t`, `\\` are unescaped in `input` only (`expected` and `opts` are
   raw JSON). Full format rules — including the `ERROR`, `UNDEFINED` and
   `@@Infinity`/`@@NaN` spellings — are in
   [`test/AGENTS.md`](test/AGENTS.md).
3. The grammar text in both runtimes is byte-identical because it is
   embedded from the same `yaml-grammar.jsonic`. Keep it that way — make
   grammar changes in the `.jsonic` and re-embed; do not hand-edit one
   runtime's embedded copy.
4. The `parity_regression_test.go` cases capture real-world YAML
   (OpenAPI/Swagger) that the Go port once rejected but TS accepted.
   When you fix a Go parity bug, prefer adding the snippet there or to a
   shared `test/spec/*.tsv`.
5. The official YAML Test Suite (`test/yaml-test-suite/`) is run by
   **both** runtimes — `ts/test/yaml-test-suite.test.ts` and
   `go/yaml_test_suite_test.go`, which mirror each other's gathering and
   comparison rules. Each has a `SKIP` / `suiteSkip` map (both currently
   empty) for cases beyond this subset; if you regress a case, add it
   there with a reason rather than silently dropping coverage. The
   `error`-case expectations live in the shared
   `test/yaml-test-suite-lenient.tsv` ledger, not in either runner.

## Public API

The TS and Go surfaces differ in shape (TS exposes only the plugin; Go
exposes convenience entry points):

- **TS** (`src/yaml.ts`) exports the `Yaml` plugin, the `YamlOptions`
  type, and `const VERSION`. Parse by installing the plugin:
  `new Tabnas().use(jsonic).use(Yaml).parse(src)`. There is **no**
  exported `parse`/`make` on the TS side.
- **Go** (`go/yaml.go`) exports `Parse(src) (any, error)` (lazy default
  instance), `MakeJsonic(opts ...YamlOptions) *jsonic.Jsonic` (build a
  configured instance), the `Yaml` plugin (`j.Use(Yaml, opts)`), and
  `const VERSION`.
- **`VERSION` must always equal `ts/package.json` "version"**, in both
  runtimes. `go/version_test.go` and `ts/test/version.test.ts` are the CI
  checks: they read `ts/package.json` and fail (never skip) on drift. The
  release orchestrator rewrites both constants — never bump one by hand.
- `YamlOptions{ meta }` exists in both: with `meta: true`, parsing
  returns `{ meta, content }` (per-document `{directives, explicit,
  ended}`) instead of bare content.

## Repo-specific gotchas

- **Do not edit the embedded grammar copies.** The grammar in
  `src/yaml.ts` / `go/yaml.go` is generated. Edit `yaml-grammar.jsonic`
  and run `npm run embed` in `ts/` (or `make -C ts embed`).
- **The start rule is `stream`, not jsonic's `val`.** The plugin sets
  it via `tabnas.options({ rule: { start: 'stream' } })` in `src/yaml.ts`
  (Go: `Rule: &jsonic.RuleOptions{Start: "stream"}` in `MakeJsonic`);
  a YAML document stream is the entry point and
  it opens into the shared `val` rule. The `debug.model()` test asserts
  `m.config.start === 'stream'` (note `config.start`, not `m.start`).
- The grammar adds YAML rules (`stream`, `yamlBlockElem`,
  `yamlBlockList`, `yamlElemMap`, `yamlElemPair`) on top of jsonic's
  shared `val`/`map`/`list`/`pair`/`elem`/`indent` rules; the full rule
  set is asserted in `debug-model.test.ts`.
- The Go module path says `tabnas/yaml/go`, but the dependency is on
  **jsonic**, not parser, and `go/go.sum` still carries a stale
  `github.com/jsonicjs/jsonic/go` hash from the pre-rename history — the
  active require/replace points at `github.com/tabnas/jsonic/go =>
  ../../jsonic/go`.
- **A block scalar indicator followed by text on the same line
  (`a: > x`) is NOT a block scalar.** YAML calls that an error; this
  plugin falls through to plain-scalar handling and yields
  `{"a": "> x"}`. Both runtimes do this — Go's `textCheck` must fall
  through to `handlePlainScalar` when `handleBlockScalar` returns nil,
  or it rejects what TS accepts (yaml-test-suite S4GJ).
- **A `...` with no document open produces no document.** Only a `...`
  that *terminates* something closes a document; a stray or
  comment-only `...` region is not a document (`a\n...\n...` is one
  document, not two). The `stream` rule's open-phase `#DE` alt consumes
  and rotates without accumulating.
- A pile of one-off debug scripts (`check_*.js`, `test_*.js`,
  `*_failing.txt`, etc.) are `.gitignore`d; don't commit them.

## Build & test

TypeScript (from `ts/`):

```bash
npm install            # auto-installs the jsonic/parser peers; resolves file: siblings
npm run build          # node embed-grammar.js && tsc --build src test
npm test               # node --enable-source-maps --test "dist-test/*.test.js"
```

`npm run embed` re-syncs the grammar without a full build;
`npm run watch` is `tsc --build src test -w`. The tests are TypeScript
(`ts/test/*.test.ts`) compiled to `dist-test/*.test.js` by the build —
run `npm run build` before `npm test`.

Go (from `go/`):

```bash
go build ./...
go test -v ./...       # unit + shared .tsv fixtures + parity
```

The repo-root [`Makefile`](Makefile) (adapted from voxgig/util) wraps
both halves: `make` / `make build` / `make test` run the TS and Go
sides; `make test-ts` / `make test-go` run one; `make reset` does a
clean install/rebuild/retest. (The `embed` target lives in `ts/Makefile`,
not the root one — run `make -C ts embed` or `npm run embed` in `ts/`.)
`make publish-go V=x.y.z` injects `V` into the `const VERSION` in
`go/yaml.go`, commits, and tags `go/vX.Y.Z`; `make publish-ts` publishes
the TS package at its `package.json` version.

## Composition test (@tabnas/debug)

`ts/test/debug-model.test.ts` layers the plugin with the official
[`@tabnas/debug`](https://github.com/tabnas/debug) plugin and asserts the
structured grammar via `debug.model()`: the full rule-name set, `json`-
style `m.config.start === 'stream'`, that `Yaml` is in `m.plugins`, and
push edges (`stream` opens `val`; `yamlBlockList` pushes `yamlElemMap`).
`@tabnas/debug` is resolved dynamically and the test **skips** when it is
absent (set `TABNAS_DEBUG_PATH` to a built sibling to force it). Because
debug is a `file:` devDependency, plain `npm test` runs it.

## CI

`.github/workflows/build.yml` has a single matrix job
(`ubuntu`/`windows`/`macos`, Node 24). It sets
`git config --global core.autocrlf false` (CRLF corrupts the `.tsv`
fixtures), git-clones the tabnas closure
(`parser debug json abnf railroad jsonic`) as siblings, runs
`npm i && npm run build --if-present` for each (and `yaml`) in order,
then `npm test` in `yaml/ts`. There is **no Go CI job** in this repo;
the Go side is validated locally / via `make test-go`.
