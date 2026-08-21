# @tabnas/yaml

<!-- tabnas-badges -->
[![npm](https://tabnas.github.io/status/badges/yaml-npm.svg)](https://www.npmjs.com/package/@tabnas/yaml)
[![CI](https://github.com/tabnas/yaml/actions/workflows/ci.yml/badge.svg)](https://github.com/tabnas/yaml/actions/workflows/ci.yml)
[![go](https://tabnas.github.io/status/badges/yaml-go.svg)](https://pkg.go.dev/github.com/tabnas/yaml/go)
[![tabnas standard](https://tabnas.github.io/status/badges/yaml-standard.svg)](https://tabnas.github.io/status/)
<!-- /tabnas-badges -->

A [Tabnas](https://github.com/tabnas/parser) grammar plugin that parses
a core subset of YAML into plain values, built on the relaxed-JSON
[`jsonic`](https://github.com/tabnas/jsonic) grammar. Available for both
**TypeScript/JavaScript** and **Go** from one shared grammar.

Docs, guides, the error reference and the playground: **[tabnas.dev](https://tabnas.dev)**.

| Path | Description |
|---|---|
| [`ts/`](ts/) | TypeScript / JavaScript implementation. |
| [`go/`](go/) | Go port. |


## Install

```bash
# Node.js
npm install @tabnas/yaml @tabnas/jsonic @tabnas/parser

# Go
go get github.com/tabnas/yaml/go
```


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


## Documentation

The docs follow the [Diátaxis](https://diataxis.fr) four-quadrant
structure (learning / tasks / reference / explanation):

**TypeScript / JavaScript** — [`ts/doc/`](ts/doc/)

- [Tutorial](ts/doc/tutorial.md) — parse your first document, step by step.
- [How-to guide](ts/doc/guide.md) — focused task recipes.
- [Reference](ts/doc/reference.md) — API, options, accepted syntax.
- [Concepts](ts/doc/concepts.md) — how it works, and why.

**Go** — [`go/doc/`](go/doc/)

- [Tutorial](go/doc/tutorial.md) — parse your first document, step by step.
- [How-to guide](go/doc/guide.md) — focused task recipes.
- [Reference](go/doc/reference.md) — API, options, accepted syntax.
- [Concepts](go/doc/concepts.md) — how it works, and the differences from TS.


## Grammar

The grammar is defined once in the top-level
[`yaml-grammar.jsonic`](yaml-grammar.jsonic) and embedded into both the
TypeScript ([`ts/src/yaml.ts`](ts/src/yaml.ts)) and Go
([`go/yaml.go`](go/yaml.go)) implementations by
[`ts/embed-grammar.js`](ts/embed-grammar.js). After editing the grammar
file, re-run the embed step (`make embed`, or `npm run build` in `ts/`)
to re-sync both sources.

The installed grammar as a railroad/syntax diagram, generated from the
live grammar with [`@tabnas/railroad`](https://github.com/tabnas/railroad):

![yaml grammar railroad diagram](ts/doc/grammar.svg)

An ASCII version is in [`ts/doc/grammar.txt`](ts/doc/grammar.txt).


## License

MIT. Copyright (c) Richard Rodger.
