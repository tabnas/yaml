# @tabnas/yaml

A [Tabnas](https://github.com/tabnas/parser) grammar plugin that parses
a core subset of YAML into plain JavaScript values, built on the
relaxed-JSON [`jsonic`](https://github.com/tabnas/jsonic) grammar.

[![npm version](https://img.shields.io/npm/v/@tabnas/yaml.svg)](https://www.npmjs.com/package/@tabnas/yaml)
[![license](https://img.shields.io/npm/l/@tabnas/yaml.svg)](https://github.com/tabnas/yaml/blob/main/LICENSE)


## Install

```bash
npm install @tabnas/yaml @tabnas/jsonic @tabnas/parser
```

`@tabnas/jsonic` (>= 2) and `@tabnas/parser` (>= 2) are peer
dependencies.


## Example

Register the plugin on a Tabnas engine that already has the `jsonic`
grammar, then parse:

```js
const { Tabnas } = require('@tabnas/parser')
const { jsonic } = require('@tabnas/jsonic')
const { Yaml } = require('@tabnas/yaml')

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("name: Alice\nitems:\n  - one\n  - two\n")   // => { name: 'Alice', items: ['one', 'two'] }
```


## Documentation

The docs follow the [Diátaxis](https://diataxis.fr) four-quadrant
structure:

- [Tutorial](doc/tutorial.md) — parse your first document, step by step.
- [How-to guide](doc/guide.md) — focused task recipes (flow collections,
  block scalars, anchors, multi-document streams, the `meta` option).
- [Reference](doc/reference.md) — the public API, every option, and the
  full list of accepted syntax.
- [Concepts](doc/concepts.md) — how the plugin extends the engine, and
  why.

The Go port lives in [`../go`](../go) with its own
[docs](../go/doc/).


## Grammar diagram

The installed grammar as a railroad/syntax diagram, generated from the
live grammar with [`@tabnas/railroad`](https://github.com/tabnas/railroad):

![yaml grammar railroad diagram](doc/grammar.svg)

ASCII version: [`doc/grammar.txt`](doc/grammar.txt).


## License

MIT. Copyright (c) Richard Rodger.
