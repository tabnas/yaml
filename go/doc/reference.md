# Reference

Dry and complete: the public API, every option, and the YAML syntax the
plugin accepts. For a guided start see the [tutorial](tutorial.md); for
task recipes see the [how-to guide](guide.md); for design background and
the differences from the TypeScript version see [concepts](concepts.md).


## Package

```go
import tabnasyaml "github.com/tabnas/yaml/go"
```

```bash
go get github.com/tabnas/yaml/go
```

Requires `github.com/tabnas/jsonic/go`.


## Functions

### `Parse`

```go
func Parse(src string) (any, error)
```

Parses a YAML string and returns the resulting Go value. Uses a single
lazily-built parser instance shared across calls (building the YAML
grammar dominates a parse), so repeated calls do not rebuild the engine.
The shared instance only reads instance state during a parse, so `Parse`
is safe for concurrent use.

```go
result, err := tabnasyaml.Parse("name: Alice\nitems:\n  - one\n  - two\n")
// result == map[string]any{"name": "Alice", "items": []any{"one", "two"}}
```

### `MakeJsonic`

```go
func MakeJsonic(opts ...YamlOptions) *tabnasjsonic.Jsonic
```

Creates a `*tabnasjsonic.Jsonic` configured for YAML parsing. Pass an optional
`YamlOptions` to configure the plugin (only the first is used). Reuse
the returned instance for repeated parses, or to apply further engine
options.

```go
j := tabnasyaml.MakeJsonic(tabnasyaml.YamlOptions{Meta: true})
result, err := j.Parse(src)
```

### `Yaml`

```go
func Yaml(j *tabnasjsonic.Jsonic, opts map[string]any) error
```

The raw plugin function. Install it on an existing `*tabnasjsonic.Jsonic` with
`j.Use(tabnasyaml.Yaml, opts)`. `MakeJsonic` is the convenience wrapper around
this; reach for `Yaml` directly only when you build the engine yourself.
The `opts` map honours the key `"meta"` (a `bool`), matching
`YamlOptions.Meta`.


## Types

### `YamlOptions`

```go
type YamlOptions struct {
    Meta bool
}
```

| Field  | Type   | Default | Effect |
| ------ | ------ | ------- | ------ |
| `Meta` | `bool` | `false` | When `false`, `Parse` returns the bare document value (or `[]any` for a multi-document stream). When `true`, `Parse` returns a `*MetaResult` envelope (see below). |

### `MetaResult`

Returned (as `*MetaResult`) when `YamlOptions.Meta` is `true`:

```go
type MetaResult struct {
    Meta    any // *DocMeta (single doc) or []*DocMeta (multi-doc)
    Content any // the doc value (single) or []any (multi-doc)
}
```

`Content` is exactly what the `Meta: false` path returns. `Meta` is a
`*DocMeta` for a single document, or a `[]*DocMeta` parallel to the
`Content` slice for a stream. Type-assert at the call site:

```go
r, _ := tabnasyaml.MakeJsonic(tabnasyaml.YamlOptions{Meta: true}).Parse("a: 1")
mr := r.(*tabnasyaml.MetaResult)
m := mr.Meta.(*tabnasyaml.DocMeta)   // single doc
```

### `DocMeta`

```go
type DocMeta struct {
    Directives []string // raw directive lines for this doc, e.g. ["%YAML 1.2"]
    Explicit   bool     // true if the doc was opened with `---`
    Ended      bool     // true if the doc was closed with `...`
}
```

### `Version`

```go
const Version = "0.7.0"
```

The module version.


## Return value

### With `Meta: false` (default)

| Source                             | Returns          |
| ---------------------------------- | ---------------- |
| empty / whitespace / comments-only | `nil`            |
| single document                    | the doc value    |
| multi-document stream              | `[]any` of values|

The document value follows these YAML-to-Go mappings:

| YAML            | Go                |
| --------------- | ----------------- |
| mapping         | `map[string]any`  |
| sequence        | `[]any`           |
| number          | `float64`         |
| string          | `string`          |
| `true` / `false`| `bool`            |
| `null` / `~`    | `nil`             |
| `.inf` / `.nan` | `math.Inf(1)`, `math.NaN()` |

All numbers — including hex/octal/binary integers — come back as
`float64`. Cast to `int` at the call site when appropriate.

### With `Meta: true`

`Parse` returns `*MetaResult` (see the type above).


## Accepted syntax

