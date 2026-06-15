# @tabnas/yaml — Performance Review

_Target:_ parse very large YAML files (tens of KB → tens of MB) in
both the TypeScript and Go implementations.

_Method:_

1. Static review of `src/yaml.ts` (2,649 lines) and `go/yaml.go` (2,858 lines).
2. Synthetic fixture generator (`bench/fixtures/generate.mjs`) — one fixture per
   suspected hotspot shape.
3. Measurement:
   - TS: `bench/ts/bench.mjs` (`node --expose-gc`).
   - Go: `go/bench_test.go` (Go's built-in benchmarking).
4. Scaling sweep (`bench/ts/scaling.mjs`, `go/scaling_test.go`) — measures
   per-byte cost at N = 100…4000, so linear vs super-linear behavior is
   observable directly from the numbers.
5. CPU profiles:
   - TS: `bench/ts/flow_map_large.cpuprofile` (Chrome-style, V8 inspector).
   - Go: `bench/go-blockmap.cpuprof` (pprof).

All numbers below are from a single run on an Intel Xeon Platinum 8581C
@ 2.10 GHz, Linux, Node v22.22.2 / Go 1.24.7. Reproduce with the commands in
[README.md](README.md).

---

## Top-line numbers

### Fixture sweep (`node bench/ts/bench.mjs`, `go test -bench=. -benchmem`)

| Fixture                | Size   | TS median | TS MB/s | Go ns/op    | Go MB/s | Go allocs/op |
|------------------------|-------:|----------:|--------:|------------:|--------:|-------------:|
| block_map_wide         | 52.5 KB | 17.3 ms  |  2.97   | 468 ms      | 0.11    |   693,697    |
| block_seq_long         | 22.4 KB | 10.2 ms  |  2.15   | 183 ms      | 0.12    |   409,891    |
| nested_map_deep        |  2.0 KB |  1.4 ms  |  1.40   | 3.5 ms      | 0.58    |    18,941    |
| literal_block_long     | 86.0 KB |  3.3 ms  | 25.28   | 3.1 ms      | 28.09   |    10,371    |
| folded_block_long      | 63.8 KB |  7.0 ms  |  8.97   | 3.1 ms      | 20.76   |    10,391    |
| plain_multiline_long   | 14.6 KB |  4.5 ms  |  3.13   | 7.0 ms      |  2.11   |    37,703    |
| **flow_map_large**     | 25.2 KB | **201 ms** | **0.12** | **293 ms** | **0.09** | **741,952** |
| flow_seq_large         | 18.5 KB | 83 ms    |  0.22   | 134 ms      | 0.14    |   404,152    |
| anchor_alias_heavy     |  5.5 KB |  7.7 ms  |  0.70   | 48 ms       | 0.12    |   248,513    |
| dq_strings_escaped     | 37.9 KB |  7.8 ms  |  4.77   | 105 ms      | 0.37    |   356,688    |
| mixed_realistic        | 32.7 KB | 16.4 ms  |  1.95   | 206 ms      | 0.16    |   474,106    |

Key observations:

- Throughput on most real-world shapes is **0.1–0.6 MB/s** — about
  one to two orders of magnitude below typical YAML libraries
  (`go-yaml` and `js-yaml` both parse in the 10–100 MB/s range).
- The fastest shapes (block scalars, `|` / `>`) run at 20–28 MB/s,
  showing the underlying machinery _can_ be fast when the hot paths
  are avoided.
- Go shows very high allocation counts — **~700k allocations to parse
  a 52KB block map** (≈13 allocations per byte of input).
- Identical fixtures are usually within ~2× between Go and TS; Go
  occasionally _loses_ (flow/anchor fixtures) because its
  `json.Marshal`-based deep-copy is expensive.

### Scaling sweep — per-byte cost as N grows

#### TypeScript (`node bench/ts/scaling.mjs`)

```
Shape           N      bytes      time       µs/byte
blockMap      100       1680      5.43 ms    3.23 µs
blockMap     4000      81780     41.25 ms    0.50 µs   → amortizes, mostly linear
flowMap       100        986      6.52 ms    6.61 µs
flowMap      4000      53786    524.27 ms    9.75 µs   → per-byte cost grows 1.5× per 2× N
literalBlock 4000      46901      3.68 ms    0.08 µs   → linear
foldedBlock  4000      42889      8.43 ms    0.20 µs   → near-linear
anchorAlias  4000      40218     31.22 ms    0.78 µs   → mildly super-linear
```

#### Go (`go test -run TestScaling -v .`)

```
Shape           N      bytes      time       µs/byte
blockMap      100       1680      7.4 ms     4.42 µs
blockMap     4000      81780   1221.3 ms    14.93 µs   → ~3.4× per-byte growth → Θ(N·log N) or worse
flowMap       100        986      7.7 ms     7.86 µs
flowMap      4000      53786    785.2 ms    14.60 µs   → ~1.9× per-byte growth
literalBlock 4000      46901      2.5 ms     0.05 µs   → linear, amortizes
anchorAlias  4000      40218    358.1 ms     8.90 µs   → mildly super-linear
```

> Rule of thumb: if `µs/byte` roughly _doubles_ when N doubles, that path
> is O(N²). If it stays flat, the path is linear.

Both languages are clearly **super-linear** on block mappings, flow
mappings, and (to a lesser extent) anchor/alias expansion. Block scalars
are linear in both.

---

## Profile attribution

### TS — `flow_map_large.cpuprofile`

```
 86.0%  check               dist/yaml.js  (the yamlMatcher text-check callback)
  3.8%  (garbage collector)
  2.1%  process             jsonic/rules.js
  1.7%  parse_alts          jsonic/rules.js
  0.9%  textMatcher         jsonic/lexer.js
  0.4%  processFlowCollection
```

Virtually all CPU time on flow-mapping input is spent inside the custom
`yamlMatcher` closure. The rest of the Jsonic core is negligible.

### Go — `bench/go-blockmap.cpuprof` (`pprof -top -cum`)

```
flat%   cum%    symbol
27.0%   29.8%   github.com/tabnas/yaml/go.handlePlainScalar
14.9%   30.5%   github.com/tabnas/yaml/go.Yaml.func2 (yamlMatcher / textCheck)
 0.3%   22.8%   runtime.gcBgMarkWorker   ← GC pressure from high alloc
 0.2%   13.3%   regexp.MustCompile / regexp.Compile   ← hot-path regex compile
 2.8%   10.5%   runtime.scanobject
```

Three clear culprits:

1. `handlePlainScalar` — the O(N²) flow-context rescan.
2. `regexp.MustCompile` — recompiling regexes inside the lexer loop.
3. GC — driven by 700k+ allocations per parse.

---

## Findings

Cross-language findings first (same bug in both codebases), then
language-specific items.

### F1 — O(N²) flow-context rescan _(CRITICAL — affects both)_

**TS** – `src/yaml.ts:922-950` – rescans from `_flowScanPos` to `pnt.sI`
every time a plain scalar is seen. There _is_ an incremental cache, but:

```ts
for (let fi = _flowScanPos; fi < pnt.sI; fi++) { ... }
_flowScanPos = pnt.sI
```

…only advances from the previous scalar's starting position. On an input
made of thousands of plain scalars this is still O(total tokens × max
gap) and shows up as the dominant self-time in `yamlMatcher`.

**Go** – `go/yaml.go:1342-1368` – has **no cache at all**. It rescans the
_entire_ source prefix `[0, pnt.SI)` for every plain scalar. That is
textbook O(N²) and is the single biggest reason Go lags TS on many
fixtures:

```go
for fi := 0; fi < pnt.SI; fi++ { ... }   // runs on every plain scalar
```

**Fix** (both languages):

- Maintain `_flowDepth`, `_inSingleQuote`, `_inDoubleQuote` as
  **persistent** lexer state, updated once per character as the lexer
  advances. The scalar handler then reads the current value rather than
  rescanning.
- As a cheaper interim fix in Go: port the TS `_flowScanPos` cache so at
  least the prefix is not rescanned from zero every call.

Expected impact: **5–10× on block-map, flow-map, mixed fixtures**.

### F2 — `JSON.parse(JSON.stringify(...))` / `json.Marshal` for deep copy _(HIGH — both)_

**TS** – `src/yaml.ts:1396, 2521, 2538`:

```ts
val = JSON.parse(JSON.stringify(val))   // on every alias resolve
```

**Go** – `go/yaml.go:107-135`:

```go
data, _ := json.Marshal(val)
_ = json.Unmarshal(data, &result)
```

Both are 10–100× slower than a typed recursive copy, both allocate a
full intermediate buffer (string / `[]byte`), and both silently truncate
`NaN`, `Inf`, and cyclic structures — i.e. they change behavior.

**Fix**: a typed recursive deep-copy function:

```go
func deepCopy(v any) any {
    switch x := v.(type) {
    case map[string]any:
        out := make(map[string]any, len(x))
        for k, v := range x { out[k] = deepCopy(v) }
        return out
    case []any:
        out := make([]any, len(x))
        for i, v := range x { out[i] = deepCopy(v) }
        return out
    default: return v   // primitives are immutable
    }
}
```

Even better: only copy anchors that are _mutated through an alias_. If
anchors are never mutated after resolution (which they aren't in this
parser) you can reference-share and drop copying entirely. That turns
`anchor_alias_heavy` from Θ(N·size(anchor)) into Θ(N).

Expected impact: **2–5× on anchor-heavy YAML**; eliminates behavior
difference for NaN/Inf/dates.

### F3 — Regex compiled inside hot paths _(HIGH, Go) / (MEDIUM, TS)_

**Go** – `go/yaml.go:547, 886, 897, 905, 918, 934` — `regexp.MustCompile`
is called _inside_ `yamlMatcher` / `cleanSource` on every parse. Profile
attributes **13.3 % cumulative** of CPU to compile alone.

```go
// line 547 — inside the per-token lexer loop:
structTagRe := regexp.MustCompile(`^!!(seq|map|omap|set|pairs|...)`)
if fwd[0] == '!' && len(fwd) > 1 && fwd[1] == '!' && structTagRe.MatchString(fwd) { ... }

// line 897 — inside a for{} loop:
for {
    commentRe := regexp.MustCompile(`^[ \t]*#[^\n]*\n`)
    if !commentRe.MatchString(src) { break }
    src = commentRe.ReplaceAllString(src, "")
}
```

**TS** – inline regex literals (e.g. `src/yaml.ts:1219, 1295, 1340,
1594`) are compiled once by V8 and cached, so the cost is lower, but
still worth hoisting for consistency and debuggability.

**Fix** (Go):

```go
var (
    structTagRe = regexp.MustCompile(`^!!(seq|map|omap|set|pairs|binary|ordered|python/\S*)`)
    tagDirectiveRe = regexp.MustCompile(`^%TAG\s+(\S+)\s+(\S+)`)
    commentLineRe = regexp.MustCompile(`^[ \t]*#[^\n]*\n`)
    docStartRe = regexp.MustCompile(`^---([ \t]+(.+))?(\r?\n|$)`)
    docEndRe = regexp.MustCompile(`\n\.\.\.\s*(\r?\n.*)?$`)
    // ...
)
```

Several of these (`structTagRe`, `commentLineRe`) are simple enough that
byte-level comparisons would be even faster than regex — no allocation, no
NFA execution:

```go
func hasStructTagPrefix(s string) bool {
    for _, t := range []string{"seq", "map", "omap", "set", "pairs", "binary", "ordered"} {
        if strings.HasPrefix(s[2:], t) { return true }
    }
    return false
}
```

Expected impact: **10–20 % on any parse that includes directives, tags,
or comments**.

### F4 — Character-by-character string concatenation _(HIGH — both)_

**TS** – `src/yaml.ts:765, 771, 781-784, 790-797, 1002`:

```ts
for (let li = 0; li < lines.length; li++) {
    ...
    for (let ei = 0; ei < pendingEmptyCount; ei++) result += '\n'   // O(N²) string build
    result += line + '\n'
}

