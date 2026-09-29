/* Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License */

/* YAML Test Schema conformance: how plain and tagged scalars resolve.
 *
 * Corpus: https://github.com/perlpunk/yaml-test-schema (MIT, Tina Müller),
 * vendored byte-identical at test/yaml-test-schema (relative to the repo
 * root); see test/yaml-test-schema/README.md for the source commit.
 *
 * schema-core.json maps each input (one scalar, as a whole YAML document)
 * to how the YAML 1.2 CORE schema resolves it: `[type, value, dumped]`,
 * where `value` is a literal (`"8"`, `"3.14"`, a string) or a native
 * marker (`true()`, `null()`, `inf-neg()`, `nan()`).
 *
 * EVERY case is asserted. A case must read as the core schema says, unless
 * its input is on the checked ledger test/yaml-test-schema-deviations.tsv,
 * in which case it must still NOT read that way: a repaired deviation fails
 * until its line is deleted. The census pins the corpus size and its types,
 * so a truncated file cannot improve the ratio for free.
 *
 * The Go (go/yaml_test_schema_test.go) and Rust
 * (rs/tests/yaml_test_schema_test.rs) runners read the same two files and
 * apply the same rules, so the three runtimes cannot drift.
 */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { readFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


const REPO_ROOT = join(__dirname, '..', '..')
const CORPUS = join(REPO_ROOT, 'test', 'yaml-test-schema', 'schema-core.json')
const LEDGER = join(REPO_ROOT, 'test', 'yaml-test-schema-deviations.tsv')

// Refuse to skip: without the corpus there is no measurement.
if (!existsSync(CORPUS)) {
  throw new Error(
    'yaml-test-schema corpus is MISSING at ' + CORPUS + '.\n' +
    'It is vendored in this repo: restore it with ' +
    '`git checkout -- test/yaml-test-schema`.')
}

const corpus: Record<string, [string, string, string]> =
  JSON.parse(readFileSync(CORPUS, 'utf8'))

// `<input as a JSON string> <TAB> <reason>`, # comments and blanks ignored.
function loadLedger(): Set<string> {
  const out = new Set<string>()
  for (const raw of readFileSync(LEDGER, 'utf8').split(/\r?\n/)) {
    if ('' === raw.trim() || raw.startsWith('#')) continue
    out.add(JSON.parse(raw.split('\t')[0]))
  }
  return out
}

// The value the core schema gives, as this runtime represents it.
function expected([type, value]: [string, string, string]): any {
  switch (type) {
    case 'bool': return 'true()' === value
    case 'null': return null
    case 'inf': return 'inf()' === value ? Infinity : -Infinity
    case 'nan': return NaN
    case 'int':
    case 'float': return Number(value)
    case 'str': return value
  }
  throw new Error('unknown type in corpus: ' + type)
}

// Strict: same type and value (NaN equals NaN; 1 is not "1").
function same(got: any, want: any): boolean {
  return typeof got === typeof want && (got === want || Object.is(got, want))
}

const yaml = new Tabnas().use(jsonic).use(Yaml)

function read(src: string): { ok: boolean, got?: any, err?: string } {
  try {
    return { ok: true, got: yaml.parse(src) }
  } catch (e: any) {
    return { ok: false, err: String(e?.code || e?.message || e) }
  }
}


describe('yaml-test-schema (core)', () => {
  const ledger = loadLedger()
  const inputs = Object.keys(corpus)

  test('census', () => {
    const types: Record<string, number> = {}
    for (const k of inputs) types[corpus[k][0]] = (types[corpus[k][0]] || 0) + 1
    assert.deepStrictEqual(
      { total: inputs.length, ...types },
      { total: 245, bool: 12, float: 32, inf: 18, int: 35, nan: 6, null: 10, str: 132 })
    for (const k of ledger) {
      assert.ok(k in corpus, 'ledger lists an input the corpus does not have: ' +
        JSON.stringify(k))
    }
  })

  for (const input of inputs) {
    const want = expected(corpus[input])
    const listed = ledger.has(input)
    test(JSON.stringify(input) + (listed ? ' (listed deviation)' : ''), () => {
      const r = read(input)
      const matches = r.ok && same(r.got, want)
      if (listed) {
        assert.ok(!matches, JSON.stringify(input) + ' now reads as the core ' +
          'schema says: DELETE its line from test/yaml-test-schema-deviations.tsv')
      } else {
        assert.ok(matches, JSON.stringify(input) + ': want ' + String(want) +
          ' (' + typeof want + '), got ' +
          (r.ok ? String(r.got) + ' (' + typeof r.got + ')' : 'error ' + r.err))
      }
    })
  }
})
