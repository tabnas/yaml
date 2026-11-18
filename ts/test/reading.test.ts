/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */

// Three properties of the reader, each held over every text this
// repository has: every row of the shared fixtures, every case of the YAML
// Test Suite and every input of the YAML Test Schema.
//
// 1. A line break is `\n`, `\r\n`, or `\r` by itself (YAML 1.2.2, 5.4),
//    and which one a text uses changes nothing it reads as. Each text is
//    read as written, with every `\n` made `\r\n`, and with every `\n`
//    made `\r`, and the three must agree: in the value, or in the error's
//    code.
// 2. A token stands where its offset says: its row is one more than the
//    line breaks before it, and its column is how far it is past its
//    line's start, from 1, in UTF-16 units. A row and a column kept up
//    token by token drift, and once did: a column too far after every
//    `: `, no row for a line break inside a quoted scalar, column 0 after
//    a block scalar.
// 3. `schema: 'core'` changes how a plain scalar resolves and nothing
//    else: under it a text reads as it does without, except that a scalar
//    the plugin's own reading gives a boolean or a number may be the
//    string the core schema gives it.
//
// The Go and Rust suites hold the first and the third the same way
// (go/reading_test.go, rs/tests/reading_test.rs). The second is held there
// through an error's row and column, since neither exposes its tokens.

