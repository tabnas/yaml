/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */

// A parse from a reader (`tn.parseReader`, admin ADR-30 and ADR-33).
//
// `make()` builds the parser whole, as `new Tabnas().use(jsonic).use(Yaml)`
// builds it, and declares `lex.window` on it, so that a reader is read a
// window at a time. The plugin's matcher and its text check read the source
// through the window, read on where a scan reaches its end, and keep the
// lines before the cursor their backward scans read. These tests hold it to
// that: `make()` declares the window and parses as that composition does;
// every row of the shared fixtures, every case of the YAML Test Suite and
// every input of the YAML Test Schema parses the same from a reader, in
// pieces of 1, 3 and 64 bytes, under windows of a few UTF-16 units and the
// engine's own, as from its text; and tokens longer than the window, a flow
// colon far past the quote it follows, and a long document parse the same
// too.

import { test, describe } from 'node:test'
import assert from 'node:assert'
import { readdirSync, readFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { findSpecDir, loadSpec } from '@tabnas/support'

import { Yaml, make } from '..'

const SPEC = findSpecDir(__dirname)
const REPO_ROOT = join(__dirname, '..', '..')
const SUITE = join(REPO_ROOT, 'test', 'yaml-test-suite')
const SCHEMA = join(REPO_ROOT, 'test', 'yaml-test-schema', 'schema-core.json')

type Reader = { read(buf: Uint8Array): number }
type Sizes = { margin: number; block: number; chunk: number }

// A reader that hands its bytes over at most `size` at a time.
function pieces(bytes: Uint8Array, size: number): Reader {
  let at = 0
  return {
    read(buf: Uint8Array): number {
      const n = Math.min(size, buf.length, bytes.length - at)
      buf.set(bytes.subarray(at, at + n))
      at += n
      return n
    },
  }
}

// A value as text that tells apart everything a parse can give: the order
// of a mapping's keys, a number's sign, NaN and the infinities, and
// undefined.
function canon(v: any): string {
  if ('number' === typeof v) {
    return Object.is(v, -0) ? '-0' : String(v)
  }
  if (undefined === v) {
    return 'undefined'
  }
  if (Array.isArray(v)) {
    return '[' + v.map(canon).join(',') + ']'
  }
  if (null != v && 'object' === typeof v) {
    return '{' + Object.keys(v).map((k) => JSON.stringify(k) + ':' + canon(v[k])).join(',') + '}'
  }
  return String(JSON.stringify(v))
}

// What a parse shows: its value, or its error's code, position, row,
// column and token text. An error's excerpt is left out: a parse from a
// reader shows a long line only as far back as the window reaches
// (tabnas/parser DIVERGENCE.md), the one difference allowed.
function seen(parse: () => unknown): string {
  try {
    return 'value ' + canon(parse())
  } catch (e: any) {
    if (null == e || 'function' !== typeof e.toJSON) {
      return 'throw ' + (e && e.name) + ' ' + (e && e.message)
    }
    const d = e.toJSON()
    return 'error ' + d.code + ' at ' + d.pos + ' ' + d.row + ':' + d.col + ' ' +
      JSON.stringify(d.token.src)
  }
}

const short = (s: string): string => (s.length > 300 ? s.slice(0, 300) + '...' : s)

// The pieces a reader hands its bytes over in, and the windows (margin and
// block) it is read under besides the engine's own: a few units, so that
// every scan meets the window's edge, and the window moves on behind the
// cursor at every token.
const PIECES = [1, 3, 64]
const WINDOWS = [[1, 1], [3, 2], [64, 64]]

// `text` parsed by `tn` whole, and from a reader in every pieces size,
// under the engine's window and each of WINDOWS: how many reader parses
// were compared. A difference is pushed onto `failures`.
function sameFromReader(tn: Tabnas, at: string, text: string, failures: string[]): number {
  const whole = seen(() => tn.parse(text))
  const bytes = Buffer.from(text, 'utf8')
  const anyTn = tn as any
  let compared = 0
  for (const size of PIECES) {
    const reads: [string, () => unknown][] = [['', () => anyTn.parseReader(pieces(bytes, size))]]
    for (const [margin, block] of WINDOWS) {
      const sizes: Sizes = { margin, block, chunk: size }
      reads.push([
        ', margin ' + margin + ', block ' + block,
        () => anyTn.parseReaderWithSizes(pieces(bytes, size), sizes),
      ])
    }
    for (const [how, parse] of reads) {
      const read = seen(parse)
      compared++
      if (read !== whole) {
        failures.push(
          at + ': ' + JSON.stringify(text.slice(0, 80)) + ' from a reader in pieces of ' +
          size + how + '\n  whole:  ' + short(whole) + '\n  reader: ' + short(read),
        )
      }
    }
  }
  return compared
}

// Every case of the YAML Test Suite: its id and its input, sub-cases
// (`AB12/00`) included, as yaml-test-suite.test.ts gathers them.
function suiteCases(): [string, string][] {
  const cases: [string, string][] = []
  for (const entry of readdirSync(SUITE).sort()) {
    const dir = join(SUITE, entry)
    if (existsSync(join(dir, 'in.yaml'))) {
      cases.push([entry, readFileSync(join(dir, 'in.yaml'), 'utf8')])
      continue
    }
    if (!existsSync(dir) || !readdirSync(dir).length) continue
    for (const sub of readdirSync(dir).filter((s) => /^\d+$/.test(s)).sort()) {
      const file = join(dir, sub, 'in.yaml')
      if (existsSync(file)) cases.push([entry + '/' + sub, readFileSync(file, 'utf8')])
    }
  }
  return cases
}

describe('window', () => {
  test('make-declares-the-window', () => {
    const tn = make() as any
    assert.equal(tn.internal().config.lex.window, true)
    // The plugin alone declares nothing: a host may install it beneath
    // matchers of its own.
    const composed = new Tabnas().use(jsonic).use(Yaml) as any
    assert.ok(!composed.internal().config.lex.window)
  })

  test('make-parses-as-the-composition-does', () => {
    // Every fixture row, whole, through `make()` and through
    // `new Tabnas().use(jsonic).use(Yaml)`, which dmog composed, with the
    // row's options when it has them.
    let compared = 0
    const failures: string[] = []
    const madeTn = make()
    const composedTn = new Tabnas().use(jsonic).use(Yaml)
    for (const file of readdirSync(SPEC).filter((f) => f.endsWith('.tsv')).sort()) {
      for (const row of loadSpec(join(SPEC, file)).rows) {
        const input = row.unescNamed('input')
        const made = seen(() => madeTn.parse(input))
        const composed = seen(() => composedTn.parse(input))
        compared++
        if (made !== composed) {
          failures.push(file + ':' + row.line + '\n  make:     ' + made + '\n  composed: ' + composed)
        }
      }
    }
    // With options, as the plugin takes them.
    const meta = 'a: 1\n---\nb: 2\n'
    const madeMeta = seen(() => make({ meta: true }).parse(meta))
    const composedMeta = seen(() => new Tabnas().use(jsonic).use(Yaml, { meta: true }).parse(meta))
    assert.equal(madeMeta, composedMeta)
    assert.ok(madeMeta.includes('"meta"'), madeMeta)
    assert.ok(600 < compared, '' + compared)
    assert.deepEqual(failures, [])
  })

  test('every-row-parses-the-same-from-a-reader', () => {
    const files = readdirSync(SPEC).filter((f) => f.endsWith('.tsv')).sort()
    let compared = 0
    const failures: string[] = []
    const tn = make()
    files.forEach((file, index) => {
      for (const row of loadSpec(join(SPEC, file)).rows) {
        compared += sameFromReader(tn, file + ':' + row.line, row.unescNamed('input'), failures)
      }
      console.log('window fixtures: ' + (index + 1) + ' of ' + files.length + ' files (' +
        Math.floor(((index + 1) * 100) / files.length) + '%), ' + compared + ' reader parses')
    })
    assert.ok(7000 < compared, '' + compared)
    assert.deepEqual(failures, [])
  })

  test('the-yaml-test-suite-parses-the-same-from-a-reader', () => {
    const cases = suiteCases()
    // As yaml-test-suite.test.ts pins the census: every case is read.
    assert.equal(cases.length, 402)
    const tn = make()
    let compared = 0
    const failures: string[] = []
    cases.forEach(([id, text], index) => {
      compared += sameFromReader(tn, 'suite ' + id, text, failures)
      if (0 === (index + 1) % 100 || cases.length === index + 1) {
        console.log('window suite: ' + (index + 1) + ' of ' + cases.length + ' cases (' +
          Math.floor(((index + 1) * 100) / cases.length) + '%), ' + compared + ' reader parses')
      }
    })
    assert.equal(compared, 402 * 12)
    assert.deepEqual(failures, [])
  })

  test('the-yaml-test-schema-parses-the-same-from-a-reader', () => {
    const corpus: Record<string, unknown> = JSON.parse(readFileSync(SCHEMA, 'utf8'))
    const inputs = Object.keys(corpus)
    assert.equal(inputs.length, 245)
    const tn = make()
    let compared = 0
    const failures: string[] = []
    for (const input of inputs) {
      compared += sameFromReader(tn, 'schema ' + JSON.stringify(input), input, failures)
    }
    console.log('window schema: ' + inputs.length + ' inputs, ' + compared + ' reader parses')
    assert.deepEqual(failures, [])
  })

  test('tokens-longer-than-the-window-parse-the-same-from-a-reader', () => {
    // The engine's window reaches 64 KiB past the cursor before each
    // token: a token longer than that meets its edge under the engine's
    // own sizes, and so does a line the backward scans read from its
    // start. Each kind of token the plugin cuts itself, long.
    const long = (64 << 10) + 2048
    const lines = (n: number, line: string) => line.repeat(n)
    const docs = [
      // Plain scalars: one line, folded over lines, in a flow sequence.
      'a: ' + 'p'.repeat(long) + '\nb: 1\n',
      'a: x\n' + lines(long / 8, '  more t\n') + 'b: 2\n',
      '- ' + 'w '.repeat(long / 2) + 'end\n- next\n',
      '[' + 'item, '.repeat(long / 6) + 'last]\n',
      // Quoted scalars, with escapes and folded lines.
      'a: "' + 'd'.repeat(long) + '\\u00e9\\x41\\U0001F600\\n"\nb: 1\n',
      'a: "' + 'é'.repeat(long / 2) + '"\n',
      "a: '" + "s''".repeat(long / 3) + "'\nb: 1\n",
      'a: "line\n' + lines(long / 6, '  word\n') + '  end"\n',
      // Block scalars, literal and folded, kept and stripped, explicit.
      'a: |\n' + lines(long / 8, '  literal\n') + 'b: 1\n',
      'a: >-\n' + lines(long / 8, '  folded\n') + '\n  para\nb: 1\n',
      'a: |+\n' + lines(long / 8, '  kept\n') + '\n\n',
      '- a: |2\n' + lines(long / 8, '     deep\n') + '- b\n',
      // Comments, blank lines and anchors between lines.
      '# ' + 'c'.repeat(long) + '\na: 1\n',
      'a: 1\n' + lines(long / 4, '# c\n') + 'b: 2\n',
      'a: 1\n' + '\n'.repeat(long) + 'b: 2\n',
      'a: 1\n' + lines(long / 4, ' \t \n') + 'b: 2\n',
      // Anchors, aliases and tags with long names and values.
      '&' + 'n'.repeat(long) + ' a: 1\nb: *' + 'n'.repeat(long) + '\n',
      'a: &x ' + 'v'.repeat(long) + '\nb: *x\n',
      'a: !!str ' + 's'.repeat(long) + '\nb: !!int "12"\n',
      'a: !' + 't'.repeat(long) + ' value\n',
      // Explicit keys, directives, documents.
      '? ' + 'k'.repeat(long) + '\n: v\n',
      '? |\n' + lines(long / 8, '  keyline\n') + ': v\n',
      '%YAML 1.2\n%TAG !e! tag:' + 'e'.repeat(long) + ':\n---\na: 1\n...\n---\nb: 2\n',
      // A long line whose start the backward scans read from its end.
      'k' + 'e'.repeat(long) + 'y: |\n  text\n',
      '- - ' + '- '.repeat(long / 2) + 'a: |1\n   x\n',
      // A flow colon after a quote far behind it.
      '{"a"' + '\n'.repeat(long) + ':b}\n',
      '{"a" # c\n' + lines(long / 8, '  # more\n') + ':b}\n',
      // Numbers longer than the window, which the engine's number matcher
      // reads and the text check would read on.
      'a: 1' + '_1'.repeat(long / 2) + '\nb: 2\n',
      'a: .' + '5'.repeat(long) + 'x\n',
      // Cut short by the source's end.
      'a: "' + 'u'.repeat(long),
      "a: '" + 'u\n'.repeat(long / 2),
      '? ' + 'k'.repeat(long),
      'a: *' + 'm'.repeat(long),
    ]
    const tn = make() as any
    const failures: string[] = []
    docs.forEach((doc, n) => {
      const whole = seen(() => tn.parse(doc))
      for (const size of [4096, (64 << 10) + 3]) {
        const read = seen(() => tn.parseReader(pieces(Buffer.from(doc, 'utf8'), size)))
        if (read !== whole) {
          failures.push('document ' + n + ' in pieces of ' + size + ': whole ' +
            short(whole) + ', reader ' + short(read))
        }
      }
      console.log('window long tokens: ' + (n + 1) + ' of ' + docs.length + ' (' +
        Math.floor(((n + 1) * 100) / docs.length) + '%)')
    })
    assert.deepEqual(failures, [])
  })

  test('a-long-document-parses-the-same-from-a-reader', () => {
    // From a reader in pieces of 4 KiB, under the engine's window and
    // under one of 256 units, which moves on thousands of times; with an
    // error at the end too, which the reader places where the whole text
    // does. A few hundred KB: the engine keeps every rule a parse makes,
    // which grows its heap with the document.
    const parts: string[] = ['# records\ntitle: records\nbase: &base\n  kind: record\n  tags: [a, b]\n']
    for (let i = 0; i < 1800; i++) {
      parts.push(
        '\nrecord' + i + ':\n  <<: *base\n  id: ' + i + '\n  name: "ñ' + i + '\\u00e9" # note ' + i +
        "\n  path: 'C:\\dir\\" + i + "'\n  when: 2001-12-14t21:59:43." + i + '-05:00\n' +
        '  list:\n    - one\n    - &i' + i + ' two ' + i + '\n    - { k: ' + i + ', v: [1, 2] }\n' +
        '  note: |\n    line ' + i + '\n    more\n  folded: >-\n    a\n    b ' + i + '\n' +
        '  ? explicit ' + i + '\n  : value\n  again: *i' + i + '\n  plain: word word\n    word\n',
      )
    }
    const body = parts.join('')
    const text = body + '\nend: true\n'
    assert.ok(300 << 10 < text.length, '' + text.length)
    const broken = body + '\nend: *missing\n'
    const tn = make() as any
    ;[text, broken].forEach((doc, n) => {
      const whole = seen(() => tn.parse(doc))
      assert.equal(whole.startsWith('value '), 0 === n, short(whole))
      for (const sizes of [undefined, { margin: 256, block: 256, chunk: 4096 }]) {
        const bytes = Buffer.from(doc, 'utf8')
        const read = seen(() =>
          undefined === sizes
            ? tn.parseReader(pieces(bytes, 4096))
            : tn.parseReaderWithSizes(pieces(bytes, 4096), sizes),
        )
        assert.equal(read, whole)
      }
      console.log('window long document: ' + (n + 1) + ' of 2 (' + (n + 1) * 50 + '%)')
    })
  })

  test('a-reader-is-read-no-further-than-the-window-needs', () => {
    // An alias to no anchor near the start of a document of some 4 MB:
    // from a reader the parse fails there having read a window or so past
    // it, not the document, which a scan that read to the source's end
    // would have read.
    const doc = 'a: 1\nb: *missing\n' + 'c: [1, \'two\', "3"] # c\n'.repeat(160000)
    assert.ok(3 << 20 < doc.length, '' + doc.length)
    const tn = make() as any
    const whole = seen(() => tn.parse(doc))
    assert.ok(whole.includes('yaml_undefined_alias'), short(whole))
    const inner = pieces(Buffer.from(doc, 'utf8'), 4096)
    let read = 0
    const counted: Reader = {
      read(buf: Uint8Array): number {
        const n = inner.read(buf)
        read += n
        return n
      },
    }
    assert.equal(seen(() => tn.parseReader(counted)), whole)
    assert.ok(read < 512 << 10, read + ' bytes read')
  })
})
