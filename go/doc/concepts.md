# Concepts

Background on how the YAML plugin works, and how the Go port differs
from the canonical TypeScript one. This is understanding-oriented
reading — for steps see the [tutorial](tutorial.md) and
[how-to guide](guide.md), and for exact signatures see the
[reference](reference.md).


## A grammar plugin on a relaxed-JSON grammar on an engine

There are three layers here:

- the **Tabnas engine** (`github.com/tabnas/parser` in TypeScript;
  bundled into `github.com/tabnas/jsonic/go` for Go) — a rule-based
  parser over a configurable, matcher-based lexer;
- the **relaxed-JSON grammar** (`jsonic`) — the rules that turn
  `a:1,b:2` into a map, installed on the engine;
- the **YAML plugin** (this package) — which amends that grammar and
  lexer to accept indentation-sensitive YAML.

`MakeJsonic` builds a `*jsonic.Jsonic`, configures it for YAML, and
installs the `Yaml` plugin with `j.Use(Yaml, ...)`. The plugin is not a
separate parser — it is a set of grammar amendments and lexer matchers
that ride on the engine jsonic configured. The payoff is that block
YAML, flow collections, and relaxed JSON all share one engine and one
set of value rules.


## Two stages: a custom lexer, then amended rules

A parse runs in two cooperating stages, and the plugin extends both.

The **lexer** turns source text into tokens from independent matchers
that run in priority order; the first to produce a token at each
position wins. The plugin installs a `yaml` matcher at priority
`500000` — ahead of jsonic's built-ins — so YAML-only syntax is
recognised first: block scalars (`|` / `>`), single- and double-quoted
scalars, anchors (`&`) and aliases (`*`), tags (`!!type`),
document-frame markers (`---` / `...` / `%…`), the explicit-key marker
(`?`), and — crucially — **indentation**, emitted as an `#IN` token
carrying the leading-space count of each line.

The **parser** consumes those tokens by named rules. The plugin
*amends* jsonic's `val`, `map`, `pair`, `list`, and `elem` rules
(prepending alternates) and introduces block-specific rules — `indent`,
`yamlBlockList`, `yamlBlockElem`, `yamlElemMap`, `yamlElemPair` — plus a
`stream` rule that becomes the parser's start rule.


## Indentation as tokens, structure as rules

YAML's defining feature is that indentation is significant. The engine's
rule model has only two tokens of lookahead and no backtracking search,
so the plugin makes indentation *explicit* rather than implicit: the
lexer emits an `#IN` token (with the indent count as its value) at line
starts, and the block rules compare that count against the indent
recorded for the enclosing collection.

The decisions are small numeric comparisons — "is this line indented
more than the parent (open a nested block), equal (continue), or less
(close back out)?" — encoded as alternate *conditions* in the grammar.
Sequences are likewise made explicit: a `- ` becomes an `#EL`
element-marker token, so `yamlBlockList` recognises sequence items
without ever seeing a `[`. This is the central design choice: rather
than teach the deterministic engine to be whitespace-aware, the plugin
lifts whitespace into the token stream where the rule machinery can
reason about it with ordinary two-token lookahead.


## The stream rule and multi-document shape

YAML files can hold multiple documents separated by `---`, optionally
closed by `...`, optionally preceded by `%YAML` / `%TAG` directives. The
plugin handles this with a `stream` rule that replaces `val` as the
start rule. `stream` consumes the document-frame tokens (`#DS`, `#DE`,
`#DR`), pushes a fresh `val` per document, and accumulates results.

The final shape is collapsed at the end: zero documents yields `nil`,
one document yields its bare value, and two or more yield a `[]any`. The
`Meta` option intercepts this last step and wraps the result as a
`*MetaResult{Meta, Content}`, where `Meta` records, per document, the
directives seen, whether it opened explicitly (`---`), and whether it
closed explicitly (`...`).


## Per-parse state

