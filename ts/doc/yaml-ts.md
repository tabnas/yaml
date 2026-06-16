# YAML plugin for Jsonic (TypeScript)

A Jsonic syntax plugin that parses a core subset of YAML into plain
JavaScript objects, including block and flow collections, anchors,
aliases, merge keys, tags, block scalars, and multi-document streams.

```bash
npm install @tabnas/yaml @tabnas/jsonic @tabnas/parser
```

Requires `@tabnas/jsonic` >= 2 and `@tabnas/parser` >= 2 as peer
dependencies.


## Tutorials

### Parse your first YAML document

Install the plugin, register it with a Jsonic instance, then parse:

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

### Parse block mappings and sequences

Indentation drives structure. Mappings use `key: value`; sequences use
`- item`:

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

### Parse anchors and aliases

Define a node with `&name`, reuse it with `*name`:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`base: &defaults
  timeout: 30
  retries: 3
prod:
  <<: *defaults
  timeout: 60
`)

out   // => { base: { timeout: 30, retries: 3 }, prod: { timeout: 60, retries: 3 } }
```

The `<<` merge key copies keys from the aliased mapping; explicit keys
on the current mapping win.


## How-to guides

### Parse flow collections

Inline `{}` and `[]` collections work anywhere a value is expected:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("data: {name: Bob, tags: [admin, ops]}")   // => { data: { name: 'Bob', tags: ['admin', 'ops'] } }
```

### Use block scalars (literal / folded)

Preserve or fold newlines in multi-line strings:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`literal: |
  line one
  line two
folded: >
  line one
  line two
`)

out   // => { literal: 'line one\nline two\n', folded: 'line one line two\n' }
```

Chomping indicators (`-` strip, `+` keep) and explicit indent digits
(`|2`, `>-`) are supported.

### Parse YAML value keywords

`true`/`false`/`yes`/`no`/`on`/`off`, `null`/`~`, `.inf`/`.nan`:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`enabled: yes
retries: ~
max: .inf
`)

out   // => { enabled: true, retries: null, max: null }
```

### Parse numeric literals beyond decimals

Hex (`0x`), octal (`0o`), and binary (`0b`) integers are supported:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("{mask: 0xff, perm: 0o755, flags: 0b1010}")   // => { mask: 255, perm: 493, flags: 10 }
```

### Handle multi-document streams

`---` starts a new document; `...` ends one. A multi-document stream is
returned as an array of documents:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`---
a: 1
---
b: 2
`)

out   // => [{ a: 1 }, { b: 2 }]
```

### Use tags for explicit types

`!!str`, `!!int`, `!!float`, `!!bool`, `!!null` coerce values; custom
tags are preserved via `%TAG` directives:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

const out = j.parse(`count: !!int "42"
name: !!str 100
`)

out   // => { count: 42, name: '100' }
```


## Explanation

### How the plugin works

The YAML plugin extends Jsonic's grammar and lexer:

1. **Custom tokens** — `#IN` (indent) and `#EL` (element marker `- `)
   are added so indentation-sensitive structure is expressible in the
   grammar.
2. **A custom lexer matcher** runs at priority `5e5` (before Jsonic's
   built-ins) and handles YAML-only syntax: block scalars (`|` / `>`),
   quoted scalars, anchors/aliases, tags, doc markers, explicit keys
   (`?`), and line-column-aware indent emission.
3. **Grammar amendments** — the declarative grammar in
   `yaml-grammar.jsonic` prepends alts to Jsonic's `val`, `map`,
   `pair`, `list`, and `elem` rules, and introduces new rules
   (`indent`, `yamlBlockList`, `yamlBlockElem`, `yamlElemMap`,
   `yamlElemPair`) for block-style YAML.
4. **State handlers** (before-open/before-close/after-close) in
   `src/yaml.ts` manage per-parse state: anchor table, pending
   anchors, merge-key resolution, and tag handle mapping.

### The `yaml-grammar.jsonic` file

Grammar alts live in a declarative `.jsonic` file at the repo root and
are embedded into `src/yaml.ts` between `BEGIN/END` markers. Run
`make embed` (or `node embed-grammar.js`) to re-sync after editing.
All alts are tagged `g: yaml` so callers can use
`jsonic.options({rule:{exclude:'yaml'}})` to strip them.

### Trade-offs and limitations

- **Core subset**: the plugin covers the most common YAML constructs
  but not the full YAML 1.2 specification. In particular, complex
  mapping keys (non-scalar keys), set (`!!set`) and ordered map
  (`!!omap`) shorthand, and some corner cases of folding behavior are
  not yet handled.
- **Number parsing** follows YAML 1.1-ish behavior (`yes`/`no` are
  booleans). If you need strict YAML 1.2, treat those as strings in
  the source.
- **Security**: the plugin does not implement tag restrictions or a
  "safe" mode; do not use it as a generic deserializer for untrusted
  input without reviewing the result.


## Reference

### `Yaml` (Plugin)

A Tabnas plugin function. Register with `new Tabnas().use(jsonic).use(Yaml)`.

```typescript
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)
```

### `YamlOptions`

```typescript
type YamlOptions = {
  // Reserved for future options; currently empty.
}
```

No user-facing options are defined at this time.

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

### Exports

```typescript
export { Yaml }          // The plugin function
export type { YamlOptions }
```
