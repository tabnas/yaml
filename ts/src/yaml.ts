/* Copyright (c) 2021-2025 Richard Rodger, MIT License */


// The engine is the tabnas parser; jsonic supplies the relaxed-JSON
// grammar that the embedded grammar text is authored in.
import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import {
  Rule,
  RuleSpec,
  Plugin,
  Config,
  TabnasOptions as Options,
  Context,
  Lex,
  Token,
} from '@tabnas/parser'


type YamlOptions = {
  // When true, parse() returns { meta, content } instead of bare content.
  // - meta is a per-document object {directives, explicit, ended} for single
  //   docs, or an array of such objects for multi-doc streams.
  // - content is the same value/array the no-meta path returns.
  meta?: boolean
}

type DocMeta = {
  directives: string[]
  explicit: boolean
  ended: boolean
}


// --- BEGIN EMBEDDED yaml-grammar.jsonic ---
const grammarText = `
# YAML Grammar Definition
# Parsed by a standard Tabnas instance and passed to tabnas.grammar()
# Function references (@ prefixed) are resolved against the refs map.
# State handlers (bo/ao/bc/ac) remain wired in code, since they use
# closures over per-parse state (anchors, pendingAnchors, etc.).

{
  # Amend val rule: YAML indent/element-marker handling.
  rule: val: open: {
    alts: [
      # Doc-frame markers between docs mean an empty value here; back up so
      # the stream rule consumes the marker and starts the next document.
      { s: '#DS' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DE' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DR' b: 1 a: '@val-set-null' g: yaml }
      # Indent followed by content: push indent rule.
      { s: '#IN' c: '@val-indent-deeper' p: indent a: '@val-set-in-from-o0' g: yaml }
      # Same indent followed by element marker: list value at map level.
      { s: ['#IN' '#EL'] c: '@val-indent-eq-parent' p: yamlBlockList a: '@val-set-in-from-o0' g: yaml }
      # End of input means empty value.
      { s: '#ZZ' b: 1 a: '@val-set-null' g: yaml }
      # Same or lesser indent after a colon means empty value — backtrack.
      { s: '#IN' b: 1 u: { yamlEmpty: true } g: yaml }
      # This value is a list.
      { s: '#EL' p: yamlBlockList a: '@val-set-el-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: val: close: {
    alts: [
      # Doc-frame markers terminate val; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Indent rule: start for block content at a given indent.
  rule: indent: open: [
    # Key pair => map.
    { s: ['#KEY' '#CL'] p: map b: 2 g: yaml }
    # Element marker => block sequence. yamlBlockList, not jsonic's list:
    # list reads a '[' as its own opening bracket, so a flow sequence as the
    # first item (k:\\n  - [1, 2]) became the block sequence itself, or
    # failed at the next item (tabnas/yaml#88).
    { s: '#EL' p: yamlBlockList g: yaml }
    # Flow collection as a block-mapping value on the FOLLOWING line:
    #     required:
    #       [a, b, c]
    # Valid YAML 1.2, and the inline spelling has always worked. The
    # indent rule opens at the deeper indent and meets a
    # flow opener, which only the val rule knows how to read, so hand it back
    # one token so val sees the bracket or brace itself.
    { s: '#OS' p: val b: 1 g: yaml }
    { s: '#OB' p: val b: 1 g: yaml }
    # Plain value after indent (for nested scalars).
    { s: '#KEY' a: '@indent-plain-value' g: yaml }
  ]

  # YAML block list: handles "- " sequences without consuming "[".
  rule: yamlBlockList: open: [
    # Element value is a key-value map: - key: val
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    # Default: push to val for the element's value.
    { p: val g: yaml }
  ]
  rule: yamlBlockList: close: [
    # Doc-frame markers terminate list; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    # Indent followed by element marker: next element at same level.
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: 'yaml,comma' }
    # Same or lesser indent: close list.
    { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    # Element marker at top level (no preceding newline).
    { s: '#EL' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#ZZ' b: 1 g: 'yaml,end' }
  ]

  # Subsequent elements in a yamlBlockList (via rotation).
  rule: yamlBlockElem: open: [
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    { p: val g: yaml }
  ]
  rule: yamlBlockElem: close: [
    # Doc-frame markers terminate elem; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    { s: '#EL' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#ZZ' b: 1 g: 'yaml,end' }
  ]

  # Amend list rule: close on dedent or same-indent non-element.
  rule: list: close: {
    alts: [
      # Doc-frame markers terminate list; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Amend map rule: same-indent indent continues map with pair.
  rule: map: open: {
    alts: [
      { s: '#IN' c: '@o0-eq-in' r: pair g: yaml }
    ]
    inject: { append: false }
  }
  rule: map: close: {
    alts: [
      # Doc-frame markers terminate map; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Amend pair rule: end of input ends pair; dedent closes, same-indent repeats.
  # Also handle YAML flow-mapping shapes Tabnas doesn't have natively:
  # - implicit null values: {a, b: c}  — KEY followed directly by CA or CB
  # - explicit-key marker:  {? k : v}  — leading #QM is consumed
  rule: pair: open: {
    alts: [
      { s: ['#KEY' '#CA'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#KEY' '#CB'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: val u: { pair: true } a: '@qm-pairkey' g: yaml }
      { s: ['#QM' '#KEY' '#CA'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CB'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: '#ZZ' b: 1 g: 'yaml,end' }
    ]
    inject: { append: false }
  }
  rule: pair: close: {
    alts: [
      # Doc-frame markers terminate pair; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-eq-in' r: pair g: 'yaml,comma' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # yamlElemMap: a mapping that starts in a sequence entry, "- key: val",
  # or "[key: val]" in a flow sequence. It only opens the mapping (its
  # before-open action makes the node) and hands the first pair, unread,
  # to yamlElemPair, which reads every pair. A pair rule opening on the
  # open mapping names the member in u.key before it pushes the value's
  # rule, so a consumer that follows rule events has the key before a
  # value that is a collection opens. When the rule that opened the
  # mapping named the first member as well, that consumer was never told
  # the key, and the first member's value opened ahead of it
  # (tabnas/yaml#105).
  rule: yamlElemMap: open: [
    { s: ['#KEY' '#CL'] r: yamlElemPair b: 2 g: yaml }
  ]

  # Every pair in a yamlElemMap, the first included.
  rule: yamlElemPair: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemPair: close: [
    # Doc-frame markers terminate elem-pair; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: 'yaml,comma' }
    { s: '#IN' b: 1 g: 'yaml,close' }
    { s: '#CA' b: 1 g: 'yaml,comma' }
    { s: '#CS' b: 1 g: 'yaml,close' }
    { s: '#CB' b: 1 g: 'yaml,close' }
    { s: '#ZZ' g: 'yaml,end' }
  ]

  # Amend elem rule for YAML sequences ("- key: val" at top level of [ ... ]).
  # Also handle flow-sequence explicit-key entries: [? k : v] is a single-pair
  # map element. Eat the leading #QM, then back up KEY+CL so yamlElemMap
  # consumes them as a normal pair.
  rule: elem: open: {
    alts: [
      { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: elem: close: {
    alts: [
      # Doc-frame markers terminate elem; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: ['#IN' '#EL'] c: '@t0-eq-in' r: elem g: 'yaml,comma' }
      { s: '#IN' c: '@t0-eq-in' b: 1 g: 'yaml,close' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
      { s: '#EL' r: elem g: 'yaml,comma' }
    ]
    inject: { append: false }
  }
}
`
// --- END EMBEDDED yaml-grammar.jsonic ---


// The characters updateFlowState acts on, by char code: quotes, the escape
// backslash, the comment mark, flow brackets, and block scalar indicators.
// Every other character leaves its state unchanged, so it is skipped with
// one table read.
const FLOW_SCAN_CHARS = new Uint8Array(126)
for (const c of '"\'\\#{}[]|>') FLOW_SCAN_CHARS[c.charCodeAt(0)] = 1

// The tags whose value is the structure that follows: !!seq, !!map, and
// the rest.
const STRUCT_TAG = /^!!(seq|map|omap|set|pairs|binary|ordered|python\/[^\s]*)\b/

// How much of the text from the cursor STRUCT_TAG reads, `F(i)` being its
// i-th character: past the `!!`, the run of characters that are not white
// space, and the one that ends it, where its `\b` is decided.
function structTagReach(F: (i: number) => string): number {
  let k = 2
  while (F(k) !== undefined && !/\s/.test(F(k))) k++
  return F(k) === undefined ? k : k + 1
}


// The value a `!!int` tag gives its text: YAML's core-schema hex (0x1f)
// and octal (0o17) forms, then a decimal parse. A text no form reads
// stays NaN, as before: the tag applies even when its value does not.
function yamlTagInt(raw: string): number {
  let m = raw.match(/^([-+]?)0x([0-9a-fA-F]+)$/) || raw.match(/^([-+]?)0o([0-7]+)$/)
  if (m) {
    let n = parseInt(m[2], raw.includes('0x') ? 16 : 8)
    return '-' === m[1] ? -n : n
  }
  return parseInt(raw, 10)
}

// The value a `!!float` tag gives its text: the core schema's infinities
// and not-a-number (.inf, -.inf, +.inf, .nan in three casings), then a
// decimal parse.
function yamlTagFloat(raw: string): number {
  let m = raw.match(/^([-+]?)\.(inf|Inf|INF)$/)
  if (m) return '-' === m[1] ? -Infinity : Infinity
  if (/^\.(nan|NaN|NAN)$/.test(raw)) return NaN
  return parseFloat(raw)
}


// A member this grammar stores itself, outside the engine's pair rule: a
// key with no value in a flow mapping, a sequence entry's pair, a merge
// key's members. A key the mapping holds already is combined as the pair
// rule combines one (jsonic's `pairval`): through the grammar's
// `map.merge` when it has one, else merged deep under `map.extend`, the
// default, else replaced by the later value; an earlier null is replaced.
// So `- a: {b: 1}` and `a: {c: 2}` in one sequence entry merge as they
// do at the root. With `map.ordered` on, the engine records the members
// its own rule stores in a key-order side channel, which a reader that
// walks a mapping in source order follows (`keyOrder`; alchemy-cli's walk
// of a value); a member stored here is recorded the same way, so that it
// walks where the document put it, and not after every recorded one. The
// record is the engine's `recordKeyOrder`, which @tabnas/parser exports
// after 0.12.12; its text is here until the floor moves.
// `pruneHold`, in the parse context's bag: how many anchored values are
// open. A host that streams a document through transduce's
// ParserEventSource empties each array behind the stream once its rule's
// close pass has gone by, and an anchor is recorded only when its value
// closes, after the arrays inside it have closed, so the record would hold
// them emptied: an alias used as a key would then name the emptied text
// (tabnas/yaml#138). While the count is above zero, a source that honours
// the hold empties no array. The key is transduce's, the same in every
// runtime.
const PRUNE_HOLD = 'pruneHold'

// Raise or lower `pruneHold` by `by`, one open anchored value at a time.
function holdPruning(ctx: Context, by: number): void {
  const now = 'number' === typeof ctx.u[PRUNE_HOLD] ? ctx.u[PRUNE_HOLD] : 0
  ctx.u[PRUNE_HOLD] = Math.max(0, now + by)
}

// Name, in the val's counters, each anchor it claims that its close may
// record as its node: the counter `&name`, whose value is the val's own
// instance number, and the counter `yamlAnchored`, of the same value,
// which marks the val. A counter passes to every rule below the val, with
// the val's number, so a host that reads the rule passes (YAML's
// transducer part) knows the anchored val as the one whose `yamlAnchored`
// is its own `i`, with one lookup on each val's pass, keeps the events of
// its value, and writes them again at an alias of a name. An inline anchor
// on a val that opened on a scalar records that scalar or nothing (the
// val's ac), so it is not named.
function nameAnchors(rule: Rule): void {
  const open = rule.u.yamlAnchorOpenNode
  const scalar = null != open && 'object' !== typeof open
  for (const anchor of rule.u.yamlAnchors) {
    if (anchor.inline && scalar) continue
    rule.n['&' + anchor.name] = rule.i
    rule.n.yamlAnchored = rule.i
  }
}

const KEY_ORDER = Symbol.for('tabnas.keyOrder')

function storeMember(rule: Rule, ctx: Context, key: string, value: any) {
  const node = rule.node
  const prev = node[key]
  if (null != prev) {
    const map = ctx.cfg.map
    value = map && map.merge
      ? map.merge(prev, value, rule, ctx)
      : !map || map.extend
        ? Tabnas.util.deep(prev, value)
        : value
  }
  if (ctx.cfg.map && ctx.cfg.map.ordered && !(key in node)) {
    let order: any[] = node[KEY_ORDER]
    if (undefined === order) {
      order = []
      Object.defineProperty(node, KEY_ORDER, {
        value: order,
        enumerable: false,
        writable: true,
        configurable: true,
      })
    }
    order.push('' + key)
  }
  node[key] = value
}

// A copy of an anchored value, as an alias presents it. An alias is the
// anchored node again (YAML 1.2.2, 3.2.2.2), so every scalar is kept as it
// is, a number that is not finite included, where the JSON round trip this
// copy replaces wrote one as null; and every container is rebuilt, so a
// merge into one alias cannot reach the anchor or another alias. A
// mapping's keys are defined as its own properties, so a key `__proto__`
// stays a key, as JSON.parse kept it.
function copyAnchored(val: any): any {
  if (Array.isArray(val)) return val.map(copyAnchored)
  if (null == val || 'object' !== typeof val) return val
  const out: Record<string, any> = {}
  for (const key of Object.keys(val)) {
    Object.defineProperty(out, key, {
      value: copyAnchored(val[key]),
      writable: true,
      enumerable: true,
      configurable: true,
    })
  }
  return out
}

// A member a merge key brings, stored, and named in the mapping's counters
// as the counter `<<key`, whose value is the number of the source it came
// from: 1 for the mapping a `<<` names, and for a sequence, the place of
// the mapping among the sequence's mappings, counted from 1. So the map's
// close pass names every key it merged and where it took it: a host that
// reads the rule passes (YAML's transducer part) writes those members of
// those sources, and no other, as the mapping's last.
function mergeMember(rule: Rule, ctx: Context, key: string, value: any, source: number) {
  storeMember(rule, ctx, key, value)
  rule.n['<<' + key] = source
}

// A member this grammar removes (the `<<` of a merge), taken out of the
// record too, so `keyOrder` lists the members the mapping has.
function deleteMember(node: any, key: string) {
  delete node[key]
  const order: any[] | undefined = node[KEY_ORDER]
  if (undefined !== order) {
    const at = order.indexOf(key)
    if (-1 !== at) order.splice(at, 1)
  }
}