Anchors, pending anchors, tag-handle mappings, the incremental
flow-depth cache (`flowScanState`), and the stream accumulators are all
*per-parse* state, reset on the first lex call of each parse. Anchor
resolution is two-phase because the lexer pre-fetches: an alias
(`*name`) may be lexed before the anchor it refers to is finalised. The
matcher resolves an alias immediately when the anchor is known, and
otherwise defers it for a rule handler to resolve once the anchor table
is complete. The `<<` merge key is applied in the `map` rule's
after-close handler, copying aliased keys in without overwriting keys
the mapping declares itself.


## Accepted vs rejected

The plugin targets a **core subset** of YAML 1.2 — the constructs that
appear in real configuration files — not the entire specification.

Accepted: block and flow collections, sequences of mappings, single-
and double-quoted scalars (with escape processing in double quotes and
`''`-escaping in single quotes), literal and folded block scalars with
chomping and explicit-indent indicators, anchors/aliases/merge keys,
`!!str`/`!!int`/`!!float`/`!!bool`/`!!null` tags, `%TAG` directives,
multi-document streams, comments, the broad YAML-1.1 keyword set
(`yes`/`no`/`on`/`off` as well as `true`/`false`/`null`/`~`), and
non-decimal integer literals (`0x`, `0o`, `0b`).

Not handled: non-scalar complex mapping keys, set (`!!set`) and ordered
map (`!!omap`) shorthand, and some folding corner cases. Keyword
handling is deliberately YAML-1.1-flavoured, so `yes`/`no` are booleans;
quote them when you need the literal strings. There is no "safe" mode or
tag restriction.


## Differences from the TS version

The TypeScript implementation is authoritative; the Go version is a
faithful port. The two produce identical *parse values* for the shared
fixtures. The differences are in API shape, host-language types, and
empty-input representation.

### API shape

| Area | TypeScript | Go |
| ---- | ---------- | -- |
| Entry point | register `Yaml` on an engine, then `j.parse(src)` | top-level `yaml.Parse(src) (any, error)`, or build with `MakeJsonic` |
| Plugin signature | `(tabnas, opts) => void` | `func(j *jsonic.Jsonic, opts map[string]any) error` |
| Options | `{ meta?: boolean }` passed to `.use(Yaml, opts)` | `YamlOptions{ Meta bool }` passed to `MakeJsonic` |
| Parse errors | thrown | returned as `error` (never panics) |
| Exclude the grammar | `j.options({ rule: { exclude: 'yaml' } })` | `j.SetOptions(jsonic.Options{Rule: &jsonic.RuleOptions{Exclude: "yaml"}})` |

There is no standalone `parse()` export in TypeScript — parsing always
goes through the engine after `.use()`. Go adds the convenience
`Parse`/`MakeJsonic` functions on top of the same `Yaml` plugin.

### Value types

| YAML            | TypeScript            | Go                |
| --------------- | --------------------- | ----------------- |
| mapping         | `object`              | `map[string]any`  |
| sequence        | `Array`               | `[]any`           |
| number          | `number`              | `float64`         |
| string          | `string`              | `string`          |
| `true`/`false`  | `boolean`             | `bool`            |
| `null` / `~`    | `null`                | `nil`             |
| `.inf` / `.nan` | `Infinity` / `NaN`    | `math.Inf(1)` / `math.NaN()` |

All Go numbers — including hex/octal/binary integers and `!!int` tags —
are `float64`; cast at the call site.

### The `meta` envelope

Both versions return a `{meta, content}` / `{Meta, Content}` envelope
when the option is set, with the same field meanings. The shapes differ
in the host idiom:

| | TypeScript (`meta: true`) | Go (`Meta: true`) |
| --- | --- | --- |
| Envelope | plain object `{ meta, content }` | `*MetaResult{Meta, Content any}` |
| Single-doc meta | one `DocMeta` object | `*DocMeta` (assert `mr.Meta.(*DocMeta)`) |
| Multi-doc meta | array of `DocMeta` | `[]*DocMeta` (assert `mr.Meta.([]*DocMeta)`) |

`DocMeta` has the same three fields in both: `directives` /
`Directives`, `explicit` / `Explicit`, `ended` / `Ended`.

### Empty input

Empty, whitespace-only, or comment-only source yields the host
language's "no value": `undefined` in TypeScript, `nil` in Go.
