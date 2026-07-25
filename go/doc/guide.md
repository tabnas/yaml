# How-to guide

Focused recipes for the YAML plugin in Go. Each is self-contained. For a
guided introduction start with the [tutorial](tutorial.md); for exact
signatures and the full syntax list see the [reference](reference.md).

All recipes assume the import:

```go
import tabnasyaml "github.com/tabnas/yaml/go"
```


## Get a mapping (`*jsonic.OrderedMap`) from arbitrary YAML

`tabnasyaml.Parse` returns `any`. Type-assert at the call site, and handle the
case where the top level is not a mapping:

A mapping is returned as a `*jsonic.OrderedMap` (it preserves source key
order); read entries with `Get`, or reach the underlying `map[string]any`
via its `Vals` field when order does not matter.

```go
result, err := tabnasyaml.Parse(src)
if err != nil {
    return nil, err
}
m, ok := result.(*jsonic.OrderedMap)
if !ok {
    return nil, fmt.Errorf("expected mapping at top level, got %T", result)
}
```


## Parse flow collections

Inline `{...}` mappings and `[...]` sequences work anywhere a value is
expected, and nest freely:

```go
tabnasyaml.Parse("data: {name: Bob, tags: [admin, ops]}")
// map[data:map[name:Bob tags:[admin ops]]]
```


## Use block scalars (literal and folded)

`|` keeps newlines; `>` folds them into spaces. Both add a single
trailing newline by default:

```go
tabnasyaml.Parse(`literal: |
  line one
  line two
folded: >
  line one
  line two
`)
// map[folded:"line one line two\n" literal:"line one\nline two\n"]
```

Control the trailing newline with a chomping indicator: `-` strips it,
`+` keeps every trailing blank line. An explicit indent digit (`|2`,
`>-`) sets the content indent. So `|-` strips:

```go
tabnasyaml.Parse("a: |-\n  line1\n  line2")
// map[a:"line1\nline2"]
```


## Reuse nodes with anchors and aliases

Mark a node with `&name`, reference it later with `*name`. The aliased
value is copied in:

```go
tabnasyaml.Parse("a: &items\n  - 1\n  - 2\nb: *items")
// map[a:[1 2] b:[1 2]]
```


## Merge mappings with `<<`

The `<<` merge key copies keys from an aliased mapping into the current
one. Keys already present locally win:

```go
tabnasyaml.Parse(`base: &defaults
  timeout: 30
  retries: 3
prod:
  <<: *defaults
  timeout: 60
`)
// map[base:map[retries:3 timeout:30] prod:map[retries:3 timeout:60]]
```

`prod` keeps its own `timeout: 60` and inherits `retries: 3` from
`base`.


## Coerce types with tags

`!!str`, `!!int`, `!!float`, `!!bool`, and `!!null` force a value's
type, overriding the plain-scalar inference:

```go
tabnasyaml.Parse(`count: !!int "42"
name: !!str 100
`)
// map[count:42 name:100]   // count is float64(42), name is the string "100"
```


## Parse non-decimal integers

Hex (`0x`), octal (`0o`), and binary (`0b`) integer literals resolve to
`float64` (the engine's default numeric type):

```go
tabnasyaml.Parse("{mask: 0xff, perm: 0o755, flags: 0b1010}")
// map[flags:10 mask:255 perm:493]
```


## Handle multi-document streams

`---` starts a document; `...` ends one. One document parses to its
value; two or more parse to a `[]any` of values:

```go
tabnasyaml.Parse(`---
a: 1
---
b: 2
`)
// [map[a:1] map[b:2]]
```


## Capture document metadata with `Meta`

By default the plugin returns bare content. Build a parser with
`MakeJsonic(YamlOptions{Meta: true})` to get a `*MetaResult` envelope
instead. `Content` is exactly what the default path returns; `Meta`
records each document's directives, whether it was explicitly opened
with `---` (`Explicit`), and whether it was explicitly closed with `...`
(`Ended`):

```go
j := tabnasyaml.MakeJsonic(tabnasyaml.YamlOptions{Meta: true})

r, _ := j.Parse("a: 1")
mr := r.(*tabnasyaml.MetaResult)
m := mr.Meta.(*tabnasyaml.DocMeta)
// mr.Content -> map[a:1]
// m.Explicit -> false, m.Ended -> false, m.Directives -> []
```

For a single document `Meta` is a `*DocMeta`; for a stream it is a
`[]*DocMeta`, one entry per document, parallel to the `Content` slice:

```go
j := tabnasyaml.MakeJsonic(tabnasyaml.YamlOptions{Meta: true})

r, _ := j.Parse("%YAML 1.2\n---\na: 1\n---\nb: 2")
mr := r.(*tabnasyaml.MetaResult)
metas := mr.Meta.([]*tabnasyaml.DocMeta)
// metas[0].Directives -> ["%YAML 1.2"]
// metas[1].Directives -> []
```

Directives apply only to the document that immediately follows them.


## Handle a parse error

`Parse` returns an `error` for input it cannot parse. Check it:

```go
result, err := tabnasyaml.Parse(src)
if err != nil {
    return fmt.Errorf("yaml parse failed: %w", err)
}
```

The error is the engine's parse error, carrying a stable code and the
source location of the failure.


## Install as a plugin on your own Jsonic instance

To combine YAML with your own engine options, install the raw `Yaml`
plugin on a `*tabnasjsonic.Jsonic` you built:

```go
j := tabnasjsonic.Make(tabnasjsonic.Options{ /* your options */ })
if err := j.Use(tabnasyaml.Yaml, nil); err != nil {
    return err
}
result, err := j.Parse(src)
```


## Turn the YAML grammar back off

Every rule and alternate the plugin adds is tagged with the rule group
`yaml`. To strip them — reverting to plain relaxed-JSON parsing —
exclude that group with `SetOptions`:

```go
j := tabnasyaml.MakeJsonic()
j.SetOptions(tabnasjsonic.Options{Rule: &tabnasjsonic.RuleOptions{Exclude: "yaml"}})
// j now parses relaxed JSON, without the YAML block-syntax extensions.
```
