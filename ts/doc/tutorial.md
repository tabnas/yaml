# Tutorial — parse your first YAML document

This walks you from nothing to a working parse, in order. Each step
builds on the last. When you finish you will have installed the plugin,
registered it on a parser, parsed a small block document, and read the
result back as a plain JavaScript object.

For a recipe-style index of individual tasks, see the
[how-to guide](guide.md). For exhaustive signatures, see the
[reference](reference.md). For how it all fits together, see
[concepts](concepts.md).


## 1. Install

`@tabnas/yaml` is a plugin for the Tabnas parser engine. Install it
alongside the engine and the relaxed-JSON grammar it builds on:

```bash
npm install @tabnas/yaml @tabnas/jsonic @tabnas/parser
```

`@tabnas/jsonic` (>= 2) and `@tabnas/parser` (>= 2) are peer
dependencies — you supply them.


## 2. Register the plugin

`Yaml` is a Tabnas *plugin*. Create an engine instance, add the
`jsonic` grammar, then add `Yaml` on top with `.use()`:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)
```

The order matters: `Yaml` amends the relaxed-JSON grammar that `jsonic`
installs, so `jsonic` must come first. The resulting `j` is a reusable
parser — make it once, parse as many times as you like.


## 3. Parse a document

Call `j.parse(...)` with a YAML string. Indentation drives structure:
`key: value` makes a mapping, `- item` makes a sequence.

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`name: Alice
items:
  - one
  - two
flags:
  debug: true
`)

out   // => { name: 'Alice', items: ['one', 'two'], flags: { debug: true } }
```

You get back an ordinary object. Strings stay strings, `true` becomes a
boolean, the indented `-` lines become an array, and the nested
`flags:` block becomes a nested object — no schema, no annotations.


## 4. Look at how scalars are typed

The plugin recognises YAML's plain-scalar conventions while parsing.
Numbers parse as numbers, the value keywords parse to their JavaScript
equivalents, and everything else is a string:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`port: 5432
enabled: yes
note: ~
title: hello world
`)

out   // => { port: 5432, enabled: true, note: null, title: 'hello world' }
```

`yes` is a boolean (YAML's keyword set is broad — `yes`/`no`,
`on`/`off`, `true`/`false`), `~` is null, and `hello world` — spaces and
all — is a single unquoted string up to the end of the line.


## 5. Parse a nested structure

Block mappings and sequences nest by indentation to any depth:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`server:
  host: localhost
  port: 5432
  tags:
    - web
    - api
`)

out   // => { server: { host: 'localhost', port: 5432, tags: ['web', 'api'] } }
```

That is the whole happy path: install, register, parse, read the
object.


## Where to go next

- [How-to guide](guide.md) — focused recipes (flow collections, block
  scalars, anchors, multi-document streams, the `meta` option).
- [Reference](reference.md) — the public API, every option, and the full
  list of accepted syntax.
- [Concepts](concepts.md) — how the plugin extends the engine, and why.
