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
lazily built parser instance shared across calls (building the YAML
grammar dominates a parse), so repeated calls do not rebuild the engine.
The shared instance only reads instance state during a parse, so `Parse`
is safe for concurrent use.

```go
result, err := tabnasyaml.Parse("name: Alice\nitems:\n  - one\n  - two\n")
// result is a *jsonic.OrderedMap holding, in source order:
//   "name" -> "Alice", "items" -> []any{"one", "two"}
```

### `MakeJsonic`

```go
func MakeJsonic(opts ...YamlOptions) *tabnas.Tabnas
```

Creates a `*tabnas.Tabnas` configured for YAML parsing: jsonic's grammar
with the plugin installed. Pass an optional `YamlOptions` to configure
the plugin (only the first is used). Reuse the returned instance for
repeated parses, or to apply further engine options. `MakeJsonic` has no
error to return, so it panics on a `Schema` the plugin doesn't know.

```go
j := tabnasyaml.MakeJsonic(tabnasyaml.YamlOptions{Meta: true})
result, err := j.Parse(src)
```

The instance declares the engine option `lex.window`, so `ParseReader`
reads an `io.Reader` a window at a time rather than whole, with the same
values and errors as `Parse` of the same text:

```go
result, err := tabnasyaml.MakeJsonic().ParseReader(file)
```

`Yaml` itself declares nothing, because a host may install it beneath
matchers of its own.

### `Yaml`

```go
func Yaml(j *tabnasjsonic.Jsonic, opts map[string]any) error
```

The raw plugin function. Install it on an existing `*tabnasjsonic.Jsonic` with
`j.Use(tabnasyaml.Yaml, opts)`. `MakeJsonic` is the convenience wrapper around
this; reach for `Yaml` directly only when you build the engine yourself.
The `opts` map honours the key `"meta"` (a `bool`), matching
`YamlOptions.Meta`, and the key `"schema"`, whose one value is the
string `"core"`, matching `YamlOptions.Schema`. `Use` returns an error
for any other schema.


## Types

### `YamlOptions`

```go
type YamlOptions struct {
    Meta   bool
    Schema string
}
```

| Field    | Type     | Default | Effect |
| -------- | -------- | ------- | ------ |
| `Meta`   | `bool`   | `false` | When `false`, `Parse` returns the bare document value (or `[]any` for a multi-document stream). When `true`, `Parse` returns a `*MetaResult` envelope (see below). |
| `Schema` | `string` | `""`    | How a plain scalar resolves. Empty, the plugin reads the YAML 1.2 core schema and two things more: the YAML 1.1 booleans `yes`, `no`, `on` and `off`, and jsonic's number syntax, such as `1_000` and `0b11`. With `"core"` it reads the core schema and nothing more, so those are strings: see [Value keywords](#value-keywords). The plugin refuses any other value when you install it. |