import { test, describe } from 'node:test'
import assert from 'node:assert'
import { readdirSync, readFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'

import { Tabnas, Token } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { findSpecDir, loadSpec } from '@tabnas/support'

import { Yaml } from '..'

const SPEC = findSpecDir(__dirname)
const REPO_ROOT = join(__dirname, '..', '..')
const SUITE = join(REPO_ROOT, 'test', 'yaml-test-suite')
const SCHEMA = join(REPO_ROOT, 'test', 'yaml-test-schema', 'schema-core.json')

type Text = { at: string, text: string, opts: Record<string, any> }

// Every text: the fixture rows with their options, then the suite's
// cases, sub-cases (`AB12/00`) included, then the schema's inputs.
function texts(): Text[] {
  const out: Text[] = []
  for (const file of readdirSync(SPEC).filter((f) => f.endsWith('.tsv')).sort()) {
    for (const row of loadSpec(join(SPEC, file)).rows) {
      const opts = row.named('opts')
      out.push({
        at: file + ':' + row.line,
        text: row.unescNamed('input'),
        opts: '' === opts.trim() ? {} : JSON.parse(opts),
      })
    }
  }
  for (const entry of readdirSync(SUITE).sort()) {
    const dir = join(SUITE, entry)
    if (existsSync(join(dir, 'in.yaml'))) {
      out.push({ at: 'suite ' + entry, text: readFileSync(join(dir, 'in.yaml'), 'utf8'), opts: {} })
      continue
    }
    if (!existsSync(dir) || !readdirSync(dir).length) continue
    for (const sub of readdirSync(dir).filter((s) => /^\d+$/.test(s)).sort()) {
      const file = join(dir, sub, 'in.yaml')
      if (existsSync(file)) {
        out.push({ at: 'suite ' + entry + '/' + sub, text: readFileSync(file, 'utf8'), opts: {} })
      }
    }
  }
  for (const input of Object.keys(JSON.parse(readFileSync(SCHEMA, 'utf8')))) {
    out.push({ at: 'schema ' + JSON.stringify(input), text: input, opts: {} })
  }
  return out
}

// A parser for a row's options: one for each set of options, since
// building one reads the grammar's text, and a parser keeps nothing from
// one parse to the next.
const parsers: Map<string, Tabnas> = new Map()
function yamlWith(opts: Record<string, any>): Tabnas {
  const key = JSON.stringify(opts)
  let tn = parsers.get(key)
  if (undefined === tn) {
    tn = new Tabnas().use(jsonic).use(Yaml, opts)
    parsers.set(key, tn)
  }
  return tn
}

// A value as text that tells apart everything a parse can give.
function canon(v: any): string {
  if ('number' === typeof v) return Object.is(v, -0) ? '-0' : String(v)
  if (undefined === v) return 'undefined'
  if (Array.isArray(v)) return '[' + v.map(canon).join(',') + ']'
  if (null != v && 'object' === typeof v) {
    return '{' + Object.keys(v).map((k) => JSON.stringify(k) + ':' + canon(v[k])).join(',') + '}'
  }
  return String(JSON.stringify(v))
}

// What a parse shows: its value, or its error's code.
function seen(parse: () => unknown): string {
  try {
    return 'value ' + canon(parse())
  } catch (e: any) {
    return 'error ' + (e && e.code)
  }
}

const short = (s: string): string => (s.length > 200 ? s.slice(0, 200) + '...' : s)

// The row and column of offset `sI` in `text`: a line break is `\n`, a
// `\r` no `\n` follows, or `\r\n`, and a byte order mark at the start
// takes no column. Between the halves of a `\r\n` the line has ended.
function placeOf(text: string, sI: number): { row: number, col: number } {
  let row = 1
  let ls = 0xFEFF === text.charCodeAt(0) ? 1 : 0
  for (let i = 0; i < sI && i < text.length; i++) {
    const c = text[i]
    if ('\n' === c || '\r' === c) {
      if ('\n' === c && 0 < i && '\r' === text[i - 1]) {
        ls = i + 1
      } else {
        row++
        ls = i + 1
      }
    }
  }
  return { row, col: sI < ls ? 1 : sI - ls + 1 }
}

describe('reading', () => {
  const all = texts()

  test('census', () => {
    // The texts are all there: a property held over a truncated set is
    // held over less than it says.
    assert.ok(1400 < all.length, '' + all.length)
  })

  for (const [name, brk] of [['crlf', '\r\n'], ['cr', '\r']] as [string, string][]) {
    test('line-breaks-' + name, () => {
      let compared = 0
      let own = 0
      const failures: string[] = []
      for (const { at, text, opts } of all) {
        // A text with a carriage return of its own is about one line
        // break or another already.
        if (text.includes('\r')) { own++; continue }
        const tn = yamlWith(opts)
        const written = seen(() => tn.parse(text))
        const rewritten = seen(() => tn.parse(text.replace(/\n/g, brk)))
        compared++
        if (written !== rewritten) {
          failures.push(at + ': ' + JSON.stringify(text.slice(0, 80)) +
            '\n  as written: ' + short(written) + '\n  with ' + name + ': ' + short(rewritten))
        }
      }
      assert.ok(1300 < compared, compared + ' compared, ' + own + ' left out')
      assert.deepEqual(failures.slice(0, 10), [], failures.length + ' texts differ')
    })
  }

  test('a-token-stands-where-its-offset-says', () => {
    let tokens = 0
    const failures: string[] = []
    // The text being parsed, which the one subscriber of each parser reads.
    let at = ''
    let text = ''
    const watched: Map<string, Tabnas> = new Map()
    const watchedWith = (opts: Record<string, any>): Tabnas => {
      const key = JSON.stringify(opts)
      let tn = watched.get(key)
      if (undefined === tn) {
        tn = new Tabnas().use(jsonic).use(Yaml, opts)
        tn.sub({
          lex: (tkn: Token) => {
            tokens++
            const want = placeOf(text, tkn.sI)
            if ((want.row !== tkn.rI || want.col !== tkn.cI) && failures.length < 10) {
              failures.push(at + ': ' + tkn.name + ' ' + JSON.stringify(String(tkn.src).slice(0, 20)) +
                ' at ' + tkn.sI + ' has ' + tkn.rI + ':' + tkn.cI + ', its offset gives ' +
                want.row + ':' + want.col + ' in ' + JSON.stringify(text.slice(0, 60)))
            }
          },
        })
        watched.set(key, tn)
      }
      return tn
    }
    for (const one of all) {
      at = one.at
      text = one.text
      try { watchedWith(one.opts).parse(text) } catch (e) { /* its tokens were placed all the same */ }
    }
    assert.ok(15000 < tokens, '' + tokens)
    assert.deepEqual(failures, [])
  })

  test('an-error-stands-where-its-offset-says', () => {
    // The diagnostic a refused text gives: its row and column are its
    // offset's, in every text that is refused.
    let errors = 0
    const failures: string[] = []
    for (const { at, text, opts } of all) {
      let err: any = null
      try { yamlWith(opts).parse(text) } catch (e) { err = e }
      if (null == err || 'function' !== typeof err.toJSON) continue
      const d = err.toJSON()
      errors++
      const want = placeOf(text, d.pos)
      if ((want.row !== d.row || want.col !== d.col) && failures.length < 10) {
        failures.push(at + ': ' + d.code + ' at ' + d.pos + ' has ' + d.row + ':' + d.col +
          ', its offset gives ' + want.row + ':' + want.col + ' in ' + JSON.stringify(text.slice(0, 60)))
      }
    }
    assert.ok(80 < errors, '' + errors)
    assert.deepEqual(failures, [])
  })

  test('the-core-schema-changes-plain-scalars-alone', () => {
    // `under` is `plain` except where a boolean or a number of `plain` is
    // a string in `under`: null when it is, and otherwise where they part.
    const parts = (plain: any, under: any, path: string): string | null => {
      if (Array.isArray(plain) || Array.isArray(under)) {
        if (!Array.isArray(plain) || !Array.isArray(under) || plain.length !== under.length) {
          return path + ' (a sequence)'
        }
        for (let i = 0; i < plain.length; i++) {
          const p = parts(plain[i], under[i], path + '/' + i)
          if (null != p) return p
        }
        return null
      }
      if (null != plain && 'object' === typeof plain) {
        if (null == under || 'object' !== typeof under) return path + ' (a mapping)'
        const keys = Object.keys(plain)
        if (canon(keys.slice().sort()) !== canon(Object.keys(under).sort())) return path + ' (its keys)'
        for (const k of keys) {
          const p = parts(plain[k], under[k], path + '/' + k)
          if (null != p) return p
        }
        return null
      }
      if (canon(plain) === canon(under)) return null
      if (('boolean' === typeof plain || 'number' === typeof plain) && 'string' === typeof under) return null
      return path + ' (' + canon(plain) + ' and ' + canon(under) + ')'
    }

    let compared = 0
    let changed = 0
    const failures: string[] = []
    for (const { at, text, opts } of all) {
      if ('core' === opts.schema) continue
      const plainTn = yamlWith(opts)
      const coreTn = yamlWith({ ...opts, schema: 'core' })
      let plain: any, under: any, plainErr: any = null, underErr: any = null
      try { plain = plainTn.parse(text) } catch (e: any) { plainErr = e }
      try { under = coreTn.parse(text) } catch (e: any) { underErr = e }
      compared++
      if (null != plainErr || null != underErr) {
        const a = null == plainErr ? 'a value' : 'error ' + plainErr.code
        const b = null == underErr ? 'a value' : 'error ' + underErr.code
        if (a !== b) failures.push(at + ': ' + JSON.stringify(text.slice(0, 80)) + ' gives ' + a + ', and under the core schema ' + b)
        continue
      }
      if (canon(plain) !== canon(under)) changed++
      const where = parts(plain, under, '')
      if (null != where) {
        failures.push(at + ': ' + JSON.stringify(text.slice(0, 80)) + ' parts at ' + where)
      }
    }
    assert.ok(1300 < compared, '' + compared)
    // The option does something: the fixtures hold the YAML 1.1 words and
    // jsonic's numbers, which it reads as strings.
    assert.ok(20 < changed, '' + changed)
    assert.deepEqual(failures.slice(0, 10), [], failures.length + ' texts part elsewhere')
  })
})
