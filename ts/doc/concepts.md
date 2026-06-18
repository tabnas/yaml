# Concepts

Background on how the YAML plugin works, and why it is built the way it
is. This is understanding-oriented reading — for steps see the
[tutorial](tutorial.md) and [how-to guide](guide.md), and for exact
signatures see the [reference](reference.md).


## A grammar plugin on a relaxed-JSON grammar on an engine

There are three layers here:

- the **Tabnas engine** (`@tabnas/parser`) — a rule-based parser over a
  configurable, matcher-based lexer;
- the **relaxed-JSON grammar** (`@tabnas/jsonic`) — the rules that turn
  `a:1,b:2` into an object, installed on the engine;
- the **YAML plugin** (this package) — which amends that grammar and
  lexer to accept indentation-sensitive YAML.

This is why registration is `new Tabnas().use(jsonic).use(Yaml)`: each
`.use()` layers more grammar onto the same engine instance. The YAML
plugin is not a separate parser — it is a set of grammar amendments and
lexer matchers that ride on the engine jsonic already configured. The
payoff is that block YAML, flow collections, and relaxed JSON all share
one engine and one set of value rules.


## Two stages: a custom lexer, then amended rules

A parse runs in two cooperating stages, and the plugin extends both.

The **lexer** turns source text into tokens. It is built from
independent matchers that run in priority order; the first to produce a
token at each position wins. The plugin installs a `yaml` matcher at
priority `5e5` — ahead of jsonic's built-ins — so YAML-only syntax is
recognised first: block scalars (`|` / `>`), single- and double-quoted
scalars, anchors (`&`) and aliases (`*`), tags (`!!type`),
document-frame markers (`---` / `...` / `%…`), the explicit-key marker
(`?`), and — crucially — **indentation**, emitted as an `#IN` token
carrying the leading-space count of each line.

It also reconfigures the lexer: jsonic's single-colon fixed token is
removed and jsonic's string delimiters are cleared, because YAML's
colon-separation and quoting rules differ from JSON's and are handled
inside the plugin's own matcher.

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
more than the parent (open a nested block), equal (continue the
collection), or less (close back out)?" — encoded as alternate
*conditions* in the grammar (`@val-indent-deeper`, `@t0-eq-in`,
`@t0-le-in`, …). Sequences are likewise made explicit: a `- ` becomes an
`#EL` element-marker token, so `yamlBlockList` can recognise sequence
items without ever seeing a `[`.

This is the central design choice: rather than teach the deterministic
engine to be whitespace-aware, the plugin lifts whitespace into the
token stream where the existing rule machinery can reason about it with
ordinary two-token lookahead.


## The stream rule and multi-document shape

YAML files can hold multiple documents separated by `---`, optionally
closed by `...`, optionally preceded by `%YAML` / `%TAG` directives. The
plugin handles this with a `stream` rule that replaces `val` as the
start rule. `stream` consumes the document-frame tokens (`#DS`, `#DE`,
`#DR`), pushes a fresh `val` per document, and accumulates results.

The final shape is collapsed at the end: zero documents yields
`undefined`, one document yields its bare value, and two or more yield
an array. The `meta` option intercepts this last step and wraps the
result as `{ meta, content }`, where `meta` records, per document, the
directives seen, whether it opened explicitly (`---`), and whether it
closed explicitly (`...`).


## Per-parse state in closures

Anchors, pending anchors, tag-handle mappings, the incremental
flow-depth cache, and the stream accumulators are all *per-parse* state.
The plugin keeps them in closure variables captured by the lexer matcher
and the rule state-handlers (`bo`/`ao`/`bc`/`ac`), resetting them on the
first lex call of each parse (tracked by a `WeakSet` of seen `Lex`
objects). This is why the grammar's structural alternates live in a
declarative file but the stateful handlers stay in code: they need to
close over that mutable per-parse state.

Anchor resolution is two-phase because the lexer pre-fetches: an alias
(`*name`) may be lexed before the anchor it refers to is finalised by a
rule. The matcher resolves an alias immediately when the anchor is
already known, and otherwise emits a deferred `{ __yamlAlias }` marker
that the `val` rule's after-close handler resolves once the anchor table
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
tag restriction — review parsed output before trusting untrusted input.


## Why the grammar is a separate file

The structural grammar alternates live in a declarative `.jsonic` file
at the repo root ([`yaml-grammar.jsonic`](../../yaml-grammar.jsonic)) and
are embedded into `ts/src/yaml.ts` (and `go/yaml.go`) between
`BEGIN`/`END` markers by `embed-grammar.js`. One source of truth keeps
the TypeScript and Go ports in lockstep, and lets the grammar be
rendered directly as a railroad diagram. Every added alternate is tagged
with the rule group `yaml`, so the whole extension can be excluded
(`j.options({ rule: { exclude: 'yaml' } })`) to fall back to plain
relaxed-JSON parsing on the same instance.

For how the Go port differs, see
[../../go/doc/concepts.md](../../go/doc/concepts.md).