```go
core := tabnasyaml.MakeJsonic(tabnasyaml.YamlOptions{Schema: "core"})
core.Parse("{a: yes, b: 1_000, c: true, d: 12}")
// map[a:yes b:1_000 c:true d:12]
```

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
    Directives []string // raw directive lines for this doc, for example ["%YAML 1.2"]
    Explicit   bool     // true if the doc was opened with `---`
    Ended      bool     // true if the doc was closed with `...`
}
```

### `VERSION`

```go
const VERSION = "0.4.3"
```

The module version. It always equals `ts/package.json` "version";
`TestVersionMatchesPackageJSON` fails the build if the two drift.


## Return value

### With `Meta: false` (default)

| Source                             | Returns          |
| ---------------------------------- | ---------------- |
| empty / whitespace / comments-only | `nil`            |
| single document                    | the doc value    |
| multi-document stream              | `[]any` of values|

The document value follows these YAML-to-Go mappings:

| YAML            | Go                     |
| --------------- | ---------------------- |
| mapping         | `*jsonic.OrderedMap`   |
| sequence        | `[]any`           |
| number          | `float64`         |
| string          | `string`          |
| `true` / `false`| `bool`            |
| `null` / `~`    | `nil`             |
| `.inf` / `.nan` | `math.Inf(1)`, `math.NaN()` |

All numbers (including hex/octal/binary integers) come back as
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
| Quoted merge key       | `"<<": 1`                     | `map[<<:1]`, an ordinary key |
| Repeated key           | `a: {b: 1}` then `a: {c: 2}`  | `map[a:map[b:1 c:2]]`, merged as jsonic merges, or the later value with `Map.Extend` false |
| Type tag               | `!!int "42"`                  | `float64(42)` (coerced) |
| `%TAG` directive       | `%TAG !e! tag:example.com/`   | captured (see `Meta.Directives`) |
| Explicit flow key      | `{? k : v}`                   | `map[k:v]` |
| Document start / end   | `---`, `...`                  | document boundaries |
| Value keywords         | `true`, `null`, `yes`, `~`    | `true`, `nil`, `true`, `nil`. Under `Schema: "core"`, `yes` is `"yes"` |
| Non-decimal integers   | `0xff`, `0o755`, `0b1010`     | `255`, `493`, `10`. Under `Schema: "core"`, `0b1010` is a string |
| Line comments          | `# comment`                   | ignored |
| Line breaks            | `\n`, `\r\n`, `\r`            | each is one line break |

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

`yes`, `no`, `on` and `off` are the YAML 1.1 booleans. The YAML 1.2 core
schema reads them as strings, and so does the plugin under
`Schema: "core"`. Under that option a number is one the core schema
names and no other: a decimal integer or float, a `0o` octal integer or
a `0x` hexadecimal one. So `1_000`, `0b11`, `0X1F`, `-0x30` and
`Infinity` are strings there. The
[YAML Test Schema](https://github.com/perlpunk/yaml-test-schema) corpus
defines the option: under it, every one of the corpus's 245 scalars
reads as the core schema says.

## Error codes

A refused document returns the engine's `*tabnas.TabnasError`, whose
`Code` is stable. The plugin adds three codes of its own to the engine's
and jsonic's:

| Code | Raised when |
| ---- | ----------- |
| `yaml_undefined_alias` | An alias such as `*name` names an anchor that doesn't occur before it in the document. |
| `yaml_recursive_alias` | An alias is inside the node its anchor names, as in `&a [*a]`, so the node would hold itself. The plugin gives a tree, which can't. |
| `yaml_tab_indent` | A tab stands in a line's indentation before a mapping key or a sequence entry. YAML indents block structure with spaces alone. A tab before a scalar, a flow collection or a comment is white space. |

Every other refusal carries one of the engine's codes, most often
`unexpected`. An error's `Row` and `Col` are the place its offset has in
the source: the line, from 1, and how far along it in characters, from 1.


## Tokens

The plugin adds YAML-specific lexer tokens, surfaced in the railroad
diagram legend (`ts/doc/grammar.svg`):

| Token | Meaning |
| ----- | ------- |
| `#IN` | line indentation (count of leading spaces) |
| `#EL` | block sequence item dash `- ` |
| `#DS` | document start marker `---` (column 0) |
| `#DE` | document end marker `...` (column 0) |
| `#DR` | directive line, for example `%YAML` / `%TAG` (column 0) |
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
| `yamlElemPair` | each pair within a `yamlElemMap`, the first included |

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
- Keyword handling is YAML-1.1-flavoured unless you ask otherwise:
  `yes`/`no`/`on`/`off` are booleans. Quote them if you need them as
  strings, or set `Schema: "core"`.
- The plugin refuses an alias inside the node it names. YAML allows it,
  and it describes a node that holds itself, which a tree can't.
- All numbers are `float64`; cast to `int` at the call site.
- No "safe" mode or tag restriction. Review parsed results before using
  them as a deserializer for untrusted input.
