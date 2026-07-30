/* Copyright (c) 2025 Richard Rodger and other contributors, MIT License */

// Cross-runtime conformance, driven by the shared `test/spec/*.tsv` fixtures
// at the repo root — the same convention @tabnas/parser and @tabnas/abnf use
// (see ../../test/AGENTS.md).
//
// `go/parity_test.go` discovers and runs the SAME files, so the two
// implementations cannot drift without one of them going red.

import { describe, test } from 'node:test'
import assert from 'node:assert'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'

// At runtime this file is loaded from `dist-test/`, so hop up one level to
// reach the shared spec directory in the repo root.
const specDir = join(__dirname, '..', '..', 'test', 'spec')

type SpecRow = { line: number; input: string; expected: string; opts: string }

// Decode the escape set used in non-JSON columns. Kept byte-identical to the
// Go loader so both runtimes feed the parser the exact same source text.
function unescape(s: string): string {
  if (!s.includes('\\')) return s
  let out = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c === '\\' && i + 1 < s.length) {
      const n = s[i + 1]
      if (n === 'n') { out += '\n'; i++; continue }
      if (n === 'r') { out += '\r'; i++; continue }
      if (n === 't') { out += '\t'; i++; continue }
      if (n === '\\') { out += '\\'; i++; continue }
    }
    out += c
  }
  return out
}

function loadSpec(file: string): SpecRow[] {
  const body = readFileSync(join(specDir, file), 'utf8')
  const lines = body.split(/\r?\n/)
  const rows: SpecRow[] = []
  // Line 1 is the header naming the columns.
  for (let i = 1; i < lines.length; i++) {
    const raw = lines[i]
    // A comment line starts with '#' and has no tab; a data row always has
    // at least one (input + expected), so '#'-leading sources still work.
    if (raw === '' || (raw.startsWith('#') && !raw.includes('\t'))) continue
    const cols = raw.split('\t')
    if (cols.length < 2) {
      throw new Error(`${file}:${i + 1}: expected at least 2 tab-separated columns`)
    }
    rows.push({
      line: i + 1,
      input: unescape(cols[0]),
      expected: cols[1],
      opts: cols[2] ?? '',
    })
  }
  return rows
}

// YAML has non-finite numbers (.inf / .nan) that JSON cannot spell, and they
// can appear at any depth. Fixtures encode them as the marker strings
// "@@Infinity", "@@-Infinity" and "@@NaN"; canon maps a parse result into the
// same encoding so the two sides compare structurally. See ../../test/AGENTS.md.
function canon(v: any): any {
  if ('number' === typeof v && !Number.isFinite(v)) {
    return Number.isNaN(v) ? '@@NaN' : 0 < v ? '@@Infinity' : '@@-Infinity'
  }
  if (null === v || 'object' !== typeof v) return v ?? null
  if (Array.isArray(v)) return v.map(canon)
  const out: Record<string, any> = {}
  for (const k of Object.keys(v)) out[k] = canon(v[k])
  return out
}

// A truncated single-line rendering of the input, so a failure names its case.
function label(s: string): string {
  const one = s.replace(/\s+/g, ' ').trim()
  return 60 < one.length ? one.slice(0, 57) + '...' : one
}

function runSpec(file: string) {
  const rows = loadSpec(file)
  describe('spec: ' + file, () => {
    assert.ok(0 < rows.length, file + ': no cases')
    for (const row of rows) {
      test(`row ${row.line}: ${label(row.input)}`, () => {
        const opts = '' === row.opts.trim() ? {} : JSON.parse(row.opts)
        const tn = new Tabnas().use(jsonic).use(Yaml, opts)

        if (row.expected.startsWith('ERROR')) {
          const want = row.expected.slice('ERROR'.length).replace(/^:/, '')
          assert.throws(
            () => tn.parse(row.input),
            (err: Error) => '' === want || err.message.includes(want),
            `${file}:${row.line}: expected ${row.expected}`,
          )
          return
        }

        const raw = tn.parse(row.input)

        // Input that yields no value at all cannot be spelled in JSON.
        if ('UNDEFINED' === row.expected) {
          assert.strictEqual(raw, undefined, `${file}:${row.line}`)
          return
        }

        // A fixture that says `null` must not be satisfied by a parse that
        // produced nothing — UNDEFINED is the spelling for that.
        assert.notStrictEqual(raw, undefined,
          `${file}:${row.line}: no value; expected ${row.expected}`)

        // Round-trip through JSON so null-prototype maps and numeric types
        // compare structurally against the fixture's decoded shape.
        const got = JSON.parse(JSON.stringify(canon(raw)))
        assert.deepStrictEqual(got, JSON.parse(row.expected),
          `${file}:${row.line}`)
      })
    }
  })
}

// Auto-discover every fixture: adding a .tsv runs it in both runtimes
// without touching either runner.
for (const file of readdirSync(specDir).sort()) {
  if (file.endsWith('.tsv')) runSpec(file)
}