| Feature                | Example                       | Result |
| ---------------------- | ----------------------------- | ------ |
| Block mapping          | `a: 1`                        | `map[a:1]` |
| Block sequence         | `- a`                         | `[a]` |
| Sequence of mappings   | `- name: alice`               | `[map[name:alice]]` |
| Flow mapping           | `{a: 1, b: 2}`                | `map[a:1 b:2]` |
| Flow sequence          | `[1, 2, 3]`                   | `[1 2 3]` |
| Double-quoted scalar   | `"line1\nline2"`              | `"line1\nline2"` (escapes processed) |
| Single-quoted scalar   | `'it''s'`                     | `"it's"` (`''` is a literal `'`) |
| Quoted-forced string   | `"42"`                        | `"42"` (stays a string) |
| Plain scalar           | `hello world`                 | `"hello world"` |
| Literal block scalar   | <code>&#124;</code>           | newlines preserved, one trailing `\n` |
| Folded block scalar    | `>`                           | newlines folded to spaces, one trailing `\n` |
| Chomping indicators    | <code>&#124;-</code>, `>+`    | strip / keep trailing newlines |
| Block-scalar indent    | <code>&#124;2</code>          | explicit content indent |
| Anchor / alias         | `&x 1`, `*x`                  | aliased value copied in |
| Merge key              | `<<: *x`                      | keys merged, local keys win |
| Type tag               | `!!int "42"`                  | `float64(42)` (coerced) |
| `%TAG` directive       | `%TAG !e! tag:example.com/`   | captured (see `Meta.Directives`) |
| Explicit flow key      | `{? k : v}`                   | `map[k:v]` |
| Document start / end   | `---`, `...`                  | document boundaries |
| Value keywords         | `true`, `null`, `yes`, `~`    | `true`, `nil`, `true`, `nil` |
| Non-decimal integers   | `0xff`, `0o755`, `0b1010`     | `255`, `493`, `10` |
| Line comments          | `# comment`                   | ignored |

### Value keywords

These plain scalars resolve to non-string values (case variants like
`True`, `NULL`, `.Inf` are also accepted):

| Keyword(s)             | Value         |
| ---------------------- | ------------- |
| `true` / `yes` / `on`  | `true`        |
| `false` / `no` / `off` | `false`       |
| `null` / `~`           | `nil`         |
| `.inf` / `-.inf`       | `math.Inf(±1)`|
| `.nan`                 | `math.NaN()`  |


## Tokens

The plugin adds YAML-specific lexer tokens, surfaced in the railroad
diagram legend (`ts/doc/grammar.svg`):

| Token | Meaning |
| ----- | ------- |
| `#IN` | line indentation (count of leading spaces) |
| `#EL` | block sequence item dash `- ` |
| `#DS` | document start marker `---` (column 0) |
| `#DE` | document end marker `...` (column 0) |
| `#DR` | directive line, e.g. `%YAML` / `%TAG` (column 0) |
| `#QM` | explicit-key marker `?` in flow `{? k : v }` |


## Grammar / rules

The plugin parses with a `stream` start rule (it replaces jsonic's
default `val` start). `stream` consumes the document-frame tokens
(`#DS` / `#DE` / `#DR`) and pushes a `val` rule per document. The block
rules it adds, alongside jsonic's `val` / `map` / `pair` / `list` /
`elem`, are:

| Rule | Role |
| ---- | ---- |
| `stream` | top-level document collector |
| `indent` | start of block content at a given indent |
| `yamlBlockList` | block sequence (`- ` items) |
| `yamlBlockElem` | subsequent items in a block sequence |
| `yamlElemMap` | a mapping that is a sequence element (`- key: val`) |
| `yamlElemPair` | additional pairs within a `yamlElemMap` |

Every alternate the plugin adds is tagged with the rule group `yaml`, so
the whole extension can be removed from an instance with
`j.SetOptions(tabnasjsonic.Options{Rule: &tabnasjsonic.RuleOptions{Exclude: "yaml"}})`,
reverting to relaxed-JSON parsing.

The grammar is authored once in the repo-root
[`yaml-grammar.jsonic`](../../yaml-grammar.jsonic) and embedded into
`go/yaml.go` (and `ts/src/yaml.ts`) by `embed-grammar.js`. The current
installed grammar as a railroad diagram is
[`ts/doc/grammar.svg`](../../ts/doc/grammar.svg), with an ASCII version
in [`ts/doc/grammar.txt`](../../ts/doc/grammar.txt).


## Limitations

- A core subset of YAML 1.2, not the full specification. Non-scalar
  complex keys, set (`!!set`) and ordered-map (`!!omap`) shorthand, and
  some folding corner cases are not handled.
- Keyword handling is YAML-1.1-flavoured: `yes`/`no`/`on`/`off` are
  booleans. Quote them if you need them as strings.
- All numbers are `float64`; cast to `int` at the call site.
- No "safe" mode or tag restriction. Review parsed results before using
  them as a deserializer for untrusted input.
