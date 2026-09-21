# @tabnas/yaml

<!-- tabnas-badges -->
[![npm](https://tabnas.github.io/status/badges/yaml-npm.svg)](https://www.npmjs.com/package/@tabnas/yaml)
[![CI](https://github.com/tabnas/yaml/actions/workflows/ci.yml/badge.svg)](https://github.com/tabnas/yaml/actions/workflows/ci.yml)
[![go](https://tabnas.github.io/status/badges/yaml-go.svg)](https://pkg.go.dev/github.com/tabnas/yaml/go)
[![tabnas standard](https://tabnas.github.io/status/badges/yaml-standard.svg)](https://tabnas.github.io/status/)
<!-- /tabnas-badges -->

A [Tabnas](https://github.com/tabnas/parser) grammar plugin that parses
a core subset of YAML into plain values, built on the relaxed-JSON
[`jsonic`](https://github.com/tabnas/jsonic) grammar. Available for
**TypeScript/JavaScript**, **Go** and **Rust** from one shared grammar.

Docs, guides, the error reference and the playground: **[tabnas.dev](https://tabnas.dev)**.

| Path | Description |
|---|---|
| [`ts/`](ts/) | TypeScript / JavaScript implementation. |
| [`go/`](go/) | Go port. |
| [`rs/`](rs/) | Rust port, crate `tabnas-yaml`. See [`rs/README.md`](rs/README.md). |


## Install

```bash
# Node.js
npm install @tabnas/yaml @tabnas/jsonic @tabnas/parser

# Go
go get github.com/tabnas/yaml/go
```

Rust is not published to a registry. Clone this repository beside
checkouts of `parser`, `jsonic`, `json` and `support`, and take it as a
path dependency; see [`rs/README.md`](rs/README.md).


## One tiny example

TypeScript / JavaScript:

```js
const { Tabnas } = require('@tabnas/parser')
const { jsonic } = require('@tabnas/jsonic')
const { Yaml } = require('@tabnas/yaml')

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("name: Alice\nitems:\n  - one\n  - two\n")   // => { name: 'Alice', items: ['one', 'two'] }
```

Go:

```go
import tabnasyaml "github.com/tabnas/yaml/go"

result, _ := tabnasyaml.Parse("name: Alice\nitems:\n  - one\n  - two\n")
// map[items:[one two] name:Alice]
```

Rust:

```rust
let value = tabnas_yaml::parse("name: Alice\nitems:\n  - one\n  - two\n")?;
// {"name":"Alice","items":["one","two"]}
```


## Documentation

The docs follow the [Diátaxis](https://diataxis.fr) four-quadrant
structure (learning / tasks / reference / explanation):

**TypeScript / JavaScript**: [`ts/doc/`](ts/doc/)

- [Tutorial](ts/doc/tutorial.md). Parse your first document, step by step.
- [How-to guide](ts/doc/guide.md). Focused task recipes.
- [Reference](ts/doc/reference.md). API, options, accepted syntax.
- [Concepts](ts/doc/concepts.md). How it works, and why.

**Go**: [`go/doc/`](go/doc/)

- [Tutorial](go/doc/tutorial.md). Parse your first document, step by step.
- [How-to guide](go/doc/guide.md). Focused task recipes.
- [Reference](go/doc/reference.md). API, options, accepted syntax.
- [Concepts](go/doc/concepts.md). How it works, and the differences from TS.


## Grammar

The grammar is defined once in the top-level
[`yaml-grammar.jsonic`](yaml-grammar.jsonic) and embedded into the
TypeScript ([`ts/src/yaml.ts`](ts/src/yaml.ts)), Go
([`go/yaml.go`](go/yaml.go)) and Rust
([`rs/src/lib.rs`](rs/src/lib.rs)) implementations by
[`ts/embed-grammar.js`](ts/embed-grammar.js). After editing the grammar
file, re-run the embed step (`make embed`, or `npm run build` in `ts/`)
to re-sync every source.

The installed grammar as a railroad/syntax diagram, generated from the
live grammar with [`@tabnas/railroad`](https://github.com/tabnas/railroad):

![yaml grammar railroad diagram](ts/doc/grammar.svg)

An ASCII version is in [`ts/doc/grammar.txt`](ts/doc/grammar.txt).


## License

MIT. Copyright (c) Richard Rodger.