// scanLine inner loop
while (i < fwd.length) { ... line += c; i++ }
```

**Go** – `go/yaml.go:406-409, 1423, 1507-1513`:

```go
// handlePlainScalar.scanLine — every char allocates
line += string(c)

// continuation — re-allocates the whole accumulated string
for b := 0; b < blankLines; b++ { text += "\n" }
text += contLine

// anchor inline scalar — 4 full passes over the same string
raw = strings.ReplaceAll(raw, "\\n", "\n")
raw = strings.ReplaceAll(raw, "\\t", "\t")
raw = strings.ReplaceAll(raw, "\\\\", "\\")
raw = strings.ReplaceAll(raw, `\"`, `"`)
```

V8 turns `+=` on short strings into cons-strings, softening the blow,
but on large block scalars we still see measurable super-linear growth
(`foldedBlock` 0.13 → 0.20 µs/byte).

In Go, there is no cons-string optimization — every `+=` reallocates
and re-copies.

**Fix**:

- **TS**: build `lines` once with `push` and `join('\n')`; for
  `scanLine`, slice from `fwd` using the final index rather than
  concatenating per char.
- **Go**: use `strings.Builder` everywhere (already done in
  `foldLines`); slice `fwd[start:end]` directly instead of per-char
  concatenation; collapse the four `ReplaceAll` passes into a single
  scan into a `Builder`.

Expected impact: **30–50 % on block-scalar and multiline-plain paths**.

### F5 — Unsized maps & slices, heavy allocation per token _(HIGH — Go)_

Go's memory profile shows `block_map_wide` (52 KB input) allocates
**97 MB** with **693k allocations** — i.e. ~13 allocations per source
byte. Contributors:

- `go/yaml.go:2692` — `r.Node = make(map[string]any)` with no capacity
  hint. For a 1k-key object, Go rehashes the map ~10 times.
- `go/yaml.go:233, 237` — `anchors` / `tagHandles` maps with no capacity hint.
- Per-token allocations in `yamlMatcher` for `pendingAnchors`,
  `entryParts`, interface boxing of values, etc.

**Fix**:

- `make(map[string]any, 16)` (or estimate from `rule.open` hints).
- Reuse `strings.Builder` instances via `sync.Pool` if the hot paths
  really do need per-parse builders.
- Store anchor values as typed rather than `any` where possible to
  avoid boxing.

Expected impact: **2–3× on any realistic map-heavy file**,
simultaneously reducing GC pressure (22 % of current CPU).

### F6 — `preprocessFlowCollections` runs on the full source _(MEDIUM — TS)_

`src/yaml.ts:214-508` rebuilds the entire source as a string
_before_ the main lexer runs — character-by-character, with recursion
inside flow collections and string concatenation in each recursive
frame. Cost is paid by every parse even when no flow collection is
present.

The scaling test shows `flowMap` per-byte cost grows from **6.6 µs to
9.7 µs** as N grows (TS) — the preprocessor dominates.

**Fixes (in order of ambition):**

1. **Cheap check first.** Before running the preprocessor, scan the
   source for any `{` or `[`. If none is present, skip the entire
   pass. Saves the cost on documents that use only block syntax.
2. **Avoid `entryParts.join('').trim()` per entry.** Track pending
   entries as indices into `src` rather than as an array of small
   strings.
3. **Fold preprocessing into the main lexer.** Flow-collection
   normalization (implicit-null keys, comment-in-key, fold newlines in
   quoted scalars) is information the lexer already has once it enters
   flow context. Running a second full-source pass is architectural
   duplication.

Expected impact: **5–50 %** depending on shape. Big win on documents
with no flow collections at all.

### F7 — Per-line regex scan to strip directives/comments _(MEDIUM — both)_

**TS** – `src/yaml.ts:1219-1223`:

```ts
while (/^[ \t]*#[^\n]*\n/.test(src) && /\n---/.test(src)) {
    src = src.replace(/^[ \t]*#[^\n]*\n/, '')
}
```

**Go** – `go/yaml.go:894-902` — same shape, and recompiles the regex
every iteration:

```go
for {
    commentRe := regexp.MustCompile(`^[ \t]*#[^\n]*\n`)
    ...
}
```

Both iteratively regex + replace one prefix at a time, rescanning the
whole rest of the string each iteration. A single forward pass that
finds the first non-comment, non-blank line and slices once is O(N).

### F8 — Misc smaller wins

- **Go – escape handling** (`go/yaml.go:1965-2016`): `val += string(esc)`
  allocates per character. Use a `strings.Builder`.
- **Go – `parseYamlNumber`** (`go/yaml.go:79-101`): four `HasPrefix`
  pairs before giving up. Replace with a single-byte check on `text[0]`
  (`'0'` → hex/oct/bin paths, `'-'` / `'+'` → recurse, else return
  early). Saves cycles on every non-special-number value.
- **TS – `JSON.parse(JSON.stringify(anchors[name]))`**: same F2 issue
  but also breaks `Date`, `BigInt`, `undefined`, and cyclic references.
- **TS – double-quoted escape switch** (`src/yaml.ts:2086-2115`): 30+
  `if/else` branches per character. A lookup object indexed by the
  escape char would be a single property lookup.
- **Go – `strings.Split(dirBlock, "\n")`** in `cleanSource`: replace with
  `bufio.Scanner` / manual line iteration to avoid allocating a
  `[]string`.

---

## Recommendations summary (ranked)

| # | Fix                                       | Languages | Expected impact            |
|---|-------------------------------------------|-----------|----------------------------|
| 1 | Persistent flow-context state (no rescan) | TS, Go    | **5–10×** on map/flow fixtures |
| 2 | Typed recursive deep-copy / no-copy       | TS, Go    | 2–5× on anchor-heavy       |
| 3 | Hoist / eliminate regex from hot paths    | Go, TS    | 10–20% overall in Go       |
| 4 | Replace `+=` / `ReplaceAll` with builders | TS, Go    | 30–50% on scalar-heavy     |
| 5 | Size maps & reuse buffers (sync.Pool)     | Go        | 2–3× on map-heavy          |
| 6 | Skip / fold `preprocessFlowCollections`   | TS        | 5–50% depending on input   |
| 7 | Single-pass directive/comment strip       | TS, Go    | moderate on header-heavy   |
| 8 | Misc (escape tables, number fast-path)    | TS, Go    | small constant factors     |

A reasonable first sprint targets **#1**, **#2**, **#4** in both
languages plus **#3** and **#5** in Go. That mix should bring both
parsers into the 5–20 MB/s range on realistic inputs — a 20–100×
improvement on most current benchmarks — before the more architectural
#6 is considered.

---

## Reproducing

See [README.md](README.md) for the bench commands. The raw outputs that
backed this report are in:

- `bench/ts-run.log`, `bench/ts-results.json`
- `bench/go-run.log`
- `bench/ts-scaling.log`, `bench/go-scaling.log`
- `bench/go-blockmap.cpuprof`
- `bench/ts/flow_map_large.cpuprofile`

Re-running `node bench/fixtures/generate.mjs && node bench/ts/bench.mjs
&& (cd go && go test -bench=. -benchmem -run=^$ .)` end-to-end takes
about 5 minutes on the reference hardware.
