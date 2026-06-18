# Reference

Dry and complete: the public API, every option, and the YAML syntax the
plugin accepts. For a guided start see the [tutorial](tutorial.md); for
task recipes see the [how-to guide](guide.md); for design background see
[concepts](concepts.md).


## Package

`@tabnas/yaml` — a Tabnas plugin. Peer dependencies: `@tabnas/jsonic`
(>= 2) and `@tabnas/parser` (>= 2).

```bash
npm install @tabnas/yaml @tabnas/jsonic @tabnas/parser
```


## Exports

```typescript
export { Yaml }            // the plugin function
export type { YamlOptions }
```

There is no standalone `parse()` export. Parsing happens through the
Tabnas engine, after the plugin is registered with `.use()`.


## `Yaml`

```typescript
const Yaml: Plugin
Yaml.defaults: YamlOptions   // { meta: false }
```

A Tabnas plugin function. Register it on an engine that already has the
`jsonic` grammar:

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml)
```

The plugin is idempotent: registering it twice on the same instance is a
no-op (it guards against re-entry). Order matters — `jsonic` must be
applied before `Yaml`, because `Yaml` amends the relaxed-JSON grammar
`jsonic` installs.

Once registered, parse with the engine's `parse` method:

```typescript
j.parse(source: string): any
```


## `YamlOptions`

Options passed as the second argument to `.use(Yaml, options)`:

```typescript
type YamlOptions = {
  meta?: boolean
}
```

| Option | Type      | Default | Effect |
| ------ | --------- | ------- | ------ |
| `meta` | `boolean` | `false` | When `false`, `parse` returns the bare document value (or an array of values for a multi-document stream). When `true`, `parse` returns a `{ meta, content }` envelope (see below). |

```js
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '@tabnas/yaml'

const j = new Tabnas().use(jsonic).use(Yaml, { meta: true })

j.parse("a: 1")   // => { meta: { directives: [], explicit: false, ended: false }, content: { a: 1 } }
```


## Return value

### With `meta: false` (default)

| Source                       | Returns                  |
| ---------------------------- | ------------------------ |
| empty / whitespace / comments-only | `undefined` |
| single document              | the document's value     |
| multi-document stream        | array of document values |

The document value follows these YAML-to-JavaScript mappings:

| YAML            | JavaScript                         |
| --------------- | ---------------------------------- |
| mapping         | `object`                           |
| sequence        | `Array`                            |
| number          | `number`                           |
| string          | `string`                           |
| `true` / `false`| `boolean`                          |
| `null` / `~`    | `null`                             |
| `.inf` / `.nan` | `Infinity` / `NaN`                 |

### With `meta: true`

`parse` returns `{ meta, content }`:

- `content` — exactly what the `meta: false` path returns.
- `meta` — for a single document, one `DocMeta`; for a stream, an array
  of `DocMeta` parallel to the `content` array.

```typescript
type DocMeta = {
  directives: string[]   // raw directive lines for this doc, e.g. ['%YAML 1.2']
  explicit: boolean      // true if the doc was opened with `---`
  ended: boolean         // true if the doc was closed with `...`
}
```


## Accepted syntax

| Feature                | Example                       | Result |
| ---------------------- | ----------------------------- | ------ |
| Block mapping          | `a: 1`                        | `{ a: 1 }` |
| Block sequence         | `- a`                         | `['a']` |
| Sequence of mappings   | `- name: alice`               | `[{ name: 'alice' }]` |
| Flow mapping           | `{a: 1, b: 2}`                | `{ a: 1, b: 2 }` |
| Flow sequence          | `[1, 2, 3]`                   | `[1, 2, 3]` |
| Double-quoted scalar   | `"line1\nline2"`              | `'line1\nline2'` (escapes processed) |
| Single-quoted scalar   | `'it''s'`                     | `"it's"` (`''` is a literal `'`) |
| Quoted-forced string   | `"42"`                        | `'42'` (stays a string) |
| Plain scalar           | `hello world`                 | `'hello world'` |
| Literal block scalar   | <code>&#124;</code>           | newlines preserved, one trailing `\n` |
| Folded block scalar    | `>`                           | newlines folded to spaces, one trailing `\n` |
| Chomping indicators    | <code>&#124;-</code>, `>+`    | strip / keep trailing newlines |
| Block-scalar indent    | <code>&#124;2</code>          | explicit content indent |
| Anchor / alias         | `&x 1`, `*x`                  | aliased value copied in |
| Merge key              | `<<: *x`                      | keys merged, local keys win |
| Type tag               | `!!int "42"`                  | `42` (coerced) |
| `%TAG` directive       | `%TAG !e! tag:example.com/`   | captured (see `meta.directives`) |
| Explicit flow key      | `{? k : v}`                   | `{ k: 'v' }` |
| Document start / end   | `---`, `...`                  | document boundaries |
| Value keywords         | `true`, `null`, `yes`, `~`    | `true`, `null`, `true`, `null` |
| Non-decimal integers   | `0xff`, `0o755`, `0b1010`     | `255`, `493`, `10` |
| Line comments          | `# comment`                   | ignored |