const Yaml: Plugin = (tabnas: Tabnas, options: YamlOptions) => {
  // Install once per instance: a second use(Yaml) on the same instance
  // returns here. options() does not re-run plugins, and make() builds a
  // fresh child that installs its own.
  if ((tabnas as any).__yamlInstalled) return
  ;(tabnas as any).__yamlInstalled = true

  // Human descriptions for YAML tokens, surfaced in railroad diagram legends
  // (read off the live config by @tabnas/railroad).
  tabnas.options({
    config: {
      modify: {
        'yaml-tokendesc': (cfg: any) => {
          cfg.tokenDesc = Object.assign(cfg.tokenDesc || {}, {
            '#DS': 'document start marker --- (column 0)',
            '#DE': 'document end marker ... (column 0)',
            '#DR': 'directive line, e.g. %YAML / %TAG (column 0)',
            '#EL': 'block sequence item dash "- "',
            '#IN': 'line indentation (count of leading spaces)',
            '#QM': 'explicit-key marker ? in flow {? k : v }',
          })
        },
      },
    },
  })

  // This plugin's own error codes, with the templates and hints the engine
  // fills from the bad token. Each is declared in every runtime and listed
  // in tabnas.plugin.json.
  tabnas.options({
    error: {
      yaml_undefined_alias: 'undefined alias: {src}',
    },
    hint: {
      yaml_undefined_alias: `
The alias {src} names no anchor that occurs before it in the document.
An anchor is written &name on the node it names, and an alias can only
name an anchor defined earlier.`,
    },
  })

  let TX = tabnas.token.TX
  let NR = tabnas.token.NR
  let ST = tabnas.token.ST
  let VL = tabnas.token.VL
  let CL = tabnas.token.CL
  let ZZ = tabnas.token.ZZ

  let IN = tabnas.token('#IN')

  // Shared anchor storage for the plugin instance.
  // Allocated without a prototype: anchor names come from the document, so on
  // a plain literal `anchors['__proto__'] = value` never defines a key - it
  // runs the Object.prototype setter. A map value then reparents this table
  // and every later alias resolves through attacker-supplied data (*toString
  // would find a value that was never anchored), while a scalar value is
  // silently dropped and *__proto__ reads back Object.prototype.
  let anchors: Record<string, any> = Object.create(null)
  let pendingAnchors: { name: string, inline: boolean }[] = []
  // Every anchor name the lexer has passed, so an alias can be checked
  // against YAML's rule that its anchor occurs before it. The anchors table
  // cannot answer that: a value is recorded only when its node closes, so
  // `&a [*a]` names an anchor that occurs before it but is not recorded yet.
  let declaredAnchors: Set<string> = new Set()
  const pendAnchor = (name: string, inline: boolean) => {
    pendingAnchors.push({ name, inline })
    if (!declaredAnchors.has(name)) {
      declaredAnchors.add(name)
      if (windowed) undoLog.push([0, name, false, undefined])
    }
  }
  // An anchor the lexer records itself (an inline scalar's, a tagged
  // value's), noted so that a cut made again can take it back.
  const setAnchor = (name: string, val: any) => {
    if (windowed) undoLog.push([1, name, name in anchors, anchors[name]])
    anchors[name] = val
  }
  let pendingExplicitCL = false
  // Flag to tell the number matcher to skip, so text.check handles the value.
  let skipNumberMatch = false
  // TAG directive handle mappings (e.g. %TAG !! tag:example.com/).
  // When !! is redefined, built-in type conversion is skipped.
  let tagHandles: Record<string, string> = {}
  // Per-parse accumulators for the stream rule. Reset on first lex call.
  let yamlStreamDocs: any[] = []
  let yamlStreamMeta: DocMeta[] = []
  let yamlStreamCurMeta: DocMeta | null = null
  // Incremental flow-depth cache for text.check (avoids O(n²) rescan).
  // Positions are the source's, so they hold across a window's moves.
  let _flowDepth = 0
  let _flowScanPos = 0
  let _flowScanUpTo = 0
  // Persistent quote state so multi-call scans handle quotes spanning slices.
  let _inSingleQuote = false
  let _inDoubleQuote = false

  // ---- Reading the source ----
  //
  // The matcher and text.check read the source by position. In a parse of
  // a whole text `lex.src` is that text. In a parse from a reader a window
  // at a time (`lex.window`, admin ADR-30), `lex.src` is the window: the
  // source from the oldest token the parser holds to as far as it has been
  // read, and while a token is cut the cursor counts from the window's
  // start. Every read here goes through `srcAt`, which reads the source on
  // (`lex.fill`) past the window's end, so a scan sees what the whole text
  // holds, and reads `hist`, this plugin's own copy of the lines before the
  // cursor, behind the window's start: the backward scans read the line a
  // token starts on and the two before it, which the window may have
  // dropped. Positions kept from one token to the next (the flow scan, the
  // line breaks) are the source's, `viewBase` + a window position.
  let view: Lex = null as any
  let viewBase = 0
  // Whether the lexer reads a window of a source read from a reader. A
  // whole text needs neither `hist` nor the cuts' state kept, since the
  // engine cuts a token of it once.
  let windowed = false
  // The source position `histSync` has brought `hist`, `breaks` and the
  // flow colon's question up to.
  let syncEnd = 0
  // The source behind the window, from a reader: positions [histLow,
  // syncEnd), histLen of them.
  let hist = new Uint16Array(256)
  let histLow = 0
  let histLen = 0
  // The source positions of the line breaks `histSync` has passed, the
  // last few kept: `hist` keeps the text after the HIST_BREAKS-th last of
  // them.
  let breaks: number[] = []
  // The flow colon's question, kept as the text goes by: the last
  // character before the cursor that is neither blank nor in a comment
  // (`jSig`, a char code, -1 for none). A comment is a `#` that starts its
  // line or follows a blank, outside a quoted scalar opened on that line;
  // `jQuote` is the quote open, `jSkip` that the next character is escaped
  // or the second of a doubled single quote, `jComment` that the rest of
  // the line is a comment, `jPrev` the character before, -1 at a line's
  // start.
  let jSig = -1
  let jQuote = 0
  let jSkip = false
  let jComment = false
  let jPrev = -1
  // The cut the matcher last began: its lexer, its source position, and how
  // many tokens the parser had taken by then. The engine cuts a token again
  // over a wider window when a read reached the window's end, from the same
  // place, before the parser takes another: such a cut starts from the
  // state the first began with (`snapshot`, `restore`).
  let cutLex: Lex | null = null
  let cutPos = -1
  let cutTaken = -1
  // What a cut changed of the anchors it is undone by, newest last:
  // [0, name] an anchor declared, [1, name, had, old] an anchor recorded.
  let undoLog: [number, string, boolean, any][] = []
  // The state a cut began with.
  let s_pending = 0
  let s_explicitCL = false
  let s_skipNumber = false
  let s_flow = [0, 0, 0, 0, 0]
  let s_syncEnd = 0
  let s_histLen = 0
  let s_breaks = 0
  let s_j = [0, 0, 0, 0, 0]

  // Whether a read past the window's end reads the source on. The matcher
  // runs first in a cut, and reads on. The text check runs after the
  // engine's own matchers, and a read of its past the window's end notes
  // it instead (`lex.touchEnd`), so that the engine cuts the token again
  // over a wider window: those matchers read only the window, and a number
  // or a comment cut short at its end, which they leave for a later
  // matcher, would otherwise be read on as text, and the window then end
  // where the source does, the engine taking the cut as it stands.
  let readOn = true

  // The character at `x`, a window position (lex.src[x] as the whole text
  // gives it), undefined past the source's end and before its start. Typed
  // as a string, as `lex.src[x]` is.
  function srcAt(x: number): string {
    const s = view.src as string
    if (x < s.length) {
      return 0 <= x ? s[x] : histAt(viewBase + x)
    }
    if (!readOn) {
      view.touchEnd()
      return undefined as any
    }
    view.fill(x - view.pnt.sI + 1)
    const t = view.src as string
    return x < t.length ? t[x] : (undefined as any)
  }

  // The text from window position `x` to `y`, as `substring` takes it:
  // the source read on to the further of the two first.
  function srcSub(x: number, y: number): string {
    const far = x < y ? y : x
    if (0 < far) srcAt(far - 1)
    return (view.src as string).substring(x, y)
  }

  // The character at source position `a`.
  function absAt(a: number): string {
    return srcAt(a - viewBase)
  }

  // The char code at source position `a`, -1 outside the source.
  function absCode(a: number): number {
    const c = srcAt(a - viewBase)
    return undefined === c ? -1 : c.charCodeAt(0)
  }

  // The character at source position `a`, behind the window's start: from
  // `hist`. The scans never read behind the lines `hist` keeps, so a read
  // that would is a fault of this plugin's, and fails the parse loudly
  // rather than read a different text.
  function histAt(a: number): string {
    if (a < 0) return undefined as any
    const i = a - histLow
    if (0 <= i && i < histLen) return String.fromCharCode(hist[i])
    throw new Error('yaml: source position ' + a + ' lies behind the text kept (' +
      histLow + ' to ' + (histLow + histLen) + ')')
  }

  // Read the lexer `lex` through the window: its window starts at source
  // position `viewBase`.
  function viewOf(lex: Lex): void {
    view = lex
    viewBase = lex.lengthRead() - (lex.src as string).length
  }

  // Copy the source up to position `to`, which the window holds, onto the
  // end of `hist`, noting each line break and the flow colon's character
  // as it goes.
  function histSync(to: number): void {
    let a = syncEnd
    if (to <= a) return
    if (a < viewBase) {
      throw new Error('yaml: source position ' + a + ' was dropped before it was kept')
    }
    if (windowed && hist.length < histLen + (to - a)) {
      let size = hist.length
      while (size < histLen + (to - a)) size *= 2
      const grown = new Uint16Array(size)
      grown.set(hist.subarray(0, histLen))
      hist = grown
    }
    // The window holds the text up to `to`, which the cursor has reached.
    srcAt(to - 1 - viewBase)
    const src = view.src as string
    syncEnd = to
    for (; a < to; a++) {
      const c = src.charCodeAt(a - viewBase)
      if (windowed) hist[histLen++] = c
      if (10 === c || 13 === c) {
        breaks.push(a)
        // Line breaks are lines' ends in every scan here, and neither is
        // blank or in a comment to the flow colon.
        jQuote = 0
        jSkip = false
        jComment = false
        jPrev = -1
        continue
      }
      if (jComment) continue
      const blank = 32 === c || 9 === c
      if (jSkip) {
        jSkip = false
      } else if (0 !== jQuote) {
        if (34 === jQuote && 92 === c) {
          jSkip = true
        } else if (c === jQuote) {
          if (39 === jQuote && 39 === absCode(a + 1)) jSkip = true
          else jQuote = 0
        }
      } else if (34 === c || 39 === c) {
        jQuote = c
      } else if (35 === c && (-1 === jPrev || 32 === jPrev || 9 === jPrev)) {
        jComment = true
        continue
      }
      if (!blank) jSig = c
      jPrev = c
    }
  }

  // The lines `hist` keeps: the text after the HIST_BREAKS-th last line
  // break. A backward scan reads the line a token starts on and at most
  // the two before it, each ended by one or two breaks (`\r\n`). The text
  // before is dropped once it is as long as what is kept, so that the copy
  // costs a constant per character.
  const HIST_BREAKS = 8
  function histTrim(): void {
    if (breaks.length < HIST_BREAKS) return
    if (2 * HIST_BREAKS < breaks.length) {
      breaks.splice(0, breaks.length - HIST_BREAKS)
    }
    if (!windowed) return
    const keep = breaks[breaks.length - HIST_BREAKS] + 1
    const dead = keep - histLow
    if (dead <= 0 || dead < histLen - dead) return
    hist.copyWithin(0, dead, histLen)
    histLen -= dead
    histLow = keep
  }

  // The source position of the start of the line that holds source
  // position `a`: the source's start, or the position after a line break
  // (`\n` or `\r`), as a scan back from `a` would find it. The breaks
  // `histSync` has passed answer it at once, where a scan back over a long
  // line reads each of its characters; past them, a scan back to them; and
  // before the oldest of them kept, the scan.
  function lineStartOf(a: number): number {
    if (a > syncEnd) {
      for (let k = a; k > syncEnd; k--) {
        const c = absCode(k - 1)
        if (10 === c || 13 === c) return k
      }
      a = syncEnd
    }
    let lo = 0
    let hi = breaks.length
    while (lo < hi) {
      const mid = (lo + hi) >>> 1
      if (breaks[mid] < a) lo = mid + 1
      else hi = mid
    }
    if (0 < lo) return breaks[lo - 1] + 1
    // No break before the position `histSync` has reached: a list trimmed
    // keeps HIST_BREAKS of them.
    if (0 === breaks.length) return 0
    while (a > 0 && absAt(a - 1) !== '\n' && absAt(a - 1) !== '\r') a--
    return a
  }

  // How many tokens the parser has taken from the lexer, ignored ones
  // aside: those it has consumed, and the lookahead it holds.
  function taken(ctx: Context): number {
    let n = ctx.vAbs
    const t = ctx.t
    const NO = ctx.NOTOKEN
    for (let i = 0; i < t.length; i++) {
      if (null != t[i] && NO !== t[i]) n++
    }
    return n
  }

  // Begin the matcher's call: read the lexer through its window, and either
  // begin a cut, bringing `hist` and the flow scan up to the cursor and
  // keeping the state the cut begins with, or, where the engine makes the
  // last cut again (the same place, and no token taken since), begin it
  // from that state again.
  function beginCut(lex: Lex): void {
    viewOf(lex)
    const pos = viewBase + lex.pnt.sI
    if (windowed) {
      const took = taken(lex.ctx)
      if (lex === cutLex && pos === cutPos && took === cutTaken) {
        restore()
        return
      }
      cutLex = lex
      cutPos = pos
      cutTaken = took
    }
    histSync(pos)
    updateFlowState(pos)
    histTrim()
    if (windowed) snapshot()
  }

  function snapshot(): void {
    undoLog.length = 0
    s_pending = pendingAnchors.length
    s_explicitCL = pendingExplicitCL
    s_skipNumber = skipNumberMatch
    s_flow[0] = _flowDepth
    s_flow[1] = _flowScanPos
    s_flow[2] = _flowScanUpTo
    s_flow[3] = _inSingleQuote ? 1 : 0
    s_flow[4] = _inDoubleQuote ? 1 : 0
    s_syncEnd = syncEnd
    s_histLen = histLen
    s_breaks = breaks.length
    s_j[0] = jSig
    s_j[1] = jQuote
    s_j[2] = jSkip ? 1 : 0
    s_j[3] = jComment ? 1 : 0
    s_j[4] = jPrev
  }

  function restore(): void {
    for (let i = undoLog.length - 1; 0 <= i; i--) {
      const [kind, name, had, old] = undoLog[i]
      if (0 === kind) declaredAnchors.delete(name)
      else if (had) anchors[name] = old
      else delete anchors[name]
    }
    undoLog.length = 0
    pendingAnchors.length = s_pending
    pendingExplicitCL = s_explicitCL
    skipNumberMatch = s_skipNumber
    _flowDepth = s_flow[0]
    _flowScanPos = s_flow[1]
    _flowScanUpTo = s_flow[2]
    _inSingleQuote = 1 === s_flow[3]
    _inDoubleQuote = 1 === s_flow[4]
    syncEnd = s_syncEnd
    histLen = s_histLen
    breaks.length = s_breaks
    jSig = s_j[0]
    jQuote = s_j[1]
    jSkip = 1 === s_j[2]
    jComment = 1 === s_j[3]
    jPrev = s_j[4]
  }

  // A parse begins, over the lexer `lex`: nothing kept of the last.
  function resetView(lex: Lex): void {
    windowed = lex.windowed
    syncEnd = 0
    histLow = 0
    histLen = 0
    breaks = []
    jSig = -1
    jQuote = 0
    jSkip = false
    jComment = false
    jPrev = -1
    cutLex = null
    cutPos = -1
    cutTaken = -1
    undoLog.length = 0
  }

  // Whether the source holds nothing but white space and comment lines, as
  // `src.replace(/^[ \t]*#[^\n]*(\n|$)/gm, '').trim() === ''` asks of the
  // whole text. It reads from the source's start and stops at the first
  // character that is neither, so only the leading space and comments are
  // read. A line starts after a line terminator, as `^` takes one under the
  // `m` flag, and white space is what `trim` removes.
  function sourceBlank(): boolean {
    let a = 0
    let lineStart = true
    for (;;) {
      if (lineStart) {
        let q = a
        while (' ' === absAt(q) || '\t' === absAt(q)) q++
        if ('#' === absAt(q)) {
          while (undefined !== absAt(q) && '\n' !== absAt(q)) q++
          if (undefined === absAt(q)) return true
          a = q + 1
          continue
        }
      }
      const c = absAt(a)
      if (undefined === c) return true
      if (!/\s/.test(c)) return false
      a++
      lineStart = '\n' === c || '\r' === c || ' ' === c || ' ' === c
    }
  }

  // Bring _flowDepth up to date with `upTo`, a source position, by scanning
  // the source incrementally.
  //
  // The scan has to read the source the way the lexer does, or text is
  // counted as syntax. A quoted region is skipped, and so are the regions
  // where a bracket or a quote is ordinary text (tabnas/yaml#89):
  //   - a comment (`#` at the start of a line or after whitespace);
  //   - a block scalar's content lines (`|` / `>`);
  //   - in block context, any `{` `[` `"` `'` that does not begin a node:
  //     the `{` in `f({` or the `"` in `say "hi` is inside a plain scalar.
  // Counting those left the depth above zero, so the rest of the document
  // was read as flow (a `{` failed the parse), or left a quote open, so a
  // later `[1, 2]` was read as the one string "1, 2]".
  // The scan may run past `upTo` (to the end of a comment or a block
  // scalar), since the lexer never stops inside either. Each call goes on
  // from where the last stopped, so calling it at every cut, as
  // `beginCut` does, reads each character once, as one call would.
  function updateFlowState(upTo: number) {
    if (upTo < _flowScanUpTo) {
      _flowDepth = 0; _flowScanPos = 0
      _inSingleQuote = false; _inDoubleQuote = false
    }
    _flowScanUpTo = upTo
    let fi = _flowScanPos
    for (; fi < upTo; fi++) {
      // Fast path: only these ten characters matter in any state. The
      // window's text read at the call stays a prefix of the window's
      // after any read past its end.
      const s = view.src as string
      const x = fi - viewBase
      let cc = 0 <= x && x < s.length ? s.charCodeAt(x) : absCode(fi)
      if (cc > 125 || 0 === FLOW_SCAN_CHARS[cc]) continue
      let fc = absAt(fi)
      if (_inDoubleQuote) {
        if (fc === '\\') fi++
        else if (fc === '"') _inDoubleQuote = false
        continue
      }
      if (_inSingleQuote) {
        if (fc === "'") {
          if (absAt(fi + 1) === "'") fi++
          else _inSingleQuote = false
        }
        continue
      }
      if (fc === '#' && (fi === 0 || isYamlSpace(absAt(fi - 1)))) {
        while (absAt(fi + 1) !== undefined && absAt(fi + 1) !== '\n' &&
               absAt(fi + 1) !== '\r') fi++
        continue
      }
      if (_flowDepth > 0) {
        if (fc === '{' || fc === '[') _flowDepth++
        else if (fc === '}' || fc === ']') _flowDepth--
        else if (fc === '"') _inDoubleQuote = true
        else if (fc === "'") {
          let pc = fi > 0 ? absCode(fi - 1) : 0
          if (!((pc >= 65 && pc <= 90) || (pc >= 97 && pc <= 122) || (pc >= 48 && pc <= 57))) {
            _inSingleQuote = true
          }
        }
        continue
      }
      // Block context: only a node's first character opens anything.
      if (fc === '{' || fc === '[' || fc === '"' || fc === "'" ||
          fc === '|' || fc === '>') {
        if (!startsYamlNode(fi)) {
          // Inside a plain scalar, which nothing can open until it ends at
          // `: `, ` #` or the end of the line: skip to there.
          fi = plainScalarRest(fi) - 1
        }
        else if (fc === '{' || fc === '[') _flowDepth++
        else if (fc === '"') _inDoubleQuote = true
        else if (fc === "'") _inSingleQuote = true
        else {
          let end = blockScalarEnd(fi)
          if (end > fi) fi = end - 1
        }
      }
    }
    _flowScanPos = fi
  }

  // Whether a quoted scalar opens at source position `i` and is a block
  // mapping key: its closing quote is followed, on the same line, by `:` and
  // then whitespace or the end of the line.
  function quotedKeyAt(i: number): boolean {
    let q = absAt(i)
    let j = i + 1
    while (absAt(j) !== undefined && absAt(j) !== '\n' && absAt(j) !== '\r') {
      if (q === '"' && absAt(j) === '\\') { j += 2; continue }
      if (absAt(j) === q) {
        if (q === "'" && absAt(j + 1) === "'") { j += 2; continue }
        break
      }
      j++
    }
    if (absAt(j) !== q) return false
    j++
    while (absAt(j) === ' ' || absAt(j) === '\t') j++
    return absAt(j) === ':' && (absAt(j + 1) === undefined || isYamlSpace(absAt(j + 1)))
  }

  // The single-character escapes of a double-quoted scalar, YAML 1.2's
  // c-ns-esc-char less `x`, `u` and `U`, which take hexadecimal digits.
  const DQ_ESCAPES: Record<string, string> = {
    '0': '\0', a: '\x07', b: '\b', t: '\t', '\t': '\t', n: '\n',
    v: '\v', f: '\f', r: '\r', e: '\x1b', ' ': ' ', '"': '"', '/': '/',
    '\\': '\\', N: '\u0085', _: '\u00a0', L: '\u2028', P: '\u2029',
  }

  // `text` with its quotes removed when the whole of it is one quoted
  // scalar on one line; otherwise `text` unchanged. Single quotes take ''
  // for a quote. Double quotes take every escape YAML 1.2 defines: the
  // single characters of DQ_ESCAPES, and `\x`, `\u` and `\U` with exactly
  // two, four and eight hexadecimal digits, a `\u` high surrogate
  // followed by a `\u` low one read as the one character they name. A
  // malformed or unknown escape, a lone surrogate or a quote inside leaves
  // the text as it is.
  function unquoteWholeScalar(text: string): string {
    let q = text[0]
    if ((q !== '"' && q !== "'") || text.length < 2 || text[text.length - 1] !== q) {
      return text
    }
    let last = text.length - 1
    let out = ''
    for (let j = 1; j < last; j++) {
      let c = text[j]
      if (q === "'" && c === "'") {
        if (text[j + 1] !== "'" || j + 1 === last) return text
        out += "'"; j++
      } else if (q === '"' && c === '"') {
        return text
      } else if (q === '"' && c === '\\') {
        let e = text[++j]
        if (j >= last) return text
        if (Object.prototype.hasOwnProperty.call(DQ_ESCAPES, e)) {
          out += DQ_ESCAPES[e]
          continue
        }
        let width = 'x' === e ? 2 : 'u' === e ? 4 : 'U' === e ? 8 : 0
        let cp = hexAt(text, j + 1, width, last)
        if (cp < 0) return text
        j += width
        if ('u' === e && cp >= 0xd800 && cp <= 0xdbff && '\\' === text[j + 1] && 'u' === text[j + 2]) {
          let low = hexAt(text, j + 3, 4, last)
          if (low >= 0xdc00 && low <= 0xdfff) {
            cp = 0x10000 + ((cp - 0xd800) << 10) + (low - 0xdc00)
            j += 6
          }
        }
        if ((cp >= 0xd800 && cp <= 0xdfff) || cp > 0x10ffff) return text
        out += String.fromCodePoint(cp)
      } else {
        out += c
      }
    }
    return out
  }

  // The number the `width` hexadecimal digits at `at` spell, all of them
  // before `end`; -1 when they are not that many, or a width of 0.
  function hexAt(text: string, at: number, width: number, end: number): number {
    if (0 === width || at + width > end) return -1
    let digits = text.substring(at, at + width)
    return /^[0-9a-fA-F]+$/.test(digits) ? parseInt(digits, 16) : -1
  }

  // Whether source position `i` begins a line: the source start, just after
  // a line break, or just after the byte order mark that opens the stream.
  function atLineStart(i: number): boolean {
    return 0 === i || absAt(i - 1) === '\n' || absAt(i - 1) === '\r' ||
      (1 === i && 0xFEFF === absCode(0))
  }

  function isYamlSpace(c: string | undefined): boolean {
    return c === ' ' || c === '\t' || c === '\n' || c === '\r'
  }

  // Whether the character at source position `i` (block context) is the
  // first character of a node: first on its line, or after a `: ` / `- ` /
  // `? ` indicator, a `---` marker, or a node property (`&anchor`, `!tag`)
  // that itself starts a node. Anything else is inside a plain scalar.
  function startsYamlNode(i: number): boolean {
    // Char codes throughout: this runs for every bracket and quote in block
    // text. A byte order mark opening the stream counts as a line start.
    let p = i - 1
    let c = p >= 0 ? absCode(p) : -1
    if (c === 10 || c === 13 || c < 0 || (0 === p && 0xFEFF === c)) return true
    if (c !== 32 && c !== 9) return false
    while (c === 32 || c === 9) c = --p >= 0 ? absCode(p) : -1
    if (c === 10 || c === 13 || c < 0 || (0 === p && 0xFEFF === c)) return true
    if (c === 58) return true // `:`
    if (c === 45 || c === 63) { // `-` / `?`
      if (c === 45 && absCode(p - 1) === 45 && absCode(p - 2) === 45 &&
          (p - 3 < 0 || absCode(p - 3) === 10 || absCode(p - 3) === 13)) return true
      return startsYamlNode(p)
    }
    let w = p
    while (w > 0 && !isYamlSpaceCode(absCode(w - 1))) w--
    c = absCode(w)
    if (c === 38 || c === 33) return startsYamlNode(w) // `&` / `!`
    return false
  }

  // From source position `i` inside a block-context plain scalar, the
  // position where it can end: the next line break, a `:` followed by
  // whitespace or the end, or a `#` after whitespace.
  function plainScalarRest(i: number): number {
    let j = i + 1
    for (;; j++) {
      let c = absCode(j)
      if (c < 0) break
      if (c === 10 || c === 13) break
      if (c === 58) { // `:`
        let n = absCode(j + 1)
        if (n < 0 || isYamlSpaceCode(n)) break
      }
      else if (c === 35) { // `#`
        let b = absCode(j - 1)
        if (b === 32 || b === 9) break
      }
    }
    return j
  }

  function isYamlSpaceCode(c: number): boolean {
    return c === 32 || c === 9 || c === 10 || c === 13
  }

  // Whether the source at position `i` holds `word`.
  function holdsAt(i: number, word: string): boolean {
    for (let k = 0; k < word.length; k++) {
      if (absAt(i + k) !== word[k]) return false
    }
    return true
  }

  // A block scalar indicator (`|` or `>`, optional chomping/indentation
  // indicators, then only a comment) at source position `i`: the position
  // just past its content lines, or `i` when it is not one. Content runs
  // while lines are blank or indented at least as far as the first content
  // line, which must be deeper than the indicator's line (column 0 is
  // allowed for a document's root).
  function blockScalarEnd(i: number): number {
    let j = i + 1
    let explicit = 0
    for (let k = 0; k < 2; k++) {
      if (absAt(j) === '+' || absAt(j) === '-') j++
      else if (absAt(j) >= '1' && absAt(j) <= '9') { explicit = +absAt(j); j++ }
    }
    while (absAt(j) === ' ' || absAt(j) === '\t') j++
    if (absAt(j) === '#') {
      while (absAt(j) !== undefined && absAt(j) !== '\n' && absAt(j) !== '\r') j++
    }
    if (absAt(j) !== undefined && absAt(j) !== '\n' && absAt(j) !== '\r') return i
    let ls = lineStartOf(i)
    if (0 === ls && 0xFEFF === absCode(0)) ls = 1
    let lineIndent = 0
    while (absAt(ls + lineIndent) === ' ') lineIndent++
    let isRoot = ls + lineIndent === i ||
      (absAt(ls) === '-' && absAt(ls + 1) === '-' && absAt(ls + 2) === '-')
    let parentIndent = isRoot && lineIndent === 0 ? -1 : lineIndent
    let pos = j
    if (absAt(pos) === '\r') pos++
    if (absAt(pos) === '\n') pos++
    let blockIndent = -1
    if (explicit > 0) {
      // As the block scalar handler does: after a key on the same line
      // (`- a: |2`), the indent the indicator counts from includes each
      // `- ` before the key, not only the line's leading spaces.
      let base = Math.max(parentIndent, 0)
      let hasColon = false
      for (let ci = ls + lineIndent; ci < i; ci++) {
        if (absAt(ci) === ':' && (absAt(ci + 1) === ' ' || absAt(ci + 1) === '\t')) { hasColon = true; break }
      }
      if (hasColon) {
        let si = ls + lineIndent
        while (si < i && absAt(si) === '-' && (absAt(si + 1) === ' ' || absAt(si + 1) === '\t')) {
          base += 2
          si += 2
          while (si < i && absAt(si) === ' ') { base++; si++ }
        }
      }
      blockIndent = base + explicit
    }
    let end = pos
    while (absAt(pos) !== undefined) {
      let n = 0
      while (absAt(pos + n) === ' ') n++
      let c = absAt(pos + n)
      let lineEnd = pos + n
      while (absAt(lineEnd) !== undefined && absAt(lineEnd) !== '\n' && absAt(lineEnd) !== '\r') lineEnd++
      let next = lineEnd
      if (absAt(next) === '\r') next++
      if (absAt(next) === '\n') next++
      if (c === undefined || c === '\n' || c === '\r') { pos = next; continue }
      if (blockIndent < 0) {
        if (n <= parentIndent) break
        blockIndent = n
      }
      if (n < blockIndent) break
      if (n === 0 && (holdsAt(pos, '---') || holdsAt(pos, '...'))) break
      end = lineEnd
      pos = next
    }
    return end
  }


  tabnas.options({
    fixed: {
      token: {
        // Single colon is not a YAML token, so remove.
        '#CL': null,
      }
    },

    // Colons can still end unquoted text (TX, lexer.textMatcher).
    ender: ':',

    // Remove all jsonic string chars — YAML handles quotes in yamlMatcher.
    // Backtick is not a string delimiter in YAML.
    string: {
      chars: '',
    },

    // Skip number matching when yamlMatcher detected trailing text
    // after a digit-starting value (e.g. "64 characters, hexadecimal.").
    number: {
      check: (_lex: any) => {
        if (skipNumberMatch) {
          skipNumberMatch = false
          return { done: true }
        }
      },
    },

    // Custom text check: consume to end of line (including spaces)
    // for YAML plain scalar values.
    text: {
      check: (lex: any) => {
        viewOf(lex)
        readOn = false
        let pnt = lex.pnt
        // The text from the cursor on: F(i) is the character i past it,
        // read through the window, and FS(a, b) the text between.
        const fwdAt = pnt.sI
        const F = (i: number): string => srcAt(fwdAt + i)
        const FS = (a: number, b: number): string => srcSub(fwdAt + a, fwdAt + b)

        let ch = F(0)
        // Block scalar: | or > (with optional chomping indicator)
        if (ch === '|' || ch === '>') {
          let fold = ch === '>'
          let chomp = 'clip' // default: single trailing newline
          let explicitIndent = 0
          let idx = 1
          // Parse optional chomping and indentation indicators in either order.
          // Valid: |, |+, |-, |2, |+2, |-2, |2+, |2-
          for (let pi = 0; pi < 2; pi++) {
            if (F(idx) === '+') { chomp = 'keep'; idx++ }
            else if (F(idx) === '-') { chomp = 'strip'; idx++ }
            else if (F(idx) >= '1' && F(idx) <= '9') { explicitIndent = parseInt(F(idx)); idx++ }
          }

          // Must be followed by newline (possibly with trailing spaces/comment)
          while (F(idx) === ' ') idx++
          if (F(idx) === '#') {
            while (F(idx) !== undefined && F(idx) !== '\n' && F(idx) !== '\r') idx++
          }
          if (F(idx) !== '\n' && F(idx) !== '\r' && F(idx) !== undefined) {
            // Not a block scalar — fall through to normal text handling.
          } else {
            // Skip the indicator line.
            if (F(idx) === '\r') idx++
            if (F(idx) === '\n') idx++

            // Determine block indent from first content line,
            // or use explicit indent indicator if provided.
            let blockIndent = 0
            if (explicitIndent === 0) {
              // Auto-detect: skip blank lines, find first content line.
              let tempIdx = idx
              while (F(tempIdx) !== undefined) {
                let lineSpaces = 0
                while (F(tempIdx + lineSpaces) === ' ') lineSpaces++
                let afterSpaces = tempIdx + lineSpaces
                if (F(afterSpaces) === undefined || F(afterSpaces) === '\n' || F(afterSpaces) === '\r') {
                  // Blank line — skip it.
                  tempIdx = afterSpaces
                  if (F(tempIdx) === '\r') tempIdx++
                  if (F(tempIdx) === '\n') tempIdx++
                  continue
                }
                blockIndent = lineSpaces
                break
              }
            }

            // Determine the indent of the line containing the block indicator.
            // If blockIndent <= that indent, the block is empty (content must
            // be more indented than the containing line). Exception: after ---,
            // the containing indent is effectively -1 so blockIndent 0 is valid.
            let containingIndent = 0
            let isDocStart = false
            let isDocRoot = false
            // The indicator's source position: the scans back from it read
            // the source's own lines, whatever the window holds.
            const here = viewBase + pnt.sI
            {
              // Walk back from the indicator to the start of its own line.
              // Start at the indicator, not one before it: when the indicator
              // IS the first character of its line, one before is the
              // preceding newline and the scan would land on the PREVIOUS
              // line, mis-reading both containingIndent and the --- check.
              let li = lineStartOf(here)
              if (0 === li && 0xFEFF === absCode(0)) li = 1
              let lineStart = li
              while (li < here && absAt(li) === ' ') { containingIndent++; li++ }
              // Check if this line starts with --- (document start marker).
              if (absAt(lineStart) === '-' && absAt(lineStart+1) === '-' && absAt(lineStart+2) === '-') {
                isDocStart = true
              }
              // The indicator is the first thing on a column-0 line, so nothing
              // can enclose it — it can only be a document's root node. YAML
              // gives the root node parent indent -1, so content at column 0 is
              // "more indented" and the block is not empty. (yaml-test-suite
              // M7A3: a bare `|` document whose content starts at column 0.)
              isDocRoot = 0 === containingIndent && li === here
            }
            // Apply explicit indent relative to containing indent.
            // Per YAML spec, the content indentation = block scalar's indent
            // level + indicator value. The block scalar's indent level is the
            // indent of the containing block (e.g., the mapping key), which
            // may differ from the line's leading spaces (e.g., after "- ").
            if (explicitIndent > 0) {
              // Find the line containing the block indicator. Start at the
              // indicator (not one before it) for the same reason as the
              // containingIndent scan above: an indicator that begins its
              // own line would otherwise resolve to the PREVIOUS line.
              let li = lineStartOf(here)
              // li is now at the start of the line. Find the colon position.
              let keyCol = containingIndent
              // Check if there's a colon on the SAME line as the block indicator.
              let hasColonOnLine = false
              for (let ci = li + containingIndent; ci < here; ci++) {
                if (absAt(ci) === ':' && (absAt(ci+1) === ' ' || absAt(ci+1) === '\t')) {
                  hasColonOnLine = true
                  break
                }
              }
              if (hasColonOnLine) {
                // Block indicator on same line as colon (e.g., "key: |2").
                // Check for sequence indicators: each "- " adds to the effective indent.
                let scanI = li + containingIndent
                while (scanI < here && absAt(scanI) === '-' &&
                       (absAt(scanI+1) === ' ' || absAt(scanI+1) === '\t')) {
                  keyCol += 2
                  scanI += 2
                  while (scanI < here && absAt(scanI) === ' ') { keyCol++; scanI++ }
                }
                blockIndent = keyCol + explicitIndent
              } else {
                // Block indicator on its own line (e.g., after a tag on
                // a separate line). Look backward to find the parent
                // mapping key's indent by scanning previous lines for
                // the colon that started this value context.
                let parentIndent = 0
                let searchI = li - 1
                while (searchI > 0) {
                  // Find start of previous line.
                  if (absAt(searchI) === '\n') searchI--
                  if (absAt(searchI) === '\r') searchI--
                  let prevLineEnd = searchI + 1
                  while (searchI > 0 && absAt(searchI - 1) !== '\n' && absAt(searchI - 1) !== '\r') searchI--
                  let prevLineStart = searchI
                  // Check if this line has a colon (mapping key).
                  for (let ci = prevLineStart; ci < prevLineEnd; ci++) {
                    if (absAt(ci) === ':' && (absAt(ci+1) === ' ' || absAt(ci+1) === '\t' ||
                        absAt(ci+1) === '\n' || absAt(ci+1) === '\r' || ci+1 >= prevLineEnd)) {
                      // Found the parent key line. Get its indent.
                      parentIndent = 0
                      let pi = prevLineStart
                      while (pi < prevLineEnd && absAt(pi) === ' ') { parentIndent++; pi++ }
                      break
                    }
                  }
                  break  // Only check the immediately preceding non-blank line.
                }
                blockIndent = parentIndent + explicitIndent
                // Update containingIndent to parent's indent so the
                // "blockIndent <= containingIndent" check below works.
                containingIndent = parentIndent
              }
            }
            if (blockIndent <= containingIndent && !isDocStart && !isDocRoot &&
                F(idx) !== undefined) {
              // Content is not indented enough — empty block scalar.
              // For keep chomping, count trailing blank lines.
              let val: string
              if (chomp === 'keep') {
                let blankCount = 0
                let bi = idx
                while (F(bi) !== undefined) {
                  if (F(bi) === '\n') { blankCount++; bi++ }
                  else if (F(bi) === '\r') { bi++; if (F(bi) === '\n') bi++; blankCount++ }
                  else break
                }
                val = '\n'.repeat(blankCount > 0 ? blankCount : 1)
                idx = bi
              } else {
                val = chomp === 'strip' ? '' : ''
              }
              let src = FS(0, idx)
              let tkn = lex.token('#TX', val, src, pnt)
              pnt.sI += idx
              pnt.rI += 1
              pnt.cI = 0
              return { done: true, token: tkn }
            }

            // Collect indented lines.
            let lines: string[] = []
            let pos = idx
            let rows = 1 // Already consumed one newline
            let lastNewlinePos = idx // Track position before last consumed newline
            while (F(pos) !== undefined) {
              // Check indent of current line.
              let lineIndent = 0
              while (F(pos + lineIndent) === ' ') lineIndent++

              // Blank line (only whitespace before newline or end).
              let afterSpaces = pos + lineIndent
              if (F(afterSpaces) === undefined || F(afterSpaces) === '\n' || F(afterSpaces) === '\r') {
                // Preserve spaces beyond block indent on blank lines.
                if (lineIndent > blockIndent) {
                  lines.push(FS(pos + blockIndent, afterSpaces))
                } else {
                  lines.push('')
                }
                lastNewlinePos = afterSpaces
                pos = afterSpaces
                if (F(pos) === '\r') pos++
                if (F(pos) === '\n') pos++
                rows++
                continue
              }

              // Less indent means end of block.
              if (lineIndent < blockIndent) break

              // Stop at document markers (--- or ...) at indent 0.
              if (lineIndent === 0 &&
                  ((F(pos) === '-' && F(pos+1) === '-' && F(pos+2) === '-' &&
                    (F(pos+3) === '\n' || F(pos+3) === '\r' || F(pos+3) === ' ' || F(pos+3) === undefined)) ||
                   (F(pos) === '.' && F(pos+1) === '.' && F(pos+2) === '.' &&
                    (F(pos+3) === '\n' || F(pos+3) === '\r' || F(pos+3) === ' ' || F(pos+3) === undefined)))) break

              // Consume the line content (strip block indent).
              let lineStart = pos + blockIndent
              let lineEnd = lineStart
              while (F(lineEnd) !== undefined && F(lineEnd) !== '\n' && F(lineEnd) !== '\r') lineEnd++
              lines.push(FS(lineStart, lineEnd))
              lastNewlinePos = lineEnd
              pos = lineEnd
              if (F(pos) === '\r') pos++
              if (F(pos) === '\n') pos++
              rows++
            }

            // Build the scalar value.
            let val: string
            if (fold) {
              // Folded: newlines between "normal" lines become spaces.
              // Empty lines and "more indented" lines are preserved literally.
              let result = ''
              let prevWasNormal = false
              let pendingEmptyCount = 0
              for (let li = 0; li < lines.length; li++) {
                let line = lines[li]
                let isMore = line.length > 0 && (line[0] === ' ' || line[0] === '\t')
                let isEmpty = line === ''

                if (isEmpty) {
                  pendingEmptyCount++
                } else if (isMore) {
                  // Flush pending empty lines, with paragraph-close if needed.
                  if (prevWasNormal && result.length > 0) result += '\n'
                  for (let ei = 0; ei < pendingEmptyCount; ei++) result += '\n'
                  pendingEmptyCount = 0
                  // "More indented" — preserve with newlines around it.
                  if (result.length > 0 && result[result.length - 1] !== '\n') {
                    result += '\n'
                  }
                  result += line + '\n'
                  prevWasNormal = false
                } else {
                  // Normal line.
                  if (pendingEmptyCount > 0) {
                    // Empty lines between content: emit them.
                    // The transition normal→empty→normal needs paragraph-close \n
                    // (which counts as the first empty line).
                    if (prevWasNormal && result.length > 0) {
                      // First empty line is the paragraph break.
                      result += '\n'
                      for (let ei = 1; ei < pendingEmptyCount; ei++) result += '\n'
                    } else {
                      for (let ei = 0; ei < pendingEmptyCount; ei++) result += '\n'
                    }
                    pendingEmptyCount = 0
                  }
                  if (prevWasNormal && result.length > 0 && result[result.length - 1] !== '\n') {
                    // Join with space (folding).
                    result += ' '
                  }
                  result += line
                  prevWasNormal = true
                }
              }
              // Flush trailing empty lines.
              for (let ei = 0; ei < pendingEmptyCount; ei++) result += '\n'
              val = result
            } else {
              // Literal: preserve newlines.
              val = lines.join('\n')
            }

            // Apply chomping.
            if (lines.length === 0) {
              // No content lines at all — result is empty string
              // regardless of chomping.
              val = ''
            } else if (chomp === 'strip') {
              val = val.replace(/\n+$/, '')
            } else if (chomp === 'clip') {
              val = val.replace(/\n+$/, '') + '\n'
            } else {
              // keep: preserve all trailing newlines
              val = val + '\n'
            }

            // If block ended because of less indent (more content follows
            // that isn't a doc marker), don't consume the final newline —
            // leave it for the yamlMatcher to emit #IN so the grammar can
            // continue properly.
            let endPos = pos
            let endRows = rows
            if (F(pos) !== undefined && pos > lastNewlinePos) {
              // Check if the next content is a doc marker (--- or ...).
              let nextLineIndent = 0
              let ni = pos
              while (F(ni) === ' ') { nextLineIndent++; ni++ }
              let isDocMarker = nextLineIndent === 0 &&
                ((F(ni) === '-' && F(ni+1) === '-' && F(ni+2) === '-') ||
                 (F(ni) === '.' && F(ni+1) === '.' && F(ni+2) === '.'))
              if (!isDocMarker) {
                // Regular content follows — back up to the newline position.
                endPos = lastNewlinePos
                endRows = rows - 1
              }
            }
            let src = FS(0, endPos)
            let tkn = lex.token('#TX', val, src, pnt)
            pnt.sI += endPos
            pnt.rI += endRows
            pnt.cI = 0
            return { done: true, token: tkn }
          }
        }

        // YAML tags: !!type value
        if (ch === '!' && F(1) === '!') {
          let tagEnd = 2
          while (F(tagEnd) !== undefined && F(tagEnd) !== ' ' && F(tagEnd) !== '\n' &&
                 F(tagEnd) !== '\r') tagEnd++
          let tag = FS(2, tagEnd)
          // For !!seq and !!map, these are handled in the yamlMatcher.
          if (tag === 'seq' || tag === 'map') {
            return null  // Don't advance pnt — let yamlMatcher handle it.
          }
          // For value tags, parse the value after the tag.
          let valStart = tagEnd
          if (F(valStart) === ' ') valStart++
          // Get the raw value string.
          let rawVal = ''
          let valEnd = valStart
          if (F(valStart) === '"' || F(valStart) === "'") {
            // Quoted value — find matching close quote.
            let q = F(valStart)
            valEnd = valStart + 1
            while (F(valEnd) !== undefined && F(valEnd) !== q) {
              if (F(valEnd) === '\\' && q === '"') valEnd++  // Skip escape in double quotes.
              valEnd++
            }
            if (F(valEnd) === q) valEnd++
            rawVal = FS(valStart + 1, valEnd - 1)
          } else {
            // Unquoted value — to end of line, stopping at `: ` and ` #`.
            // A `#` straight after the tag's space starts a comment, so
            // the value is empty (`!!str #c` is "").
            while (F(valEnd) !== undefined && F(valEnd) !== '\n' && F(valEnd) !== '\r') {
              if (valEnd === valStart && valStart > tagEnd && F(valEnd) === '#') break
              if (F(valEnd) === ':' && (F(valEnd+1) === ' ' || F(valEnd+1) === '\n' ||
                  F(valEnd+1) === '\r' || F(valEnd+1) === undefined)) break
              if (F(valEnd) === ' ' && F(valEnd+1) === '#') break
              valEnd++
            }
            rawVal = FS(valStart, valEnd).replace(/\s+$/, '')
          }
          // Apply tag conversion.
          let result: any
          if (tag === 'str') result = String(rawVal)
          else if (tag === 'int') result = yamlTagInt(rawVal)
          else if (tag === 'float') result = yamlTagFloat(rawVal)
          else if (tag === 'bool') result = rawVal === 'true' || rawVal === 'True' || rawVal === 'TRUE'
          else if (tag === 'null') result = null
          else result = rawVal  // Unknown tag — keep as string.

          let src = FS(0, valEnd)
          let tknTin = typeof result === 'string' ? '#TX' :
                       typeof result === 'number' ? '#NR' :
                       '#VL'
          let tkn = lex.token(tknTin, result, src, pnt)
          pnt.sI += valEnd
          pnt.cI += valEnd
          return { done: true, token: tkn }
        }

        // Don't apply text check for special chars or flow context.
        // Also skip * and & which are YAML alias/anchor indicators.
        if (ch === '{' || ch === '}' || ch === '[' || ch === ']' ||
            ch === ',' || ch === '#' || ch === '\n' ||
            ch === '\r' || ch === '"' || ch === "'" ||
            ch === '*' || ch === '&' || ch === '!' ||
            ch === undefined) {
          return null
        }
        // Colon only starts a key-value separator if followed by space/tab/newline/eof.
        // Otherwise it can start a plain scalar (e.g. ::vector).
        if (ch === ':' && (F(1) === ' ' || F(1) === '\t' || F(1) === '\n' ||
            F(1) === '\r' || F(1) === undefined)) {
          return null
        }

        // Match text to end of line, stopping at `: `, `:\n`, ` #`, or newline.
        // This handles YAML plain scalars with multiline continuation.
        const here = viewBase + pnt.sI
        updateFlowState(here)
        let inFlowCtx = _flowDepth > 0
        // Find key indent and determine context for multiline scalars.
        let lineStart = lineStartOf(here)
        if (0 === lineStart && 0xFEFF === absCode(0)) lineStart = 1

        // Current line indent (indent of the line where text starts).
        let currentLineIndent = 0
        {
          let ci = lineStart
          while (ci < here && absAt(ci) === ' ') { currentLineIndent++; ci++ }
        }

        // Check if text is preceded by ": " on the same line (map value context).
        let isMapValue = false
        {
          let ci = here - 1
          // Skip whitespace before text
          while (ci >= lineStart && (absAt(ci) === ' ' || absAt(ci) === '\t')) ci--
          if (ci >= lineStart && absAt(ci) === ':') isMapValue = true
        }

        // The minimum indent for continuation lines.
        // For map values: continuation requires indent > the colon's line
        // indent; for standalone scalars, indent >= the current line's.
        let minContinuationIndent = isMapValue ? currentLineIndent + 1 : currentLineIndent
        let text = ''
        let i = 0
        let totalConsumed = 0
        let rows = 0
        let scanLine = () => {
          let line = ''
          while (F(i) !== undefined) {
            let c = F(i)
            if (c === '\n' || c === '\r') break
            if (c === ':' && (F(i + 1) === ' ' || F(i + 1) === '\t' || F(i + 1) === '\n' ||
                F(i + 1) === '\r' || F(i + 1) === undefined)) break
            if ((c === ' ' || c === '\t') && F(i + 1) === '#') break
            if (inFlowCtx && (c === ']' || c === '}')) break
            if (c === ',' && inFlowCtx) break
            line += c
            i++
          }
          return line.replace(/\s+$/, '')
        }

        text = scanLine()
        totalConsumed = i

        // Check for continuation lines (multiline plain scalars).
        // Blank lines (whitespace-only) within a scalar become newlines.
        while (F(i) === '\n' || F(i) === '\r') {
          let nlPos = i
          // Count blank lines (lines with only whitespace).
          let blankLines = 0
          while (F(i) === '\n' || F(i) === '\r') {
            if (F(i) === '\r') i++
            if (F(i) === '\n') i++
            // Count indent of next line.
            let li = 0
            while (F(i + li) === ' ' || F(i + li) === '\t') li++
            if (F(i + li) === undefined || F(i + li) === '\n' || F(i + li) === '\r') {
              // Blank line — count it and skip.
              blankLines++
              i += li
              continue
            }
            break
          }
          // Count indent of the content line after blank lines.
          let lineIndent = 0
          while (F(i) === ' ' || F(i) === '\t') { lineIndent++; i++ }
          // In flow context, continuation is allowed regardless of indent
          // (as long as the next line doesn't start a flow indicator or comment).
          // In block context, must be more indented than the key.
          // Check for document markers (--- or ...) at column 0.
          let isDocMarker = lineIndent === 0 &&
            ((F(i) === '-' && F(i+1) === '-' && F(i+2) === '-' &&
              (F(i+3) === ' ' || F(i+3) === '\t' || F(i+3) === '\n' ||
               F(i+3) === '\r' || F(i+3) === undefined)) ||
             (F(i) === '.' && F(i+1) === '.' && F(i+2) === '.' &&
              (F(i+3) === ' ' || F(i+3) === '\t' || F(i+3) === '\n' ||
               F(i+3) === '\r' || F(i+3) === undefined)))
          // Check for sequence marker "- ". Only treat as a new sequence
          // entry when the indent matches an enclosing sequence's level.
          // Find the nearest "- " sequence marker preceding the text on
          // the first line to determine the relevant sequence indent.
          let isSeqMarker = false
          if (F(i) === '-' &&
              (F(i+1) === ' ' || F(i+1) === '\t' || F(i+1) === '\n' ||
               F(i+1) === '\r' || F(i+1) === undefined)) {
            // Determine the sequence indent from the first line's context.
            // Look backward from the text's start to find "- " markers before it.
            let seqIndent = -1
            let si = here - 1
            while (si >= lineStart) {
              if (absAt(si) === '-' && (absAt(si+1) === ' ' || absAt(si+1) === '\t')) {
                seqIndent = si - lineStart
                break
              }
              si--
            }
            // isSeqMarker if the continuation "- " matches a known sequence
            // indent, or if it's at the current line indent level.
            isSeqMarker = (seqIndent >= 0 && lineIndent === seqIndent) ||
                          (seqIndent < 0 && lineIndent <= currentLineIndent)
          }
          let canContinue = inFlowCtx
            ? (F(i) !== undefined && F(i) !== '\n' && F(i) !== '\r' &&
               F(i) !== '#' && F(i) !== '{' && F(i) !== '}' &&
               F(i) !== '[' && F(i) !== ']')
            : (lineIndent >= minContinuationIndent && F(i) !== undefined &&
               F(i) !== '\n' && F(i) !== '\r' && F(i) !== '#' &&
               !isDocMarker && !isSeqMarker)
          if (canContinue) {
            // Check if this line is a key-value pair (contains ": ").
            let peekJ = i
            let isKV = false
            while (F(peekJ) !== undefined && F(peekJ) !== '\n' && F(peekJ) !== '\r') {
              if (F(peekJ) === ':' && (F(peekJ + 1) === ' ' || F(peekJ + 1) === '\t' ||
                  F(peekJ + 1) === '\n' || F(peekJ + 1) === '\r' ||
                  F(peekJ + 1) === undefined)) {
                isKV = true
                break
              }
              if (F(peekJ) === '}' || F(peekJ) === ']' || F(peekJ) === ',') {
                break
              }
              peekJ++
            }
            if (!isKV || inFlowCtx) {
              let contLine = scanLine()
              if (contLine.length > 0) {
                // Blank lines → newlines; single newline → space (folding).
                if (blankLines > 0) {
                  for (let b = 0; b < blankLines; b++) text += '\n'
                } else {
                  text += ' '
                }
                text += contLine
                totalConsumed = i
                rows++
                continue
              }
            }
          }
          // Not a continuation — revert to newline position.
          i = nlPos
          break
        }

        text = text.replace(/\s+$/, '')

        if (text.length === 0) return null

        // Check if this is a known YAML value.
        let valMap: Record<string, any> = {
          'true': true, 'True': true, 'TRUE': true,
          'false': false, 'False': false, 'FALSE': false,
          'null': null, 'Null': null, 'NULL': null,
          '~': null,
          'yes': true, 'Yes': true, 'YES': true,
          'no': false, 'No': false, 'NO': false,
          'on': true, 'On': true, 'ON': true,
          'off': false, 'Off': false, 'OFF': false,
          '.inf': Infinity, '.Inf': Infinity, '.INF': Infinity,
          '+.inf': Infinity, '+.Inf': Infinity, '+.INF': Infinity,
          '-.inf': -Infinity, '-.Inf': -Infinity, '-.INF': -Infinity,
          '.nan': NaN, '.NaN': NaN, '.NAN': NaN,
        }
        if (text in valMap) {
          let tkn = lex.token('#VL', valMap[text], text, pnt)
          pnt.sI += text.length
          pnt.cI += text.length
          return { done: true, token: tkn }
        }

        // Check if it's a number.
        let num = +text
        if (!isNaN(num) && text !== '') {
          let tkn = lex.token('#NR', num, text, pnt)
          pnt.sI += text.length
          pnt.cI += text.length
          return { done: true, token: tkn }
        }

        // Plain text — consume to end of meaningful content.
        let src = FS(0, totalConsumed)
        let tkn = lex.token('#TX', text, src, pnt)
        pnt.sI += totalConsumed
        pnt.rI += rows
        pnt.cI += totalConsumed  // approximate
        return { done: true, token: tkn }
      },
    },
  })

  // Register #EL token (not as a fixed token — we match it in yamlMatcher).
  let EL = tabnas.token('#EL')

  // Register #QM token: the YAML `?` explicit-key indicator inside flow
  // collections. Emitted by yamlMatcher only when in flow context and
  // followed by whitespace; consumed by pair/elem rule alts below.
  let QM = tabnas.token('#QM')

  // YAML document-frame tokens, emitted by yamlMatcher at column 0:
  //   #DS  document start: ---     (with optional inline content following)
  //   #DE  document end:   ...
  //   #DR  directive line: %YAML 1.2  /  %TAG !! tag:...
  // The `stream` rule consumes them; rules apply directives and accumulate
  // each document's value into the result.
  let DS = tabnas.token('#DS')
  let DE = tabnas.token('#DE')
  let DR = tabnas.token('#DR')

  // Flow collection tokens.
  let CA = tabnas.token.CA  // comma
  let CS = tabnas.token.CS  // ]
  let CB = tabnas.token.CB  // }
  let OS = tabnas.token.OS  // [
  let OB = tabnas.token.OB  // {

  // All tokens that can start a value.
  let KEY = [TX, NR, ST, VL]

  // Add a custom lex matcher for YAML special cases.
  tabnas.options({
    lex: {
      match: {
        yaml: {
          order: 5e5,
          make: (_cfg: Config, _opts: Options) => {
            // Track Lex objects we've already initialised. Identity comparison
            // distinguishes parse invocations correctly even when the same
            // source string is parsed twice in a row — `lex.src !== cleanedSrc`
            // would skip the reset on the second call and pollute output with
            // state from the prior parse.
            const seenLex: WeakSet<Lex> = new WeakSet()
            return function yamlMatcher(lex: Lex) {
              // First call of a new parse: reset per-parse state.
              // Document-frame syntax (--- / ... / %YAML / %TAG) is no longer
              // mutated out of lex.src here — it flows through as #DS / #DE /
              // #DR tokens consumed by the `stream` rule.
              let first = false
              if (!seenLex.has(lex)) {
                seenLex.add(lex)
                first = true
                anchors = Object.create(null)
                pendingAnchors = []
                declaredAnchors = new Set()
                pendingExplicitCL = false
                skipNumberMatch = false
                tagHandles = {}
                yamlStreamDocs = []
                yamlStreamMeta = []
                yamlStreamCurMeta = null
                _flowDepth = 0
                _flowScanPos = 0
                _flowScanUpTo = 0
                _inSingleQuote = false
                _inDoubleQuote = false
                resetView(lex)
              }
              readOn = true
              beginCut(lex)
              // Empty / whitespace-only / comments-only source: emit one
              // null #VL so the parser yields `null` rather than an error.
              // The source has been read to its end when it is so.
              if (first && sourceBlank()) {
                lex.pnt.len = 0
                let tkn = lex.token('#VL', null, '', lex.pnt)
                lex.pnt.sI = 0
                if (lex.windowed) {
                  // The window's length is the source's again once the
                  // cut is made: the end is the next token, at the start.
                  lex.pnt.end = lex.token('#ZZ', undefined, '', lex.pnt)
                }
                return tkn
              }
              const tkn = yamlMatch(lex)
              // What this call stepped over without a token of its own (an
              // anchor, a tag) is the window's to drop at the next cut, so
              // `hist` takes it now.
              histSync(viewBase + lex.pnt.sI)
              return tkn
            }
          }
        }
      }
    }
  })

  // The YAML matcher's body, run with the source read through the window.
  function yamlMatch(lex: Lex): Token | undefined {
              // A byte order mark opening the stream is not content: step
              // over it without counting a column. atLineStart and the
              // flow scan treat the offset after it as a line start. Only
              // the source's start can hold it, and every cut made there
              // steps over it, the engine's own again over a wider window
              // among them.
              if (0 === viewBase + lex.pnt.sI && 0xFEFF === absCode(0)) {
                lex.pnt.sI = 1
              }

              let pnt = lex.pnt
              // The text from the cursor on: F(i) is the character i past
              // `fwdAt`, the cursor as it stood when last taken, read through
              // the window, and FS(a, b) the text between.
              let fwdAt = pnt.sI
              const F = (i: number): string => srcAt(fwdAt + i)
              const FS = (a: number, b: number): string => srcSub(fwdAt + a, fwdAt + b)

              // Loop to restart matching after consuming flow whitespace.
              yamlMatchLoop: while (true) {

              // Skip blank lines that contain only tabs (and maybe spaces).
              // YAML treats these as blank lines, but jsonic errors on bare tabs.
              // The line is what lies before the next line feed: blank when
              // the run of tabs and spaces at the cursor, a tab among them,
              // reaches it or the source's end.
              if (F(0) === '\t' || F(0) === ' ') {
                let k = 0
                let tab = false
                while (F(k) === ' ' || F(k) === '\t') {
                  if (F(k) === '\t') tab = true
                  k++
                }
                if (tab && (F(k) === '\n' || F(k) === undefined)) {
                  let skip = F(k) === '\n' ? k + 1 : k
                  pnt.sI += skip
                  pnt.rI++
                  pnt.cI = 0
                  fwdAt = pnt.sI
                  continue yamlMatchLoop
                }
              }

              // Emit pending CL from explicit key — must be before !!type handlers
              // so the CL token appears before the value token.
              if (pendingExplicitCL) {
                pendingExplicitCL = false
                let tkn = lex.token('#CL', 1, ': ', lex.pnt)
                return tkn
              }

              // YAML alias: *name — emit a VL token with alias name.
              // Resolution happens at grammar time (val.ac) since the anchor
              // may not be recorded yet due to lexer pre-fetching.
              if (F(0) === '*') {
                let nameEnd = 1
                while (F(nameEnd) !== undefined && F(nameEnd) !== ' ' && F(nameEnd) !== '\t' &&
                       F(nameEnd) !== '\n' && F(nameEnd) !== '\r' && F(nameEnd) !== ',' &&
                       F(nameEnd) !== '{' && F(nameEnd) !== '}' && F(nameEnd) !== '[' &&
                       F(nameEnd) !== ']') {
                  // Colon terminates only when followed by space/tab (key-value separator).
                  // Otherwise colon is a valid anchor-name character per YAML spec.
                  if (F(nameEnd) === ':' &&
                      (F(nameEnd+1) === ' ' || F(nameEnd+1) === '\t')) break
                  nameEnd++
                }
                let name = FS(1, nameEnd)
                let src = FS(0, nameEnd)
                // YAML 1.2.2, 7.1: "It is an error for an alias node to use
                // an anchor that does not previously occur in the document."
                // Checked here, as a key or as a value, because an alias read
                // as nothing hides a mistyped name.
                if (!declaredAnchors.has(name)) {
                  return lex.bad('yaml_undefined_alias', pnt.sI, pnt.sI + nameEnd)
                }
                // Check if this alias is used as a map key (followed by ` :` or `:`).
                let afterAlias = nameEnd
                while (F(afterAlias) === ' ' || F(afterAlias) === '\t') afterAlias++
                let isKey = F(afterAlias) === ':' &&
                  (F(afterAlias+1) === ' ' || F(afterAlias+1) === '\t' ||
                   F(afterAlias+1) === '\n' || F(afterAlias+1) === '\r' ||
                   F(afterAlias+1) === undefined)
                if (isKey && anchors[name] !== undefined) {
                  // Resolve alias immediately as a key string.
                  let resolved = String(anchors[name])
                  let tkn = lex.token('#TX', resolved, src, lex.pnt)
                  pnt.sI += nameEnd
                  pnt.cI += nameEnd
                  return tkn
                }
                // Resolve alias immediately if anchor exists, since deferred
                // markers can be lost through Jsonic's rule processing.
                let tkn: any
                if (anchors[name] !== undefined) {
                  let val = anchors[name]
                  if (typeof val === 'object' && val !== null) {
                    val = copyAnchored(val)
                  }
                  let tin = typeof val === 'string' ? '#TX' :
                            typeof val === 'number' ? '#NR' : '#VL'
                  tkn = lex.token(tin, val, src, lex.pnt)
                } else {
                  // Anchor not yet seen — store marker for deferred resolution.
                  let marker = { __yamlAlias: name }
                  tkn = lex.token('#VL', marker, src, lex.pnt)
                }
                pnt.sI += nameEnd
                pnt.cI += nameEnd
                return tkn
              }

              // YAML anchor: &name — store value. Skip the anchor marker,
              // let the value be parsed, and record it post-parse via grammar rules.
              if (F(0) === '&') {
                let nameEnd = 1
                while (F(nameEnd) !== undefined && F(nameEnd) !== ' ' && F(nameEnd) !== '\t' &&
                       F(nameEnd) !== '\n' && F(nameEnd) !== '\r' && F(nameEnd) !== ',' &&
                       F(nameEnd) !== '{' && F(nameEnd) !== '}' && F(nameEnd) !== '[' &&
                       F(nameEnd) !== ']') nameEnd++
                let anchorName = FS(1, nameEnd)
                let skip = nameEnd
                if (F(skip) === ' ' || F(skip) === '\t') skip++
                // Check if anchor is standalone (first content on its line).
                // Look backward to see if only whitespace precedes & on this line.
                let isStandalone = true
                let anchorIndent = 0
                {
                  let bi = viewBase + pnt.sI - 1
                  while (bi >= 0 && absAt(bi) !== '\n' && absAt(bi) !== '\r') {
                    if (absAt(bi) !== ' ' && absAt(bi) !== '\t') {
                      isStandalone = false
                      break
                    }
                    anchorIndent++
                    bi--
                  }
                }
                pnt.sI += skip
                pnt.cI += skip
                // Skip an inline comment after the anchor (e.g. `&a # note`),
                // so the `#` is not mis-lexed as #BD and not captured as the
                // anchor's scalar value. The following newline is handled next.
                {
                  let ci = pnt.sI
                  while (srcAt(ci) === ' ' || srcAt(ci) === '\t') ci++
                  if (srcAt(ci) === '#') {
                    while (srcAt(ci) !== undefined && srcAt(ci) !== '\n' && srcAt(ci) !== '\r') ci++
                    pnt.cI += (ci - pnt.sI)
                    pnt.sI = ci
                  }
                }
                // Determine if anchor is inline (content follows on same line)
                // or standalone (only newline follows).
                let anchorInline = !(isStandalone &&
                    (srcAt(pnt.sI) === '\r' || srcAt(pnt.sI) === '\n' ||
                     srcAt(pnt.sI) === undefined))
                // For inline anchors before scalar values, record the anchor
                // immediately so aliases in later pairs can resolve them.
                if (anchorInline) {
                  const peekAt = pnt.sI
                  const P = (i: number): string => srcAt(peekAt + i)
                  let pch = P(0)
                  if (pch !== '[' && pch !== '{' && pch !== '>' && pch !== '|' &&
                      pch !== '\n' && pch !== '\r' && pch !== undefined) {
                    let scalarVal: string | undefined
                    if (pch === '"') {
                      let ei = 1
                      while (P(ei) !== undefined && P(ei) !== '"') {
                        if (P(ei) === '\\') ei++
                        ei++
                      }
                      scalarVal = srcSub(peekAt + 1, peekAt + ei)
                        .replace(/\\n/g, '\n').replace(/\\t/g, '\t')
                        .replace(/\\\\/g, '\\').replace(/\\"/g, '"')
                    } else if (pch === "'") {
                      let ei = 1
                      while (P(ei) !== undefined && P(ei) !== "'") {
                        if (P(ei) === "'" && P(ei+1) === "'") ei++
                        ei++
                      }
                      scalarVal = srcSub(peekAt + 1, peekAt + ei).replace(/''/g, "'")
                    } else {
                      let ei = 0
                      while (P(ei) !== undefined && P(ei) !== '\n' && P(ei) !== '\r' &&
                             P(ei) !== ',' && P(ei) !== '}' && P(ei) !== ']') {
                        if (P(ei) === ':' && (P(ei+1) === ' ' || P(ei+1) === '\t' ||
                            P(ei+1) === '\n' || P(ei+1) === '\r' ||
                            P(ei+1) === undefined)) break
                        if (P(ei) === ' ' && P(ei+1) === '#') break
                        ei++
                      }
                      let raw = srcSub(peekAt, peekAt + ei).trim()
                      if (raw.length > 0) scalarVal = raw
                    }
                    if (scalarVal !== undefined) {
                      setAnchor(anchorName, scalarVal)
                    }
                  }
                }
                // Push pending anchor with inline flag.
                pendAnchor(anchorName, anchorInline)
                // If the anchored value is a flow collection (&a [..] / &a {..}),
                // emit its opener here: we've advanced past the anchor marker,
                // so jsonic's fixed-token matcher would mis-lex it as #BD.
                {
                  let ac = srcAt(pnt.sI)
                  if (ac === '[' || ac === '{') {
                    let tkn = lex.token(ac === '[' ? '#OS' : '#OB', undefined, ac, pnt)
                    pnt.sI += 1
                    pnt.cI += 1
                    return tkn
                  }
                }
                // If anchor is standalone on its own line (followed by newline),
                // consume the newline and leading spaces so no extra IN token
                // is emitted. Only consume when next line indent >= anchor indent,
                // otherwise let the normal indent handler manage the transition.
                if (isStandalone &&
                    (srcAt(pnt.sI) === '\r' || srcAt(pnt.sI) === '\n')) {
                  let nl = pnt.sI
                  if (srcAt(nl) === '\r') nl++
                  if (srcAt(nl) === '\n') nl++
                  let spaces = 0
                  while (srcAt(nl + spaces) === ' ') spaces++
                  let nextCh = srcAt(nl + spaces)
                  if (nextCh !== undefined && nextCh !== '\n' && nextCh !== '\r' &&
                      spaces >= anchorIndent) {
                    pnt.sI = nl + spaces
                    pnt.cI = spaces
                    pnt.rI++
                  }
                }
                // Update fwd and continue matching.
                fwdAt = pnt.sI
                continue yamlMatchLoop
              }

              // YAML directive line (%YAML, %TAG, %FOO) at column 0: emit a
              // #DR token whose val is the raw directive text. The stream rule
              // applies the directive (e.g. %TAG handle registration) at parse
              // time via the @apply-directive action.
              if (F(0) === '%' && atLineStart(viewBase + pnt.sI)) {
                let pos = 0
                while (F(pos) !== undefined && F(pos) !== '\n' && F(pos) !== '\r') pos++
                let directiveSrc = FS(0, pos)
                pnt.sI += pos
                pnt.cI += pos
                let tkn = lex.token('#DR', directiveSrc, directiveSrc, lex.pnt)
                return tkn
              }

              // YAML non-specific tag (! value) or local tag (!name value):
              // skip the tag and let the value be parsed normally.
              if (F(0) === '!' && F(1) !== '!' && F(1) !== undefined) {
                if (F(1) === ' ') {
                  // Non-specific tag: ! value → treat value as string.
                  let valStart = 2
                  let valEnd = valStart
                  while (F(valEnd) !== undefined && F(valEnd) !== '\n' && F(valEnd) !== '\r') valEnd++
                  let rawVal = FS(valStart, valEnd).replace(/\s+$/, '')
                  let src = FS(0, valEnd)
                  let tkn = lex.token('#TX', rawVal, src, lex.pnt)
                  pnt.sI += valEnd
                  pnt.cI += valEnd
                  return tkn
                }
                // Local tag: !name value → skip the tag, continue with value.
                let tagEnd = 1
                while (F(tagEnd) !== undefined && F(tagEnd) !== ' ' && F(tagEnd) !== '\n' &&
                       F(tagEnd) !== '\r') tagEnd++
                if (F(tagEnd) === ' ') tagEnd++ // skip space after tag
                pnt.sI += tagEnd
                pnt.cI += tagEnd
                // If tag is standalone (followed by newline), consume the
                // newline and leading spaces so no extra #IN is emitted.
                if (srcAt(pnt.sI) === '\n' || srcAt(pnt.sI) === '\r') {
                  // Check if tag is standalone on its line.
                  let tagStandalone = true
                  let tagLineIndent = 0
                  let bi = viewBase + pnt.sI - tagEnd - 1
                  while (bi >= 0 && absAt(bi) !== '\n' && absAt(bi) !== '\r') {
                    if (absAt(bi) !== ' ' && absAt(bi) !== '\t') {
                      tagStandalone = false
                      break
                    }
                    tagLineIndent++
                    bi--
                  }
                  if (tagStandalone) {
                    let nl = pnt.sI
                    if (srcAt(nl) === '\r') nl++
                    if (srcAt(nl) === '\n') nl++
                    let spaces = 0
                    while (srcAt(nl + spaces) === ' ') spaces++
                    pnt.sI = nl + spaces
                    pnt.cI = spaces
                    pnt.rI++
                  }
                }
                fwdAt = pnt.sI
                // Restart matching to parse the value.
                continue yamlMatchLoop
              }

              // Skip !!seq, !!map, !!omap, !!set, !!binary, etc. tags — just
              // consume and return undefined so the next lex cycle handles the
              // actual structure/value. The pattern reads at most the run of
              // characters that are not white space after the `!!`, and the
              // one that ends it, which is all it is tried on.
              if (F(0) === '!' && F(1) === '!' && STRUCT_TAG.test(FS(0, structTagReach(F)))) {
                let skip = 2
                while (F(skip) !== undefined && F(skip) !== ' ' && F(skip) !== '\n') skip++
                while (F(skip) === ' ') skip++
                // Check if tag is standalone on its own line.
                let tagIndent = 0
                {
                  let bi = viewBase + pnt.sI - 1
                  let standalone = true
                  while (bi >= 0 && absAt(bi) !== '\n' && absAt(bi) !== '\r') {
                    if (absAt(bi) !== ' ' && absAt(bi) !== '\t') {
                      standalone = false
                      break
                    }
                    tagIndent++
                    bi--
                  }
                  // If standalone and next line is at the same indent, consume
                  // the newline so no extra IN token is emitted.
                  if (standalone && (F(skip) === '\n' || F(skip) === '\r')) {
                    let nl = skip
                    if (F(nl) === '\r') nl++
                    if (F(nl) === '\n') nl++
                    let spaces = 0
                    while (F(nl + spaces) === ' ') spaces++
                    if (spaces >= tagIndent) {
                      skip = nl + spaces
                      pnt.sI += skip
                      pnt.cI = spaces
                      pnt.rI++
                      if (srcAt(pnt.sI) === '[' || srcAt(pnt.sI) === '{') {
                        let oc = srcAt(pnt.sI)
                        let tkn = lex.token(oc === '[' ? '#OS' : '#OB', undefined, oc, pnt)
                        pnt.sI += 1
                        pnt.cI += 1
                        return tkn
                      }
                      fwdAt = pnt.sI
                      continue yamlMatchLoop
                    }
                  }
                }
                pnt.sI += skip
                pnt.cI += skip
                // Skip an inline comment after the tag (e.g. `!!map # note`),
                // so the `#` is not mis-lexed as #BD.
                {
                  let ci = pnt.sI
                  while (srcAt(ci) === ' ' || srcAt(ci) === '\t') ci++
                  if (srcAt(ci) === '#') {
                    while (srcAt(ci) !== undefined && srcAt(ci) !== '\n' && srcAt(ci) !== '\r') ci++
                    pnt.cI += (ci - pnt.sI)
                    pnt.sI = ci
                  }
                }
                // A tagged flow collection (!!map {..} / !!seq [..]): emit the
                // opener here — we've advanced past the tag, so jsonic would
                // otherwise mis-lex it as #BD (see anchor handler above).
                if (srcAt(pnt.sI) === '[' || srcAt(pnt.sI) === '{') {
                  let oc = srcAt(pnt.sI)
                  let tkn = lex.token(oc === '[' ? '#OS' : '#OB', undefined, oc, pnt)
                  pnt.sI += 1
                  pnt.cI += 1
                  return tkn
                }
                // Don't return a token — let the next lex cycle see the actual value.
                fwdAt = pnt.sI
                continue yamlMatchLoop
              }

              // Handle other !!type tags (!!str, !!int, !!float, !!bool, !!null).
              // These apply a type to the following value. For !!str, the value
              // is always a string. For others, convert accordingly.
              if (F(0) === '!' && F(1) === '!') {
                let tagEnd = 2
                while (F(tagEnd) !== undefined && F(tagEnd) !== ' ' && F(tagEnd) !== '\n' &&
                       F(tagEnd) !== '\r' && F(tagEnd) !== ',' &&
                       F(tagEnd) !== '}' && F(tagEnd) !== ']' &&
                       F(tagEnd) !== ':') tagEnd++
                let tag = FS(2, tagEnd)
                let valStart = tagEnd
                if (F(valStart) === ' ') valStart++
                let valEnd = valStart
                // Skip and record anchor (&name) if present before value.
                let tagAnchorName = ''
                if (F(valStart) === '&') {
                  let anchorEnd = valStart + 1
                  while (F(anchorEnd) !== undefined && F(anchorEnd) !== ' ' &&
                         F(anchorEnd) !== '\n' && F(anchorEnd) !== '\r') anchorEnd++
                  tagAnchorName = FS(valStart + 1, anchorEnd)
                  pendAnchor(tagAnchorName, true)
                  if (F(anchorEnd) === ' ') anchorEnd++
                  valStart = anchorEnd
                  valEnd = valStart
                }
                // Check for quoted value.
                if (F(valStart) === '"' || F(valStart) === "'") {
                  let q = F(valStart)
                  valEnd = valStart + 1
                  while (F(valEnd) !== undefined && F(valEnd) !== q) {
                    if (F(valEnd) === '\\' && q === '"') valEnd++
                    valEnd++
                  }
                  if (F(valEnd) === q) valEnd++
                  let rawVal = FS(valStart + 1, valEnd - 1)
                  let result: any = rawVal
                  if (!tagHandles['!!']) {
                    if (tag === 'int') result = yamlTagInt(rawVal)
                    else if (tag === 'float') result = yamlTagFloat(rawVal)
                    else if (tag === 'bool') result = rawVal === 'true' || rawVal === 'True' || rawVal === 'TRUE'
                    else if (tag === 'null') result = null
                  }
                  if (tagAnchorName) setAnchor(tagAnchorName, result)
                  let tknTin = typeof result === 'string' ? '#TX' :
                               typeof result === 'number' ? '#NR' : '#VL'
                  let tkn = lex.token(tknTin, result, FS(0, valEnd), lex.pnt)
                  pnt.sI += valEnd
                  pnt.cI += valEnd
                  return tkn
                }
                // If value is on next line (tag followed by newline with
                // indented content), skip the tag and let the next lex cycle
                // handle the value. If end-of-source or next line is not
                // indented content, fall through to produce default value.
                if ((F(valStart) === '\n' || F(valStart) === '\r') &&
                    F(valStart + 1) !== undefined) {
                  // Tag followed by newline — skip the tag and let the
                  // next lex cycle handle the value on the following line.
                  let nl = valStart
                  if (F(nl) === '\r') nl++
                  if (F(nl) === '\n') nl++
                  pnt.sI += nl
                  pnt.cI = 0
                  pnt.rI++
                  fwdAt = pnt.sI
                  continue yamlMatchLoop
                }
                // Unquoted: stop at `: `, ` #`, newline, flow indicators.
                // A `#` straight after the tag's space starts a comment, so
                // the value is empty (`!!str #c` is "").
                while (F(valEnd) !== undefined && F(valEnd) !== '\n' && F(valEnd) !== '\r' &&
                       F(valEnd) !== ',' && F(valEnd) !== '}' && F(valEnd) !== ']') {
                  if (valEnd === valStart && valStart > tagEnd && F(valEnd) === '#') break
                  if (F(valEnd) === ':' && (F(valEnd+1) === ' ' || F(valEnd+1) === '\n' ||
                      F(valEnd+1) === '\r' || F(valEnd+1) === undefined)) break
                  if (F(valEnd) === ' ' && F(valEnd+1) === '#') break
                  valEnd++
                }
                let rawVal = FS(valStart, valEnd).replace(/\s+$/, '')
                let result: any = rawVal
                // Only apply built-in type conversion when !! has not been
                // redefined by a %TAG directive. Custom tag handles mean
                // !!type is a user-defined tag, not a YAML core type.
                if (!tagHandles['!!']) {
                  if (tag === 'str') result = String(rawVal)
                  else if (tag === 'int') result = yamlTagInt(rawVal)
                  else if (tag === 'float') result = yamlTagFloat(rawVal)
                  else if (tag === 'bool') result = rawVal === 'true' || rawVal === 'True' || rawVal === 'TRUE'
                  else if (tag === 'null') result = null
                }
                if (tagAnchorName) setAnchor(tagAnchorName, result)
                // Use #ST for empty strings (jsonic handles #ST better than
                // empty #TX in flow context), #NR for numbers, #VL for null.
                let tknTin = (typeof result === 'string' && result === '') ? '#ST' :
                             typeof result === 'string' ? '#TX' :
                             typeof result === 'number' ? '#NR' : '#VL'
                let tkn = lex.token(tknTin, result, FS(0, valEnd), lex.pnt)
                pnt.sI += valEnd
                pnt.cI += valEnd
                return tkn
              }

              // Flow-context `?` explicit-key marker: emit a #QM token so
              // pair/elem rule alts can handle it. Block-context `?` falls
              // through to the heavyweight handler below.
              if (F(0) === '?' && (F(1) === ' ' || F(1) === '\t')) {
                updateFlowState(viewBase + pnt.sI)
                if (_flowDepth > 0) {
                  let tkn = lex.token('#QM', undefined, '?', lex.pnt)
                  pnt.sI += 1; pnt.cI += 1
                  return tkn
                }
              }

              // YAML explicit key indicator: ? key\n: value
              // Handles: ? key (with null value if no : follows)
              //          ? key\n: value
              //          ? key\n# comment\n: value
              //          ? key1\n? key2 (consecutive explicit keys with null values)
              if (F(0) === '?' && (F(1) === ' ' || F(1) === '\t' ||
                  F(1) === '\n' || F(1) === '\r' || F(1) === undefined)) {
                let start = (F(1) === ' ' || F(1) === '\t') ? 2 : 1
                // Collect key text (may be multiline via continuation).
                let keyEnd = start
                let key = ''
                // First line of key.
                while (F(keyEnd) !== undefined && F(keyEnd) !== '\n' && F(keyEnd) !== '\r') {
                  if (F(keyEnd) === ' ' && F(keyEnd+1) === '#') break  // comment
                  keyEnd++
                }
                key = FS(start, keyEnd).replace(/\s+$/, '')
                // Strip !!type tags from explicit keys and apply conversion.
                let explicitKeyTag = ''
                let tagMatch = key.match(/^!!(\w+)\s+(.*)$/)
                if (tagMatch) {
                  explicitKeyTag = tagMatch[1]
                  key = tagMatch[2]
                }
                // A quoted explicit key is the scalar inside the quotes, not
                // the quoted text (`? "k"` is the key k), and its token is a
                // string's, as a quoted implicit key's is, so that a quoted
                // `<<` is no merge key here either.
                let rawKey = key
                key = unquoteWholeScalar(key)
                let quotedKey = key !== rawKey
                let consumed = keyEnd
                // Track position before consuming newline (for !hasValue case).
                let beforeNewline = consumed
                // Skip comment at end of key line.
                while (F(consumed) !== undefined && F(consumed) !== '\n' && F(consumed) !== '\r') consumed++
                beforeNewline = consumed
                // Consume newline after key line.
                if (F(consumed) === '\r') consumed++
                if (F(consumed) === '\n') consumed++
                // Check for multiline key (continuation lines indented more than ?).
                let qIndent = 0
                {
                  const here = viewBase + pnt.sI
                  let li = lineStartOf(here)
                  while (li < here && absAt(li) === ' ') { qIndent++; li++ }
                }
                // Count extra rows consumed (for multiline keys).
                let extraRows = 0

                // Handle block scalar keys (| or >).
                let blockScalarMatch = key.match(/^([|>])([+-]?)([0-9]?)$/)
                if (blockScalarMatch) {
                  let isFolded = blockScalarMatch[1] === '>'
                  let chomp = blockScalarMatch[2] || ''
                  let explicitIndent = blockScalarMatch[3] ? parseInt(blockScalarMatch[3]) : 0
                  // Collect block scalar content lines.
                  let blockLines: string[] = []
                  let contentIndent = 0
                  while (F(consumed) !== undefined) {
                    let lineIndent = 0
                    while (F(consumed + lineIndent) === ' ') lineIndent++
                    let afterSpaces = consumed + lineIndent
                    // Empty line or line with only spaces.
                    if (F(afterSpaces) === undefined || F(afterSpaces) === '\n' || F(afterSpaces) === '\r') {
                      blockLines.push('')
                      consumed = afterSpaces
                      if (F(consumed) === '\r') consumed++
                      if (F(consumed) === '\n') consumed++
                      extraRows++
                      continue
                    }
                    // Determine content indent from first non-empty line.
                    if (contentIndent === 0) {
                      contentIndent = explicitIndent > 0 ? qIndent + explicitIndent : lineIndent
                    }
                    // Line must be indented more than ? to be content.
                    if (lineIndent < contentIndent) break
                    // Collect line content.
                    let lineEnd = afterSpaces
                    while (F(lineEnd) !== undefined && F(lineEnd) !== '\n' && F(lineEnd) !== '\r') lineEnd++
                    blockLines.push(FS(consumed + contentIndent, lineEnd))
                    consumed = lineEnd
                    if (F(consumed) === '\r') consumed++
                    if (F(consumed) === '\n') consumed++
                    extraRows++
                  }
                  // Apply chomping.
                  // Remove trailing empty lines for non-keep.
                  if (chomp !== '+') {
                    while (blockLines.length > 0 && blockLines[blockLines.length - 1] === '') blockLines.pop()
                  }
                  if (isFolded) {
                    key = blockLines.join(' ') + '\n'
                  } else {
                    key = blockLines.join('\n') + '\n'
                  }
                  if (chomp === '-') {
                    key = key.replace(/\n$/, '')
                  }
                } else {
                // Scan continuation lines for key (plain scalar multiline).
                while (F(consumed) !== undefined) {
                  // Skip comment lines.
                  let lineIndent = 0
                  while (F(consumed + lineIndent) === ' ') lineIndent++
                  let afterSpaces = consumed + lineIndent
                  if (F(afterSpaces) === '#') {
                    // Comment line — skip it.
                    while (F(afterSpaces) !== undefined && F(afterSpaces) !== '\n' && F(afterSpaces) !== '\r') afterSpaces++
                    beforeNewline = afterSpaces
                    if (F(afterSpaces) === '\r') afterSpaces++
                    if (F(afterSpaces) === '\n') afterSpaces++
                    extraRows++
                    consumed = afterSpaces
                    continue
                  }
                  // Check if this is a continuation of the key (indented more than ?).
                  if (lineIndent > qIndent && F(afterSpaces) !== ':' &&
                      F(afterSpaces) !== '?' && F(afterSpaces) !== '-') {
                    // Continuation line for multiline key.
                    let contEnd = afterSpaces
                    while (F(contEnd) !== undefined && F(contEnd) !== '\n' && F(contEnd) !== '\r') {
                      if (F(contEnd) === ' ' && F(contEnd+1) === '#') break
                      contEnd++
                    }
                    let contText = FS(afterSpaces, contEnd).replace(/\s+$/, '')
                    if (contText.length > 0) {
                      key += ' ' + contText
                    }
                    consumed = contEnd
                    beforeNewline = consumed
                    if (F(consumed) === '\r') consumed++
                    if (F(consumed) === '\n') consumed++
                    extraRows++
                    continue
                  }
                  break
                }
                }
                // Now check if the next non-comment line starts with `:`.
                let hasValue = false
                let valConsumed = consumed
                {
                  let ci = consumed
                  // Skip leading spaces on the next line.
                  while (F(ci) === ' ') ci++
                  if (F(ci) === ':' &&
                      (F(ci+1) === ' ' || F(ci+1) === '\t' || F(ci+1) === '\n' ||
                       F(ci+1) === '\r' || F(ci+1) === undefined)) {
                    // Found `: ` — this key has a value.
                    hasValue = true
                    valConsumed = ci + 1
                    if (F(valConsumed) === ' ' || F(valConsumed) === '\t') valConsumed++
                  }
                }
                let src = FS(0, hasValue ? consumed : keyEnd)
                if (hasValue) {
                  pnt.sI += valConsumed
                  pnt.rI += 1 + extraRows
                  let indent = valConsumed - consumed
                  pnt.cI = indent + 1
                  // Check if there's inline content after `: ` on the same line
                  // that looks like a block mapping or sequence (needs #IN context).
                  let nextCh = F(valConsumed)
                  let hasInlineContent = nextCh !== undefined &&
                    nextCh !== '\n' && nextCh !== '\r'
                  let needsIndent = false
                  if (hasInlineContent) {
                    let isQuotedOrFlowOrTag = nextCh === '"' || nextCh === "'" ||
                      nextCh === '[' || nextCh === '{' || nextCh === '!'
                    if (!isQuotedOrFlowOrTag) {
                      // Scan line for mapping key indicator (`: ` or `:` at EOL).
                      let le = valConsumed
                      while (F(le) !== undefined && F(le) !== '\n' && F(le) !== '\r') le++
                      for (let ri = valConsumed; ri < le; ri++) {
                        if (F(ri) === ':') {
                          let nc = F(ri + 1)
                          if (nc === ' ' || nc === '\t' || nc === '\n' ||
                              nc === '\r' || nc === undefined || ri + 1 === le) {
                            needsIndent = true
                            break
                          }
                        }
                      }
                      // Also check for sequence indicator (`- `).
                      if (!needsIndent && nextCh === '-' &&
                          (F(valConsumed + 1) === ' ' || F(valConsumed + 1) === '\t')) {
                        needsIndent = true
                      }
                    }
                  }
                  if (needsIndent) {
                    // Block mapping/sequence inline (e.g., `: get:\n    v: 1`).
                    // Emit CL then IN to establish indent context. They are
                    // the lexer's to hand over next, so that a cut made again
                    // makes them again rather than twice.
                    let clTkn = lex.token('#CL', 1, ': ', lex.pnt)
                    let inTkn = lex.token('#IN', indent, '', lex.pnt)
                    lex.pnt.token.push(clTkn, inTkn)
                  } else {
                    // Simple scalar or value on next line.
                    // Just emit CL; the newline handler will emit IN if needed.
                    pendingExplicitCL = true
                  }
                } else {
                  // No `:` follows — don't consume past newline so the
                  // normal newline→#IN handler can emit indent for map continuation.
                  pnt.sI += beforeNewline
                  pnt.cI += beforeNewline
                  // Emit KEY, CL, null as queued tokens.
                  let clTkn = lex.token('#CL', 1, ': ', lex.pnt)
                  let vlTkn = lex.token('#VL', null, '', lex.pnt)
                  lex.pnt.token.push(clTkn, vlTkn)
                }
                let tkn = lex.token(quotedKey ? '#ST' : '#TX', key, src, lex.pnt)
                return tkn
              }

              // YAML document-frame markers at column 0:
              //   --- → emit #DS (document start)
              //   ... → emit #DE (document end)
              // The handler also consumes trailing whitespace, optional `#`
              // comment, and the newline ending the marker line — so the next
              // matcher call lands directly on the next document's content
              // (no spurious #IN gets emitted between #DS and the content).
              // Inline content on the same line as the marker (--- foo) is
              // left in place for the next call.
              if (((F(0) === '-' && F(1) === '-' && F(2) === '-' &&
                    (F(3) === '\n' || F(3) === '\r' ||
                     F(3) === ' ' || F(3) === '\t' || F(3) === undefined)) ||
                   (F(0) === '.' && F(1) === '.' && F(2) === '.' &&
                    (F(3) === '\n' || F(3) === '\r' ||
                     F(3) === ' ' || F(3) === '\t' || F(3) === undefined))) &&
                  atLineStart(viewBase + pnt.sI)) {
                let isEnd = F(0) === '.'
                let pos = 3
                while (F(pos) === ' ' || F(pos) === '\t') pos++
                let hasInline = F(pos) !== undefined &&
                  F(pos) !== '\n' && F(pos) !== '\r' && F(pos) !== '#'
                if (!hasInline) {
                  // Skip a trailing comment, then the line terminator.
                  while (F(pos) !== undefined && F(pos) !== '\n' && F(pos) !== '\r') pos++
                  if (F(pos) === '\r') pos++
                  if (F(pos) === '\n') { pos++; pnt.rI++ }
                  pnt.cI = 1  // column 1 at start of next line
                } else {
                  pnt.cI += pos
                }
                pnt.sI += pos
                let tkn = lex.token(isEnd ? '#DE' : '#DS', undefined,
                                    FS(0, 3), lex.pnt)
                return tkn
              }

              // Non-specific tag.
              if (F(0) === '!' && F(1) === ' ') {
                let valStart = 2
                let valEnd = valStart
                while (F(valEnd) !== undefined && F(valEnd) !== '\n' && F(valEnd) !== '\r') valEnd++
                let rawVal = FS(valStart, valEnd).replace(/\s+$/, '')
                let src = FS(0, valEnd)
                let tkn = lex.token('#TX', rawVal, src, lex.pnt)
                pnt.sI += valEnd
                pnt.cI += valEnd
                return tkn
              }
              // Anchor after ---.
              if (F(0) === '&') {
                let nameEnd = 1
                while (F(nameEnd) !== undefined && F(nameEnd) !== ' ' && F(nameEnd) !== '\t' &&
                       F(nameEnd) !== '\n' && F(nameEnd) !== '\r' && F(nameEnd) !== ',' &&
                       F(nameEnd) !== '{' && F(nameEnd) !== '}' && F(nameEnd) !== '[' &&
                       F(nameEnd) !== ']') nameEnd++
                let anchorName = FS(1, nameEnd)
                let skip = nameEnd
                if (F(skip) === ' ') skip++
                pnt.sI += skip
                pnt.cI += skip
                pendAnchor(anchorName, true)
                fwdAt = pnt.sI
              }

              // YAML double-quoted string: backslash escapes + multiline folding.
              if (F(0) === '"') {
                let i = 1
                let val = ''
                let escapedUpTo = 0 // val chars up to this index are from escapes (non-trimmable)
                while (F(i) !== undefined && F(i) !== '"') {
                  if (F(i) === '\\') {
                    i++
                    let esc = F(i)
                    if (esc === 'n') { val += '\n'; i++; escapedUpTo = val.length }
                    else if (esc === 't') { val += '\t'; i++; escapedUpTo = val.length }
                    else if (esc === 'r') { val += '\r'; i++; escapedUpTo = val.length }
                    else if (esc === '"') { val += '"'; i++; escapedUpTo = val.length }
                    else if (esc === '\\') { val += '\\'; i++; escapedUpTo = val.length }
                    else if (esc === '/') { val += '/'; i++; escapedUpTo = val.length }
                    else if (esc === 'b') { val += '\b'; i++; escapedUpTo = val.length }
                    else if (esc === 'f') { val += '\f'; i++; escapedUpTo = val.length }
                    else if (esc === 'a') { val += '\x07'; i++; escapedUpTo = val.length }
                    else if (esc === 'e') { val += '\x1b'; i++; escapedUpTo = val.length }
                    else if (esc === 'v') { val += '\v'; i++; escapedUpTo = val.length }
                    else if (esc === '0') { val += '\0'; i++; escapedUpTo = val.length }
                    else if (esc === '\t') { val += '\t'; i++; escapedUpTo = val.length }
                    else if (esc === ' ') { val += ' '; i++; escapedUpTo = val.length }
                    else if (esc === '_') { val += ' '; i++; escapedUpTo = val.length }
                    else if (esc === 'N') { val += '\u0085'; i++; escapedUpTo = val.length }
                    else if (esc === 'L') { val += ' '; i++; escapedUpTo = val.length }
                    else if (esc === 'P') { val += ' '; i++; escapedUpTo = val.length }
                    else if (esc === 'x') {
                      val += String.fromCharCode(parseInt(FS(i+1, i+3), 16))
                      i += 3; escapedUpTo = val.length
                    }
                    else if (esc === 'u') {
                      val += String.fromCharCode(parseInt(FS(i+1, i+5), 16))
                      i += 5; escapedUpTo = val.length
                    }
                    else if (esc === 'U') {
                      val += String.fromCodePoint(parseInt(FS(i+1, i+9), 16))
                      i += 9; escapedUpTo = val.length
                    }
                    else if (esc === '\n' || esc === '\r') {
                      // Escaped newline: line continuation (join directly).
                      if (esc === '\r' && F(i+1) === '\n') i++
                      i++
                      // Skip leading whitespace on next line.
                      while (F(i) === ' ' || F(i) === '\t') i++
                    }
                    else if (undefined === esc) {
                      // A backslash as the last character of the source: there
                      // is nothing to escape, and `val += fwd[i]` would append
                      // the nine letters of `undefined`, text the input never
                      // held. The scan ends with the value it has.
                      i++
                    }
                    else { val += esc; i++ }
                  } else if (F(i) === '\n' || F(i) === '\r') {
                    // Flow scalar line folding for double-quoted strings.
                    // Only trim trailing whitespace that was NOT from escape sequences.
                    let trimTo = val.length
                    while (trimTo > escapedUpTo && (val[trimTo - 1] === ' ' || val[trimTo - 1] === '\t')) trimTo--
                    val = val.substring(0, trimTo)
                    let emptyLines = 0
                    while (F(i) === '\n' || F(i) === '\r') {
                      if (F(i) === '\r') i++
                      if (F(i) === '\n') i++
                      emptyLines++
                      while (F(i) === ' ' || F(i) === '\t') i++
                    }
                    if (emptyLines > 1) {
                      for (let e = 1; e < emptyLines; e++) val += '\n'
                    } else {
                      val += ' '
                    }
                  } else {
                    val += F(i)
                    i++
                  }
                }
                if (F(i) === '"') i++
                let src = FS(0, i)
                let tkn = lex.token('#ST', val, src, lex.pnt)
                pnt.sI += i
                pnt.cI += i
                return tkn
              }

              // YAML single-quoted string: no backslash escape processing.
              // Only escape is '' (two single quotes) → literal single quote.
              // Newlines are folded: single newline → space, empty lines → \n.
              if (F(0) === "'") {
                let i = 1
                let val = ''
                while (F(i) !== undefined) {
                  if (F(i) === "'") {
                    if (F(i + 1) === "'") {
                      // Escaped single quote.
                      val += "'"
                      i += 2
                    } else {
                      // End of string.
                      i++
                      break
                    }
                  } else if (F(i) === '\n' || F(i) === '\r') {
                    // Flow scalar line folding.
                    // Trim trailing whitespace from current content: from
                    // its end, as `/[ \t]+$/` would, which tries every
                    // blank in the value and so reads it all at each fold.
                    let trimTo = val.length
                    while (0 < trimTo && (' ' === val[trimTo - 1] || '\t' === val[trimTo - 1])) trimTo--
                    if (trimTo < val.length) val = val.substring(0, trimTo)
                    // Count empty lines (newlines with only whitespace).
                    let emptyLines = 0
                    while (F(i) === '\n' || F(i) === '\r') {
                      if (F(i) === '\r') i++
                      if (F(i) === '\n') i++
                      emptyLines++
                      // Skip leading whitespace on next line.
                      while (F(i) === ' ' || F(i) === '\t') i++
                    }
                    if (emptyLines > 1) {
                      // Each extra empty line becomes a \n.
                      for (let e = 1; e < emptyLines; e++) val += '\n'
                    } else {
                      // Single newline → space (folding).
                      val += ' '
                    }
                  } else {
                    val += F(i)
                    i++
                  }
                }
                let src = FS(0, i)
                let tkn = lex.token('#ST', val, src, lex.pnt)
                pnt.sI += i
                pnt.cI += i
                return tkn
              }

              // Plain scalars starting with digits but containing colons (e.g. 20:03:20),
              // trailing commas (e.g. 12,), or non-numeric text after a space
              // (e.g. "64 characters, hexadecimal.") must be captured before
              // jsonic's number matcher grabs just the digits.
              // A sign before the digit counts too: `+190:20:30` is one plain
              // scalar, where the number matcher would take `+190`.
              if ((F(0) >= '0' && F(0) <= '9') ||
                  ((F(0) === '+' || F(0) === '-') && F(1) >= '0' && F(1) <= '9')) {
                updateFlowState(viewBase + pnt.sI)
                let inFlow = _flowDepth > 0
                let hasEmbeddedColon = false
                let hasTrailingText = false
                let hasBlockComma = false
                let pi = 1
                while (F(pi) !== undefined && F(pi) !== '\n' && F(pi) !== '\r') {
                  // An embedded colon (20:03:20) does not end the scan: the
                  // scalar can go on past a space ("09:00 AM"), and only the
                  // trailing-text branch below reads that as one scalar.
                  // Stopping here took "09:00" and left "AM" to fail the
                  // parse (tabnas/yaml#90).
                  if (F(pi) === ':' && F(pi + 1) !== ' ' && F(pi + 1) !== '\t' &&
                      F(pi + 1) !== '\n' && F(pi + 1) !== '\r' && F(pi + 1) !== undefined) {
                    hasEmbeddedColon = true
                    pi++
                    continue
                  }
                  // A COMMA IS NOT A SEPARATOR IN BLOCK CONTEXT.
                  //
                  // Flow indicators only indicate inside a flow collection, so
                  // in block context `example: 1,2,3` is the plain scalar
                  // "1,2,3" — not a number, a separator, and two more numbers.
                  //
                  // This used to accept only a comma at END of line ("12,"),
                  // which left `1,2,3` to the number matcher: it took the `1`,
                  // the `,` became a structural token, and the parse died on
                  // the `2` with "unexpected character(s)". Intercom's OpenAPI
                  // document carries `example: 1,2,3` and failed to parse at
                  // all because of it.
                  //
                  // The scan CONTINUES rather than breaking, so a scalar that
                  // also has a space with text after it ("12, hexadecimal")
                  // still reaches the trailing-text branch below, which handles
                  // spaces and multiline continuation that the token scan here
                  // cannot.
                  if (F(pi) === ',') {
                    if (inFlow) {
                      break
                    }
                    hasBlockComma = true
                    pi++
                    continue
                  }
                  if (F(pi) === ' ' || F(pi) === '\t') {
                    // Check if after the space there are non-separator characters,
                    // meaning this is a plain scalar like "64 characters, hexadecimal."
                    // not a standalone number.
                    let si = pi
                    while (F(si) === ' ' || F(si) === '\t') si++
                    if (F(si) !== undefined && F(si) !== '\n' && F(si) !== '\r' &&
                        F(si) !== '#' && F(si) !== ':') {
                      // Check it's not ": " (key-value separator).
                      if (!(F(si) === ':' && (F(si + 1) === ' ' || F(si + 1) === '\t' ||
                            F(si + 1) === '\n' || F(si + 1) === '\r' || F(si + 1) === undefined))) {
                        hasTrailingText = true
                      }
                    }
                    break
                  }
                  pi++
                }
                // TRAILING TEXT FIRST. A scalar can be both ("12, hexadecimal"),
                // and the token scan below stops at the first space, which
                // would truncate it to "12,". The text handler takes the whole
                // scalar, continuation lines included.
                if (hasTrailingText) {
                  // Flag that the number matcher should skip this value,
                  // so the text.check handler can process it as a plain
                  // scalar (including multiline continuation support).
                  skipNumberMatch = true
                  return null as any
                }

                // A `#` inside the scalar's token follows a non-blank
                // character, and YAML starts a comment only at a `#` after
                // white space, so `80#` is the plain scalar "80#". Left to
                // the number matcher it read 80, and the rest of the line
                // went as a comment (tabnas/yaml#95). The text handler reads
                // it whole, as it reads `foo#bar`, and as for trailing text it
                // takes continuation lines and stops at a mapping colon
                // (`80#:`). The token ends at a blank, or in a flow collection
                // at its `,` `]` `}`, so a `#` past the collection's close
                // keeps the reading it had.
                //
                // Outside a flow collection `[` `]` `{` `}` indicate nothing
                // either, so `5[` and `12[x]` are plain scalars too. The
                // number matcher took the digits and the parse failed on the
                // bracket (tabnas/yaml#99).
                let tokenEnd = 0
                while (F(tokenEnd) !== undefined && F(tokenEnd) !== ' ' && F(tokenEnd) !== '\t' &&
                       F(tokenEnd) !== '\n' && F(tokenEnd) !== '\r' &&
                       !(inFlow && (F(tokenEnd) === ',' || F(tokenEnd) === ']' ||
                         F(tokenEnd) === '}'))) tokenEnd++
                const token = FS(0, tokenEnd)
                if (token.includes('#') || (!inFlow && /[[\]{}]/.test(token))) {
                  skipNumberMatch = true
                  return null as any
                }

                if (hasEmbeddedColon || hasBlockComma) {
                  // Scan to end of plain scalar token (space, tab, newline,
                  // eof, or in a flow collection its `,` `]` `}`: `[1, 12:30]`
                  // holds "12:30", not "12:30,").
                  let end = 0
                  while (F(end) !== undefined && F(end) !== ' ' && F(end) !== '\t' &&
                         F(end) !== '\n' && F(end) !== '\r' &&
                         !(inFlow && (F(end) === ',' || F(end) === ']' || F(end) === '}'))) end++
                  let text = FS(0, end)
                  let tkn = lex.token('#TX', text, text, lex.pnt)
                  pnt.sI += end
                  pnt.cI += end
                  return tkn
                }
              }

              // YAML element marker: "- " or "-\t" or "-\n" or "-" at end.
              if (F(0) === '-' && (F(1) === ' ' || F(1) === '\t' || F(1) === '\n' ||
                  F(1) === '\r' || F(1) === undefined)) {
                let tkn = lex.token('#EL', undefined, '- ', lex.pnt)
                pnt.sI += 1
                pnt.cI += 1
                // Consume the space/tab after dash if present.
                if (F(1) === ' ' || F(1) === '\t') {
                  pnt.sI += 1
                  pnt.cI += 1
                }
                return tkn
              }

              // Yaml colons are ': ', ':\t', ':<newline>', ':' at end of input,
              // or ':' in flow context (JSON-compatible, e.g. {"key":value}).
              // In flow context, detect by checking if the previous non-whitespace
              // token was a quoted string followed immediately by ':'.
              let isFlowColon = false
              if (F(0) === ':' && F(1) !== ' ' && F(1) !== '\t' &&
                  F(1) !== '\n' && F(1) !== '\r' && F(1) !== undefined) {
                // JSON-compatible flow colon: only when preceded by a quoted
                // string. Whitespace and line comments (`# ...` to the end of
                // a line, a `#` inside a quoted scalar being literal) between
                // are passed over, so e.g. `"foo" # c\n :bar` works: the last
                // character before the colon that is neither, which `hist`
                // keeps as the text goes by (`jSig`), however far back it is.
                histSync(viewBase + pnt.sI)
                if (34 === jSig || 39 === jSig) {
                  isFlowColon = true
                }
              }
              if (F(0) === ':' && (F(1) === ' ' || F(1) === '\t' || F(1) === '\n' ||
                  F(1) === '\r' || F(1) === undefined || isFlowColon)) {
                let tkn = lex.token('#CL', 1, ': ', lex.pnt)
                pnt.sI += 1
                if (F(1) === ' ' || F(1) === '\t') {
                  pnt.cI += 2
                } else if (F(1) === '\n' || F(1) === '\r') {
                  // Don't consume newline — leave for #IN.
                } else {
                  // End of input after colon.
                  pnt.cI += 1
                }
                return tkn
              }

              // Match any newline — YAML indentation is significant.
              // In flow context, newlines are just whitespace — don't emit #IN.
              if (F(0) === '\n' || F(0) === '\r') {
                updateFlowState(viewBase + pnt.sI)
                if (_flowDepth > 0) {
                  // Inside flow collection — consume whitespace, don't emit #IN.
                  // Track rows/last-newline so the point stays consistent: the
                  // next token must land on a valid 1-based column, else jsonic
                  // rejects it (a close ] at cI=0 lexes as #BD, not #CS).
                  let pos = 0
                  let rows = 0
                  let lastNL = -1
                  while (F(pos) === '\n' || F(pos) === '\r' || F(pos) === ' ' || F(pos) === '\t') {
                    if (F(pos) === '\n') { rows++; lastNL = pos }
                    pos++
                  }
                  // Also skip comment lines inside flow collections.
                  if (F(pos) === '#') {
                    while (F(pos) !== undefined && F(pos) !== '\n' && F(pos) !== '\r') pos++
                  }
                  // If the whitespace is immediately followed by flow
                  // punctuation ([ ] { } ,), emit that token here. We've
                  // already advanced past the newline, so handing back to
                  // jsonic's fixed-token matcher would mis-lex it as #BD (it
                  // never gets a clean shot at the advanced position). The
                  // yaml matcher itself does not otherwise produce these.
                  let nextCh = F(pos)
                  let punctTin = nextCh === '[' ? '#OS'
                    : nextCh === '{' ? '#OB'
                    : nextCh === ']' ? '#CS'
                    : nextCh === '}' ? '#CB'
                    : nextCh === ',' ? '#CA' : ''
                  pnt.sI += pos
                  pnt.rI += rows
                  // Column of the next char: chars consumed since the last
                  // newline (1-based). If no newline was consumed (only spaces
                  // after a bare \r), advance the existing column instead.
                  pnt.cI = lastNL >= 0 ? pos - lastNL : pnt.cI + pos
                  if (punctTin) {
                    let tkn = lex.token(punctTin, undefined, nextCh, pnt)
                    pnt.sI += 1
                    pnt.cI += 1
                    return tkn
                  }
                  // Re-run yamlMatcher from new position.
                  fwdAt = pnt.sI
                  continue yamlMatchLoop
                }
              }
              // Must catch all newlines before the default line/space matchers.
              if (F(0) === '\n' || F(0) === '\r') {
                // Consume all blank lines and comment-only lines,
                // finding the last meaningful indent.
                let pos = 0
                let spaces = 0
                let rows = 0
                while (F(pos) !== undefined) {
                  // Match \r\n or \n
                  if (F(pos) === '\r' && F(pos + 1) === '\n') {
                    pos += 2
                    rows++
                  } else if (F(pos) === '\n') {
                    pos += 1
                    rows++
                  } else {
                    break
                  }
                  // Count spaces after this newline.
                  spaces = 0
                  while (F(pos) === ' ') {
                    pos++
                    spaces++
                  }
                  // If the line is a comment-only line, consume it too.
                  if (F(pos) === '#') {
                    while (F(pos) !== undefined && F(pos) !== '\n' && F(pos) !== '\r') pos++
                    continue
                  }
                  // If the line is whitespace-only (tabs and/or spaces),
                  // treat it as a blank line and continue.
                  if (F(pos) === '\t') {
                    let tp = pos
                    while (F(tp) === ' ' || F(tp) === '\t') tp++
                    if (F(tp) === undefined || F(tp) === '\n' || F(tp) === '\r') {
                      pos = tp
                      continue
                    }
                  }
                  // If the line is an anchor-only line (&name with nothing after),
                  // consume it (record the anchor) and continue to the next line
                  // so the indent is determined by actual content.
                  if (F(pos) === '&') {
                    let ae = pos + 1
                    while (F(ae) !== undefined && F(ae) !== ' ' && F(ae) !== '\t' &&
                           F(ae) !== '\n' && F(ae) !== '\r') ae++
                    let afterAnchor = ae
                    while (F(afterAnchor) === ' ' || F(afterAnchor) === '\t') afterAnchor++
                    if (F(afterAnchor) === undefined || F(afterAnchor) === '\n' ||
                        F(afterAnchor) === '\r' || F(afterAnchor) === '#') {
                      pendAnchor(FS(pos + 1, ae), false)
                      // Skip to end of line (including any comment).
                      while (F(afterAnchor) !== undefined &&
                             F(afterAnchor) !== '\n' && F(afterAnchor) !== '\r') afterAnchor++
                      pos = afterAnchor
                      continue
                    }
                  }
                }

                // If we consumed everything (trailing newlines), advance and emit #ZZ.
                if (F(pos) === undefined) {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = spaces + 1
                  let tkn = lex.token('#ZZ', undefined, '', lex.pnt)
                  pnt.end = tkn
                  return tkn
                }

                // If the next line is a document-frame marker (--- / ...) at
                // column 0, advance to it without emitting #IN. The next
                // matcher call will emit the corresponding #DS / #DE token.
                if (spaces === 0 &&
                    ((F(pos) === '-' && F(pos+1) === '-' && F(pos+2) === '-' &&
                      (F(pos+3) === '\n' || F(pos+3) === '\r' ||
                       F(pos+3) === ' ' || F(pos+3) === '\t' ||
                       F(pos+3) === undefined)) ||
                     (F(pos) === '.' && F(pos+1) === '.' && F(pos+2) === '.' &&
                      (F(pos+3) === '\n' || F(pos+3) === '\r' ||
                       F(pos+3) === ' ' || F(pos+3) === '\t' ||
                       F(pos+3) === undefined)))) {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = 0
                  fwdAt = pnt.sI
                  continue yamlMatchLoop
                }

                // Likewise if the next line is a directive (%YAML / %TAG):
                // advance and let the next call emit #DR.
                if (spaces === 0 && F(pos) === '%') {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = 0
                  fwdAt = pnt.sI
                  continue yamlMatchLoop
                }

                // Skip #IN when the next content is a flow indicator or quoted
                // string at column 0 — there's no block to indent into. Match
                // the previous behavior of the inline `--- foo` handler.
                // A quoted KEY at column 0 is different: it continues (or
                // closes back to) the root block mapping, so it needs its #IN
                // like a plain key does. Without it a block sequence ending
                // just before it swallowed the pair (tabnas/yaml#86).
                if (spaces === 0 &&
                    (F(pos) === '{' || F(pos) === '[' ||
                     ((F(pos) === '"' || F(pos) === "'") &&
                      !quotedKeyAt(viewBase + fwdAt + pos)))) {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = 0
                  fwdAt = pnt.sI
                  continue yamlMatchLoop
                }

                // Emit #IN with val = indent level of the last non-blank line.
                let src = FS(0, pos)
                let tkn = lex.token('#IN', spaces, src, lex.pnt)
                pnt.sI += pos
                pnt.rI += rows
                pnt.cI = spaces + 1
                return tkn
              }

              break // End of yamlMatchLoop
              } // end while(true) yamlMatchLoop
              return undefined
  }


  // Extract a key value from a token, resolving aliases.
  function extractKey(rule: Rule, tkn: Token = rule.o0): any {
    if (VL === tkn.tin && tkn.val && typeof tkn.val === 'object' && tkn.val.__yamlAlias) {
      // Alias used as key — resolve to anchor value.
      let name = tkn.val.__yamlAlias
      return anchors[name] !== undefined ? anchors[name] : '*' + name
    }
    return ST === tkn.tin || TX === tkn.tin ? tkn.val : tkn.src
  }

  // The column the mapping of a block sequence's entry starts at, on the
  // line of the source index `at`: past the line's indentation and every
  // `- ` that leads it, as a block scalar's explicit indent counts its
  // key's column above. `- a: 1` starts at 2, `-   a: 1` and `- - a: 1`
  // at 4, and `- &x a: 1` at 2, where the anchor starts the mapping ahead
  // of its first key's own token.
  // The source is read as the lexer `lex` holds it, with `hist` behind its
  // window: the action runs on the token the lexer cut last but one or so,
  // whose line `hist` keeps.
  function entryColumn(lex: Lex, at: number): number {
    viewOf(lex)
    let li = lineStartOf(at)
    if (0 === li && 0xFEFF === absCode(0)) li = 1
    let col = 0
    let i = li
    while (i < at && ' ' === absAt(i)) { col++; i++ }
    while (i < at && '-' === absAt(i) && (' ' === absAt(i + 1) || '\t' === absAt(i + 1))) {
      col += 2
      i += 2
      while (i < at && ' ' === absAt(i)) { col++; i++ }
    }
    return col
  }

  // The column of the sequence entry's mapping whose pair a value is, when
  // it is the value of one (`- a: ...`): the mapping's keys stand at that
  // column, so the value nests only below it, as a block mapping's value
  // nests only below its key.
  function entryMapIn(rule: Rule): number | undefined {
    return 'yamlElemPair' === rule.parent?.name ? rule.k.yamlMapIn : undefined
  }

  // Function refs used by the declarative grammar (yaml-grammar.jsonic).
  const refs: Record<string, Function> = {
    '@val-indent-deeper': (rule: Rule, ctx: Context) => {
      let parentIn = rule.k.yamlIn
      let listIn = rule.k.yamlListIn
      let mapIn = entryMapIn(rule)
      if (listIn != null && ctx.t0.val <= listIn) return false
      if (mapIn != null && ctx.t0.val <= mapIn) return false
      return parentIn == null || ctx.t0.val > parentIn
    },
    // A sequence at the indent of the key it is the value of, its mapping's
    // or a sequence entry's: `a:\n- x` and `- a:\n  - x`.
    '@val-indent-eq-parent': (rule: Rule, ctx: Context) => {
      let parentIn = rule.k.yamlIn
      let mapIn = entryMapIn(rule)
      return (parentIn != null && ctx.t0.val === parentIn) ||
        (mapIn != null && ctx.t0.val === mapIn)
    },
    '@val-set-in-from-o0': (rule: Rule) => { rule.n.in = rule.o0.val },
    '@val-set-null': (rule: Rule) => { rule.node = null },
    '@val-set-el-in': (rule: Rule) => { rule.n.in = rule.o0.cI - 1 },
    '@indent-plain-value': (rule: Rule) => {
      rule.node = ST === rule.o0.tin || TX === rule.o0.tin
        ? rule.o0.val : rule.o0.src
    },
    // A block sequence entry's mapping stands at the column its content
    // starts at; a flow sequence's pair (`[a: 1]`) keeps the list's indent
    // and two, which no indent token reads.
    '@set-map-in': (rule: Rule, ctx: Context) => {
      rule.k.yamlMapIn = 'yamlBlockList' === rule.name || 'yamlBlockElem' === rule.name
        ? entryColumn(ctx.lex, rule.o0.sI)
        : rule.n.in + 2
    },
    '@t0-eq-in': (rule: Rule, ctx: Context) => ctx.t0.val === rule.n.in,
    '@t0-le-in': (rule: Rule, ctx: Context) => ctx.t0.val <= rule.n.in,
    '@t0-lt-in': (rule: Rule, ctx: Context) => ctx.t0.val < rule.n.in,
    '@o0-eq-in': (rule: Rule) => rule.o0.val === rule.n.in,
    '@t0-eq-map-in': (rule: Rule, ctx: Context) => ctx.t0.val === rule.k.yamlMapIn,
    '@elem-key': (rule: Rule) => { rule.u.key = extractKey(rule) },
    '@implicit-null-pair': (rule: Rule, ctx: Context) => {
      let key = extractKey(rule)
      rule.u.key = key
      storeMember(rule, ctx, key, null)
    },
    // Same as @pairkey, but the KEY is at o1 (after the leading #QM).
    '@qm-pairkey': (rule: Rule) => { rule.u.key = extractKey(rule, rule.o1) },
    '@qm-implicit-null-pair': (rule: Rule, ctx: Context) => {
      let key = extractKey(rule, rule.o1)
      rule.u.key = key
      storeMember(rule, ctx, key, null)
    },
  }

  // Parse the embedded grammar text and install declarative rules.
  const grammarDef: any = (new Tabnas().use(jsonic) as any).parse(grammarText)
  grammarDef.ref = refs
  tabnas.grammar(grammarDef)

  // ===== State handlers (bo/ao/bc/ac) — kept in code for closure capture =====

  // val rule: claim pending anchors (ao), handle empty (bc),
  //          resolve aliases and record anchors (ac).
  tabnas.rule('val', (rulespec: RuleSpec) => {
    rulespec.ao((rule: Rule, ctx: Context) => {
      if (pendingAnchors.length > 0) {
        rule.u.yamlAnchors = [...pendingAnchors]
        rule.u.yamlAnchorOpenNode = rule.node
        pendingAnchors.length = 0
        // The arrays inside the anchored value are kept, behind a stream
        // that empties them, until the anchor is recorded at this val's
        // close (holdPruning).
        holdPruning(ctx, 1)
        nameAnchors(rule)
      }
    })
    rulespec.bc((rule: Rule) => {
      if (rule.u.yamlEmpty) {
        rule.node = undefined
      }
    })
    rulespec.ac((rule: Rule, ctx: Context) => {
      // Resolve alias markers to actual values.
      if (rule.node && typeof rule.node === 'object' &&
          rule.node.__yamlAlias) {
        let name = rule.node.__yamlAlias
        let val = anchors[name]
        if (typeof val === 'object' && val !== null) {
          rule.node = copyAnchored(val)
        } else {
          rule.node = val
        }
      }

      // Record anchors only if this val claimed them.
      if (rule.u.yamlAnchors) {
        for (let anchor of rule.u.yamlAnchors) {
          if (anchor.inline &&
              rule.u.yamlAnchorOpenNode != null &&
              typeof rule.u.yamlAnchorOpenNode !== 'object' &&
              typeof rule.node === 'object' && rule.node !== null) {
            continue
          }
          let val = rule.node
          if (typeof val === 'object' && val !== null) {
            val = copyAnchored(val)
          }
          anchors[anchor.name] = val
        }
        // Recorded: the hold this val raised at its open is released.
        holdPruning(ctx, -1)
      }
    })
  })

  // indent rule: propagate child node up on close.
  tabnas.rule('indent', (rulespec: RuleSpec) => {
    rulespec.bc((rule: Rule) => {
      if (undefined !== rule.child.node) {
        rule.node = rule.child.node
      }
    })
  })

  // yamlBlockList rule: init array and push child nodes.
  tabnas.rule('yamlBlockList', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.node = []
      rule.k.yamlBlockArr = rule.node
      rule.k.yamlListIn = rule.n.in
    })
    rulespec.bc((rule: Rule) => {
      let val = rule.child.node !== undefined ? rule.child.node : null
      rule.k.yamlBlockArr.push(val)
    })
  })

  // yamlBlockElem rule: reuse shared array, push child nodes.
  tabnas.rule('yamlBlockElem', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.node = rule.k.yamlBlockArr
    })
    rulespec.bc((rule: Rule) => {
      let val = rule.child.node !== undefined ? rule.child.node : null
      rule.k.yamlBlockArr.push(val)
    })
  })

  // list rule: propagate list indent so val can check nesting depth, and
  // OWN the node-append phase for YAML block sequences.
  //
  // jsonic's @list-bo only allocates the array when the list is explicit
  // (`[` -> @array$) or a top-level implicit comma/space list
  // (prev.u.implist). A YAML block sequence reaches `list` a third way —
  // the indent rule's `#EL` alt does `p: list` with no `#OS` and no
  // implist — so neither builder runs and r.node stays the inherited
  // parent container (a map/pair value, or undefined). jsonic's
  // @elem-bc/replace then does a bare r.node.push(...) and throws
  // ("r.node.push is not a function") / silently drops elements in Go.
  // Allocate the array here so the push lands in a real list. For a flow
  // `[...]` list the subsequent `#OS` open alt's @array$ re-allocates an
  // (info-marked) array before any element rule is pushed, so this is a
  // harmless pre-seed in that case.
  tabnas.rule('list', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.k.yamlListIn = rule.n.in
      // OWN the node-append phase for an indented YAML block sequence.
      //
      // jsonic's @array$ only allocates the list's array on the flow `[`
      // (`#OS`) open alt; @list-bo only allocates for a top-level implicit
      // comma/space list (prev.u.implist). A block sequence nested deeper
      // than its map key reaches `list` a third way — the indent rule's
      // `#EL` alt does `p: list` with no `#OS` — so neither builder runs
      // and r.node stays the inherited parent container (the map). jsonic's
      // @elem-bc/replace then does a bare r.node.push(...) on that map and
      // throws ("r.node.push is not a function") in TS / silently drops
      // every element in Go. Allocate the array here so the push lands in a
      // real list. Only the indent path needs this: a flow `[...]` list is
      // pushed by `val` (parent=val) and gets its array from @array$, so it
      // is left untouched.
      if (rule.parent && 'indent' === rule.parent.name) {
        rule.node = []
      }
    })
  })

  // ===== stream rule: top-level YAML document collector =====
  // The stream rule replaces `val` as the parser's start rule. It consumes
  // doc-frame tokens (#DS, #DE, #DR) emitted by yamlMatcher, pushes a fresh
  // `val` rule for each document's content, and accumulates the results.
  // Final shape:
  //   - 0 docs (empty source) → undefined
  //   - 1 doc                  → the single value
  //   - >1 docs                → array of values
  const ensureCurMeta = () => {
    if (!yamlStreamCurMeta) {
      yamlStreamCurMeta = { directives: [], explicit: false, ended: false }
    }
  }
  const flushCurMeta = (ended: boolean) => {
    ensureCurMeta()
    yamlStreamCurMeta!.ended = ended || yamlStreamCurMeta!.ended
    yamlStreamMeta.push(yamlStreamCurMeta!)
    yamlStreamCurMeta = null
  }
  const accumChildDoc = (rule: Rule) => {
    if (rule.child && rule.child.node !== undefined) {
      yamlStreamDocs.push(rule.child.node)
    } else {
      yamlStreamDocs.push(null)
    }
    // The matched close-phase token tells us whether this doc ended
    // explicitly with `...`.
    flushCurMeta(rule.c0 != null && rule.c0.tin === DE)
  }
  const finalizeStream = (rule: Rule, ctx: Context) => {
    if (rule.child && rule.child.node !== undefined) {
      yamlStreamDocs.push(rule.child.node)
      flushCurMeta(false)
    } else if (yamlStreamCurMeta != null) {
      // The final document was explicitly opened (a `---` / `%TAG`
      // directive started a doc, recorded in yamlStreamCurMeta) but its
      // value coalesced to undefined — a bare `---` at end-of-stream, or a
      // trailing empty doc in `---\n---\n---`. jsonic's val-close treats a
      // deliberate `@val-set-null` as undefined (typeof null === 'object'
      // fails its primitive-value check), so the empty doc's null is lost
      // here; restore it the same way accumChildDoc / pushEmptyDoc force a
      // null for the non-final empty docs. Without an open doc (empty or
      // comment-only source) yamlStreamCurMeta stays null and the stream
      // correctly finalizes to undefined.
      yamlStreamDocs.push(null)
      flushCurMeta(false)
    }
    let content: any
    if (yamlStreamDocs.length === 0) {
      content = undefined
    } else if (yamlStreamDocs.length === 1) {
      content = yamlStreamDocs[0]
    } else {
      content = yamlStreamDocs.slice()
    }
    let result: any = content
    if (options.meta) {
      let meta: any
      if (yamlStreamMeta.length === 0) {
        meta = undefined
      } else if (yamlStreamMeta.length === 1) {
        meta = yamlStreamMeta[0]
      } else {
        meta = yamlStreamMeta.slice()
      }
      result = { meta, content }
    }
    rule.node = result
    // Rotation via `r: stream` creates a chain; ctx.root() is the original
    // stream the parser hands back. Write the result there.
    ctx.root().node = result
  }
  const applyDirective = (rule: Rule) => {
    let src: string = rule.o0.src || ''
    let m = src.match(/^%TAG\s+(\S+)\s+(\S+)/)
    if (m) tagHandles[m[1]] = m[2]
    ensureCurMeta()
    yamlStreamCurMeta!.directives.push(src)
  }
  const markExplicit = (_rule: Rule) => {
    ensureCurMeta()
    yamlStreamCurMeta!.explicit = true
  }

  tabnas.rule('stream', (rs: RuleSpec) => {
    rs.open([
      // Consume directive line; rotate to stream to look for the next token.
      { s: '#DR', a: applyDirective, r: 'stream', g: 'yaml' },
      // Explicit doc start: push val for the document content.
      { s: '#DS', a: markExplicit, p: 'val', g: 'yaml' },
      // `...` with no document open: it terminates nothing, so it does NOT
      // produce a document. Consume it and look for the next one. (YAML 1.2
      // 9.1.2; yaml-test-suite HWV9 / QT73 / M7A3, where a stray or
      // comment-only `...` region yields no document at all.)
      { s: '#DE', r: 'stream', g: 'yaml' },
      // Empty source: end immediately (stream.close will run).
      { s: '#ZZ', b: 1, g: 'yaml' },
      // Implicit first doc.
      { p: 'val', g: 'yaml' },
    ])
    rs.close([
      // End of input: accumulate last doc, finalize result shape.
      { s: '#ZZ', a: finalizeStream, g: 'yaml' },
      // Directive between docs: accumulate previous doc, apply, continue.
      { s: '#DR', a: (r: Rule) => { accumChildDoc(r); applyDirective(r) },
        r: 'stream', g: 'yaml' },
      // ... terminator: accumulate, look for next doc.
      { s: '#DE', a: accumChildDoc, r: 'stream', g: 'yaml' },
      // --- start of next doc (back up so stream.open consumes it).
      { s: '#DS', b: 1, a: accumChildDoc, r: 'stream', g: 'yaml' },
    ])
  })

  // Configure jsonic to start parsing with `stream` instead of `val`.
  tabnas.options({ rule: { start: 'stream' } })

  // The mapping nodes whose `<<` key was quoted. YAML resolves only a
  // plain `<<` to the merge key (the merge type is YAML 1.1's, and a
  // quoted scalar is always a string), so a quoted one is an ordinary key
  // and its mapping is not merged. A node is a fresh object per parse, so
  // the set needs no reset.
  const literalMergeKey: WeakSet<object> = new WeakSet()

  // pair rule: note a quoted `<<` key, a string token (after the `?` of
  // an explicit key in a flow mapping), on the mapping's node, which every
  // pair of a mapping shares. A plain `<<` holds a streaming host's
  // pruning (holdPruning) from its pair's open to its close: the map's ac
  // merges the sources the `<<` value lists, and a sequence of them,
  // emptied behind the stream when its rule closed, would merge nothing.
  tabnas.rule('pair', (rulespec: RuleSpec) => {
    rulespec.ao((rule: Rule, ctx: Context) => {
      let keyToken = QM === rule.o0.tin ? rule.o1 : rule.o0
      if ('<<' === rule.u.key && null != keyToken && ST !== keyToken.tin) {
        rule.u.yamlMergeHold = true
        holdPruning(ctx, 1)
      }
    })
    rulespec.bc((rule: Rule) => {
      let keyToken = QM === rule.o0.tin ? rule.o1 : rule.o0
      if ('<<' === rule.u.key && ST === keyToken.tin &&
          null != rule.node && 'object' === typeof rule.node) {
        literalMergeKey.add(rule.node)
      }
    })
    rulespec.ac((rule: Rule, ctx: Context) => {
      if (rule.u.yamlMergeHold) {
        rule.u.yamlMergeHold = false
        holdPruning(ctx, -1)
      }
    })
  })

  // map rule: default indent and merge-key handling.
  tabnas.rule('map', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      if (null == rule.n.in) {
        rule.n.in = 0
      }
      rule.k.yamlIn = rule.n.in
    })
    rulespec.ac((rule: Rule, ctx: Context) => {
      if (rule.node && typeof rule.node === 'object' && '<<' in rule.node &&
          !literalMergeKey.has(rule.node)) {
        let mergeVal = rule.node['<<']
        deleteMember(rule.node, '<<')
        if (Array.isArray(mergeVal)) {
          let source = 0
          for (let m of mergeVal) {
            if (typeof m === 'object' && m !== null && !Array.isArray(m)) {
              source++
              for (let k of Object.keys(m)) {
                if (!(k in rule.node)) mergeMember(rule, ctx, k, m[k], source)
              }
            }
          }
        } else if (typeof mergeVal === 'object' && mergeVal !== null) {
          for (let k of Object.keys(mergeVal)) {
            if (!(k in rule.node)) mergeMember(rule, ctx, k, mergeVal[k], 1)
          }
        }
      }
    })
  })

  // yamlElemMap rule: init the map. Its open alternate hands every pair,
  // the first included, to yamlElemPair (see yaml-grammar.jsonic), so it
  // never reaches a close phase and stores nothing itself.
  tabnas.rule('yamlElemMap', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.node = Object.create(null)
    })
  })

  // yamlElemPair rule: store each pair into the shared map node. A pair
  // whose value never arrived (`- a:` before the next entry) is a null
  // member, as `a:` at the root is and as the Go port stores it: an
  // undefined member is one JSON drops, and the mapping has the key.
  tabnas.rule('yamlElemPair', (rulespec: RuleSpec) => {
    rulespec.bc((rule: Rule, ctx: Context) => {
      if (rule.u.key != null) {
        storeMember(rule, ctx, rule.u.key, undefined === rule.child.node ? null : rule.child.node)
      }
    })
  })

}


Yaml.defaults = ({
  meta: false,
} as YamlOptions)


// Build a YAML parser whole: jsonic's base with the plugin installed, as
// `new Tabnas().use(jsonic).use(Yaml)` builds it, and with `lex.window`
// declared, so that `tn.parseReader` reads a reader a window at a time
// rather than whole (admin ADR-30). The plugin's matcher and its text check
// read the source through the window, reading on where a scan reaches its
// end, and keep the lines before the cursor that their backward scans read;
// jsonic's base cuts its tokens with the engine's own matchers. The window
// is declared here, on the parser this package makes whole, and not in
// `Yaml`, which a host may install beneath matchers of its own.
function make(options: YamlOptions = {}): Tabnas {
  const tn = new Tabnas().use(jsonic).use(Yaml, options)
  tn.options({ lex: { window: true } })
  return tn
}


// VERSION is this package's version. It MUST equal package.json "version":
// the release orchestrator rewrites both, and test/version.test.ts fails the
// build if they drift. Mirrors `const VERSION` in go/yaml.go.
const VERSION = '0.5.32'


export {
  Yaml,
  VERSION,
  make,
}

export type {
  YamlOptions,
}

export { translate } from './translate'
export type { TranslationPart, TranslationParts } from './translate'
