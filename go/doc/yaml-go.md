# YAML plugin for Jsonic (Go)

A Jsonic syntax plugin that parses a core subset of YAML into Go
values (`map[string]any`, `[]any`, `float64`, `string`, `bool`, `nil`),
including block and flow collections, anchors, aliases, merge keys,
tags, block scalars, and multi-document streams.

```bash
go get github.com/jsonicjs/yaml/go
```

Requires `github.com/jsonicjs/jsonic/go` >= v0.1.19.


## Tutorials

### Parse your first YAML document

The simplest entry point is the top-level `Parse` function:

```go
package main

import (
    "fmt"
    yaml "github.com/jsonicjs/yaml/go"
)

func main() {
    result, err := yaml.Parse(`name: Alice
items:
  - one
  - two
flags:
  debug: true
`)
    if err != nil {
        panic(err)
    }
    fmt.Println(result)
    // map[flags:map[debug:true] items:[one two] name:Alice]
}
```

### Parse many documents with one parser

For repeated parsing, reuse a Jsonic instance created with `MakeJsonic`
to avoid re-building the grammar on each call:

```go
j := yaml.MakeJsonic()

for _, src := range inputs {
    result, err := j.Parse(src)
    if err != nil {
        return err
    }
    use(result)
}
```

### Parse anchors and aliases

Define a node with `&name`, reuse it with `*name`; merge with `<<`:

```go
result, _ := yaml.Parse(`base: &defaults
  timeout: 30
  retries: 3
prod:
  <<: *defaults
  timeout: 60
`)
// map[
//   base: map[timeout:30 retries:3]
//   prod: map[timeout:60 retries:3]
// ]
```


## How-to guides

### Get a `map[string]any` from arbitrary YAML

`yaml.Parse` returns `any`. Type-assert at the call site:

```go
result, err := yaml.Parse(src)
if err != nil {
    return nil, err
}
m, ok := result.(map[string]any)
if !ok {
    return nil, fmt.Errorf("expected mapping at top level, got %T", result)
}
```

### Parse flow collections

Inline `{}` and `[]` collections work anywhere a value is expected:

```go
yaml.Parse("data: {name: Bob, tags: [admin, ops]}")
// map[data: map[name:Bob tags:[admin ops]]]
```

### Use block scalars (literal / folded)

```go
yaml.Parse(`literal: |
  line one
  line two
folded: >
  line one
  line two
`)
// map[literal:"line one\nline two\n" folded:"line one line two\n"]
```

Chomping indicators (`-` strip, `+` keep) and explicit indent digits
(`|2`, `>-`) are supported.

### Parse numeric literals beyond decimals

Hex (`0x`), octal (`0o`), and binary (`0b`) integers resolve to
`float64` (Jsonic's default numeric type):

```go
yaml.Parse("{mask: 0xff, perm: 0o755, flags: 0b1010}")
// map[mask:255 perm:493 flags:10]
```

### Use tags for explicit types

```go
yaml.Parse(`count: !!int "42"
name: !!str 100
`)
// map[count:42 name:"100"]
```

### Install as a plugin on your own Jsonic instance

Use the raw plugin with a pre-configured `*jsonic.Jsonic`:

```go
j := jsonic.Make(jsonic.Options{ /* your options */ })
if err := j.Use(yaml.Yaml, nil); err != nil {
    return err
}
result, err := j.Parse(src)
```


## Explanation

### How the plugin works

The YAML plugin extends Jsonic's grammar and lexer:

1. **Custom tokens** — `#IN` (indent) and `#EL` (element marker `- `)
   are added so indentation-sensitive structure is expressible in the
   grammar.
2. **A custom lexer matcher** runs at priority `500000` (before
   Jsonic's built-ins) and handles YAML-only syntax: block scalars
   (`|` / `>`), quoted scalars, anchors/aliases, tags, doc markers,
   explicit keys (`?`), and line-column-aware indent emission.
3. **Grammar amendments** — the declarative grammar in
   `yaml-grammar.jsonic` prepends alts to Jsonic's `val`, `map`,
   `pair`, `list`, and `elem` rules, and introduces new rules
   (`indent`, `yamlBlockList`, `yamlBlockElem`, `yamlElemMap`,
   `yamlElemPair`) for block-style YAML.
4. **State handlers** (`bo`/`ao`/`bc`/`ac`) in `go/grammar.go` manage
   per-parse state: anchor table, pending anchors, merge-key
   resolution, and tag handle mapping.

### The `yaml-grammar.jsonic` file

Grammar alts live in a declarative `.jsonic` file at the repo root and
are embedded into `go/grammar.go` between `BEGIN/END` markers. Run
`make embed` (or `node embed-grammar.js`) to re-sync after editing.
All alts are tagged `G: "yaml"` so callers can use
`j.Exclude("yaml")` to strip them.

### Trade-offs and limitations

- **Core subset**: the plugin covers the most common YAML constructs
  but not the full YAML 1.2 specification. In particular, complex
  mapping keys (non-scalar keys), set (`!!set`) and ordered map
  (`!!omap`) shorthand, and some corner cases of folding behavior are
  not yet handled.
- **Number parsing** follows YAML 1.1-ish behavior (`yes`/`no` are
  booleans). If you need strict YAML 1.2, treat those as strings.
- **Numeric type**: all numbers come back as `float64`. Cast to `int`
  at the call site when appropriate.
- **Security**: the plugin does not implement tag restrictions or a
  "safe" mode; do not use it as a generic deserializer for untrusted
  input without reviewing the result.


## Reference

### Functions

#### `Parse`

```go
func Parse(src string) (any, error)
```

Parses a YAML string and returns the resulting Go value. Equivalent
to `MakeJsonic().Parse(src)` but does not reuse a parser across calls.

#### `MakeJsonic`

```go
func MakeJsonic(opts ...YamlOptions) *jsonic.Jsonic
```

Creates a `*jsonic.Jsonic` configured for YAML parsing. Reuse the
returned instance for repeated parses.

#### `Yaml` (plugin)

```go
func Yaml(j *jsonic.Jsonic, opts map[string]any) error
```

The raw plugin function. Install on an existing Jsonic instance via
`j.Use(yaml.Yaml, nil)`.

### Types

#### `YamlOptions`

```go
type YamlOptions struct{}
```

Reserved for future options; currently empty.

### Supported YAML features

| Feature                        | Example                         |
| ------------------------------ | ------------------------------- |
| Block mapping                  | `a: 1`                          |
| Block sequence                 | `- one`                         |
| Flow mapping                   | `{a: 1}`                        |
| Flow sequence                  | `[1, 2]`                        |
| Double-quoted scalar           | `"hi\n"`                        |
| Single-quoted scalar           | `'it''s'`                       |
| Literal block scalar           | `\|`                            |
| Folded block scalar            | `>`                             |
| Anchor / alias                 | `&x 1`, `*x`                    |
| Merge key                      | `<<: *x`                        |
| Tag                            | `!!int "42"`                    |
| `%TAG` directive               | `%TAG !e! tag:example.com/`     |
| Document markers               | `---`, `...`                    |
| Value keywords                 | `true`, `null`, `yes`, `.inf`   |
| Non-decimal integers           | `0xff`, `0o755`, `0b1010`       |
| Line comments                  | `# comment`                     |

### Return types

| YAML              | Go                |
| ----------------- | ----------------- |
| mapping           | `map[string]any`  |
| sequence          | `[]any`           |
| number            | `float64`         |
| string            | `string`          |
| `true` / `false`  | `bool`            |
| `null` / `~`      | `nil`             |
| `.inf` / `.nan`   | `math.Inf(1)` etc |