### Value keywords

These plain scalars resolve to non-string values (case variants like
`True`, `NULL`, `.Inf` are also accepted):

| Keyword(s)                          | Value        |
| ----------------------------------- | ------------ |
| `true` / `yes` / `on`               | `true`       |
| `false` / `no` / `off`              | `false`      |
| `null` / `~`                        | `null`       |
| `.inf` / `-.inf`                    | `±Infinity`  |
| `.nan`                              | `NaN`        |


## Tokens

The plugin adds YAML-specific lexer tokens, surfaced in the railroad
diagram legend (`ts/doc/grammar.svg`):

| Token | Meaning |
| ----- | ------- |
| `#IN` | line indentation (count of leading spaces) |
| `#EL` | block sequence item dash `- ` |
| `#DS` | document start marker `---` (column 0) |
| `#DE` | document end marker `...` (column 0) |
| `#DR` | directive line, e.g. `%YAML` / `%TAG` (column 0) |
| `#QM` | explicit-key marker `?` in flow `{? k : v }` |

It also removes jsonic's single-colon fixed token and clears jsonic's
string delimiters, because YAML colon-separation and quoting are handled
in the plugin's own lexer matcher.


## Grammar / rules

The plugin parses with a `stream` start rule (it replaces jsonic's
default `val` start). `stream` consumes the document-frame tokens
(`#DS` / `#DE` / `#DR`) and pushes a `val` rule per document. The block
rules it adds, alongside jsonic's `val` / `map` / `pair` / `list` /
`elem`, are:

| Rule | Role |
| ---- | ---- |
| `stream` | top-level document collector |
| `indent` | start of block content at a given indent |
| `yamlBlockList` | block sequence (`- ` items) |
| `yamlBlockElem` | subsequent items in a block sequence |
| `yamlElemMap` | a mapping that is a sequence element (`- key: val`) |
| `yamlElemPair` | additional pairs within a `yamlElemMap` |

Every alternate the plugin adds is tagged with the rule group `yaml`, so
the whole extension can be removed from an instance with
`j.options({ rule: { exclude: 'yaml' } })`, reverting to relaxed-JSON
parsing.

The grammar is authored once in the repo-root
[`yaml-grammar.jsonic`](../../yaml-grammar.jsonic) and embedded into
`ts/src/yaml.ts` (and `go/yaml.go`) by `embed-grammar.js`. The current
installed grammar as a railroad diagram is
[`ts/doc/grammar.svg`](grammar.svg), with an ASCII version in
[`ts/doc/grammar.txt`](grammar.txt).


## Limitations

- A core subset of YAML 1.2, not the full specification. Non-scalar
  complex keys, set (`!!set`) and ordered-map (`!!omap`) shorthand, and
  some folding corner cases are not handled.
- Keyword handling is YAML-1.1-flavoured: `yes`/`no`/`on`/`off` are
  booleans. Quote them if you need them as strings.
- No "safe" mode or tag restriction. Review parsed results before using
  them as a deserializer for untrusted input.
