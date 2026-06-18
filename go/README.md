# @tabnas/yaml (Go)

A [Tabnas](https://github.com/tabnas/parser) grammar plugin that parses
a core subset of YAML into Go values (`map[string]any`, `[]any`,
`float64`, `string`, `bool`, `nil`), built on the relaxed-JSON
[`jsonic`](https://github.com/tabnas/jsonic) grammar.


## Install

```bash
go get github.com/tabnas/yaml/go
```

Requires `github.com/tabnas/jsonic/go`.


## Example

The simplest entry point is the package-level `Parse`:

```go
package main

import (
    "fmt"
    yaml "github.com/tabnas/yaml/go"
)

func main() {
    result, err := yaml.Parse("name: Alice\nitems:\n  - one\n  - two\n")
    if err != nil {
        panic(err)
    }
    fmt.Println(result)
    // map[items:[one two] name:Alice]
}
```

For repeated parsing, build a reusable parser with `yaml.MakeJsonic()`.


## Documentation

The docs follow the [Diátaxis](https://diataxis.fr) four-quadrant
structure:

- [Tutorial](doc/tutorial.md) — parse your first document, step by step.
- [How-to guide](doc/guide.md) — focused task recipes (flow collections,
  block scalars, anchors, multi-document streams, the `Meta` option).
- [Reference](doc/reference.md) — the public API, every option, and the
  full list of accepted syntax.
- [Concepts](doc/concepts.md) — how the plugin extends the engine, and
  the differences from the TypeScript version.

The TypeScript port lives in [`../ts`](../ts) with its own
[docs](../ts/doc/).


## Grammar diagram

The grammar is shared with the TypeScript implementation, generated from
the live grammar with
[`@tabnas/railroad`](https://github.com/tabnas/railroad):

![yaml grammar railroad diagram](../ts/doc/grammar.svg)

ASCII version: [`../ts/doc/grammar.txt`](../ts/doc/grammar.txt).


## License

MIT. Copyright (c) Richard Rodger.
