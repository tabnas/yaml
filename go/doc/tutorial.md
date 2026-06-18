# Tutorial — parse your first YAML document

This walks you from nothing to a working parse, in order. Each step
builds on the last. When you finish you will have installed the
package, parsed a small block document with the top-level `Parse`
function, and read the result back as a Go `map[string]any`.

For a recipe-style index of individual tasks, see the
[how-to guide](guide.md). For exact signatures see the
[reference](reference.md). For how it all fits together — and how the Go
port differs from the TypeScript one — see [concepts](concepts.md).


## 1. Install

```bash
go get github.com/tabnas/yaml/go
```

Import it with a short alias:

```go
import tabnasyaml "github.com/tabnas/yaml/go"
```


## 2. Parse a document

The simplest entry point is the package-level `Parse` function. It takes
a string and returns `(any, error)`. Indentation drives structure:
`key: value` makes a mapping, `- item` makes a sequence.

```go
package main

import (
    "fmt"
    tabnasyaml "github.com/tabnas/yaml/go"
)

func main() {
    result, err := tabnasyaml.Parse(`name: Alice
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

A top-level mapping comes back as `map[string]any`. The indented `-`
lines become a `[]any`, `true` becomes a `bool`, and the nested `flags:`
block becomes a nested `map[string]any`.


## 3. Reach into the result

`Parse` returns `any`, so type-assert to the concrete shape you expect:

```go
result, _ := tabnasyaml.Parse(`name: Alice
port: 5432
`)

m := result.(map[string]any)
name := m["name"].(string)   // "Alice"
port := m["port"].(float64)  // 5432 — all numbers are float64
```

Numbers always come back as `float64` (the engine's default numeric
type); cast to `int` at the call site when you need one.


## 4. Look at how scalars are typed

The plugin recognises YAML's plain-scalar conventions while parsing.
Numbers parse as numbers, the value keywords parse to their Go
equivalents, and everything else is a string:

```go
result, _ := tabnasyaml.Parse(`port: 5432
enabled: yes
note: ~
title: hello world
`)
// map[enabled:true note:<nil> port:5432 title:hello world]
```

`yes` is a `bool` (YAML's keyword set is broad — `yes`/`no`, `on`/`off`,
`true`/`false`), `~` is `nil`, and `hello world` — spaces and all — is a
single unquoted string up to the end of the line.


## 5. Reuse one parser for many inputs

`Parse` builds a fresh engine on every call. When you parse repeatedly,
build the parser once with `MakeJsonic` and reuse it:

```go
j := tabnasyaml.MakeJsonic()

for _, src := range inputs {
    result, err := j.Parse(src)
    if err != nil {
        return err
    }
    use(result)
}
```

Building the YAML grammar dominates a parse, so reusing the instance is
the meaningful optimisation. (The package-level `Parse` already shares a
single lazily-built instance internally; `MakeJsonic` gives you your own
to configure.)


## Where to go next

- [How-to guide](guide.md) — focused recipes (flow collections, block
  scalars, anchors, multi-document streams, the `Meta` option).
- [Reference](reference.md) — the public API, every option, and the full
  list of accepted syntax.
- [Concepts](concepts.md) — how the plugin extends the engine, and how
  the Go port differs from TypeScript.
