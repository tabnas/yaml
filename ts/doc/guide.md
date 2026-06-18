# How-to guide

Focused recipes for the YAML plugin. Each is self-contained — copy the
block, change the input. For a guided introduction start with the
[tutorial](tutorial.md); for exact signatures and the full syntax list
see the [reference](reference.md).

Every recipe assumes a registered instance:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)
```


## Parse flow collections

Inline `{...}` mappings and `[...]` sequences work anywhere a value is
expected, and nest freely:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("data: {name: Bob, tags: [admin, ops]}")   // => { data: { name: 'Bob', tags: ['admin', 'ops'] } }
```


## Use block scalars (literal and folded)

`|` keeps newlines; `>` folds them into spaces. Both add a single
trailing newline by default:

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

Control the trailing newline with a chomping indicator: `-` strips it,
`+` keeps every trailing blank line. An explicit indent digit (`|2`,
`>-`) sets the content indent. So `|-` strips:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("a: |-\n  line1\n  line2")   // => { a: 'line1\nline2' }
```


## Reuse nodes with anchors and aliases

Mark a node with `&name`, reference it later with `*name`. The aliased
value is copied in:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("a: &items\n  - 1\n  - 2\nb: *items")   // => { a: [1, 2], b: [1, 2] }
```


## Merge mappings with `<<`

The `<<` merge key copies keys from an aliased mapping into the current
one. Keys already present locally win:

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

`prod` keeps its own `timeout: 60` and inherits `retries: 3` from
`base`.


## Coerce types with tags

`!!str`, `!!int`, `!!float`, `!!bool`, and `!!null` force a value's
type, overriding the plain-scalar inference:

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


## Parse non-decimal integers

Hex (`0x`), octal (`0o`), and binary (`0b`) integer literals resolve to
numbers:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)

j.parse("{mask: 0xff, perm: 0o755, flags: 0b1010}")   // => { mask: 255, perm: 493, flags: 10 }
```


## Handle multi-document streams

`---` starts a document; `...` ends one. One document parses to its
value; two or more parse to an array of values:

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


## Capture document metadata with `meta`

By default the plugin returns bare content. Pass `{ meta: true }` when
registering the plugin to get a `{ meta, content }` envelope instead.
`content` is exactly what the default path returns; `meta` records each
document's directives, whether it was explicitly opened with `---`
(`explicit`), and whether it was explicitly closed with `...`
(`ended`):

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml, { meta: true })

j.parse("a: 1")   // => { meta: { directives: [], explicit: false, ended: false }, content: { a: 1 } }
```

For a single document `meta` is one object; for a stream it is an array,
one entry per document, parallel to the `content` array:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml, { meta: true })

const out = j.parse(`%YAML 1.2
---
a: 1
---
b: 2`)

out.meta[0].directives   // => ['%YAML 1.2']
out.meta[1].directives   // => []
```

Directives apply only to the document that immediately follows them.


## Handle a parse error

When the input cannot be parsed, the engine throws. Catch it and read
the structured fields:

```js ignore
try {
  j.parse('a: 1\n  : oops')
} catch (err) {
  err.code          // a stable error code, e.g. 'unexpected'
  err.lineNumber    // 1-based line of the failure
  err.columnNumber  // 1-based column
  err.message       // formatted report with a source extract
}
```

The error type is the engine's `TabnasError` (re-exported by `jsonic`
under its historic name `JsonicError`). The `message` is a human
report; the `code`/`lineNumber`/`columnNumber` fields are for your code
to branch on.


## Turn the YAML grammar back off

Every rule and alternate the plugin adds is tagged `g: yaml`. To strip
them from an instance — reverting to plain relaxed-JSON parsing —
exclude that group:

```js ignore
const j = new Tabnas().use(jsonic).use(Yaml)
j.options({ rule: { exclude: 'yaml' } })
// j now parses relaxed JSON, without the YAML block-syntax extensions.
```
