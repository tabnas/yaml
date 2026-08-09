/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

/* Official YAML Test Suite conformance — the repo's conformance dial.
 *
 * Corpus: https://github.com/yaml/yaml-test-suite (`data` branch), vendored
 * byte-identical at test/yaml-test-suite (relative to the repo root).
 *
 * Each case directory holds:
 *   in.yaml  — the input
 *   in.json  — the expected value, as a STREAM of JSON documents (one per
 *              YAML document; an empty file means "no documents at all")
 *   error    — a marker file: this input MUST be rejected
 *   ===      — a human-readable case name
 *
 * EVERY case is asserted. There is no skip list and no group that is merely
 * "gathered": a conformance suite that quietly does not run reports green
 * while measuring nothing. The three groups are
 *
 *   valid-parse           in.json present: must parse AND deep-equal it.
 *   expected-errors       `error` present: must be REJECTED, unless the id is
 *                         on the checked leniency ledger
 *                         (test/yaml-test-suite-lenient.tsv).
 *   valid-parse-novalue   neither file: the suite publishes no expected value,
 *                         so the strongest honest assertion is that a valid
 *                         document parses at all. The ones this parser still
 *                         rejects are enumerated in the checked ledger
 *                         test/yaml-test-suite-unparsed.tsv — never skipped.
 *
 * The value comparison is STRICT and covers EVERY document in the stream. It
 * used to be a `deepLooseEqual` that equated the number 1 with the string
 * "1", and equated an array with an object carrying the same index keys, and
 * it compared only the FIRST document of a multi-document stream — so a
 * multi-document case was scored on a fraction of its expected output. Both
 * of those hid real conformance failures and are gone.
 *
 * Both ledger files are read by the Go runner (go/yaml_test_suite_test.go)
 * too, so the two runtimes are scored identically and cannot drift.
 */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { join } from 'node:path'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


const REPO_ROOT = join(__dirname, '..', '..')
const SUITE_DIR = join(REPO_ROOT, 'test', 'yaml-test-suite')

// The suite's `error` cases are inputs YAML 1.2 forbids. This plugin parses a
// documented subset on top of the deliberately lenient jsonic grammar, so it
// rejects some and accepts others. This is the checked ledger of exactly
// which ones it accepts. See that file's header.
const LENIENT_FILE = join(REPO_ROOT, 'test', 'yaml-test-suite-lenient.tsv')

// The parse-only cases (no in.json, no error) that this parser still rejects,
// although the suite says they are valid YAML. Same discipline as the
// leniency ledger: a listed id that starts parsing must lose its line.
const UNPARSED_FILE = join(REPO_ROOT, 'test', 'yaml-test-suite-unparsed.tsv')


// This suite must never silently skip. Run at module top level on purpose: a
// throw inside a describe() body is reported as one failed suite, but a throw
// here fails the whole file to load, which is impossible to overlook.
if (!existsSync(join(SUITE_DIR, '229Q', 'in.yaml'))) {
  throw new Error(
    'yaml-test-suite corpus is MISSING or truncated at ' + SUITE_DIR + '.\n' +
    'It is vendored in this repo, so this means the working tree is damaged: ' +
    'restore it with `git checkout -- test/yaml-test-suite`.\n' +
    'This suite refuses to skip — without the corpus there is no conformance ' +
    'measurement, and a green run would be meaningless.')
}


// Read a ledger file: `<case id> <TAB> <description>`, # comments and blanks
// ignored.
function loadLedger(file: string): Set<string> {
  const out = new Set<string>()
  for (const raw of readFileSync(file, 'utf8').split(/\r?\n/)) {
    const line = raw.trim()
    if ('' === line || line.startsWith('#')) continue
    out.add(line.split('\t')[0])
  }
  return out
}


// Gather all test case directories (including sub-tests like AB12/00, AB12/01).
interface TestCase {
  id: string
  dir: string
  name: string
  hasJson: boolean
  hasError: boolean
}

function gatherTests(): TestCase[] {
  const cases: TestCase[] = []
  const entries = readdirSync(SUITE_DIR, { withFileTypes: true })
    .filter(e => e.isDirectory() && '.git' !== e.name)
    .map(e => e.name)
    .sort()

  for (const entry of entries) {
    const dir = join(SUITE_DIR, entry)

    // Check for sub-tests (00/, 01/, ...)
    const subDirs = readdirSync(dir, { withFileTypes: true })
      .filter(e => e.isDirectory() && /^\d+$/.test(e.name))
      .map(e => e.name)
      .sort()

    // Skip non-test directories (e.g. "tags" metadata directory).
    if (!existsSync(join(dir, 'in.yaml')) && !existsSync(join(dir, '==='))) {
      // Check if this is a parent of numbered sub-tests.
      const hasNumberedSubs = subDirs.length > 0 &&
        existsSync(join(dir, subDirs[0], 'in.yaml'))
      if (!hasNumberedSubs) continue
    }

    const mk = (id: string, d: string): TestCase => ({
      id,
      dir: d,
      name: existsSync(join(d, '===')) ?
        readFileSync(join(d, '==='), 'utf8').trim() : id,
      hasJson: existsSync(join(d, 'in.json')),
      hasError: existsSync(join(d, 'error')),
    })

    if (subDirs.length > 0) {
      for (const sub of subDirs) {
        const subDir = join(dir, sub)
        if (!existsSync(join(subDir, 'in.yaml'))) continue
        cases.push(mk(`${entry}/${sub}`, subDir))
      }
    } else {
      cases.push(mk(entry, dir))
    }
  }

  return cases
}


// in.json is a STREAM of JSON documents, one per YAML document — NOT a single
// JSON value. Split it into all of them; the previous runner kept only the
// first, so every multi-document case was scored on part of its expectation.
// An empty file is a stream of zero documents, which is a real expectation
// ("this input yields no document"), not a parse failure.
function parseJsonStream(raw: string): any[] {
  const docs: any[] = []
  let rest = raw.trim()

  while (0 < rest.length) {
    let depth = 0
    let inString = false
    let escape = false
    let cut = -1

    for (let i = 0; i < rest.length; i++) {
      const ch = rest[i]

      if (escape) {
        escape = false
      }
      else if (inString) {
        if ('\\' === ch) escape = true
        else if ('"' === ch) inString = false
      }
      else if ('"' === ch) {
        inString = true
      }
      else if ('{' === ch || '[' === ch) {
        depth++
      }
      else if ('}' === ch || ']' === ch) {
        depth--
      }

      if (inString || escape || 0 !== depth) continue

      // A document boundary must be end-of-input or whitespace, otherwise the
      // bare number 123 would be cut after its first digit.
      const next = rest[i + 1]
      if (undefined !== next && !/\s/.test(next)) continue

      try {
        JSON.parse(rest.slice(0, i + 1))
        cut = i + 1
        break
      }
      catch { /* not a complete value yet */ }
    }

    if (-1 === cut) {
      // Not a JSON document stream. Surface it rather than silently comparing
      // against null, which is what the old runner's "last resort" did.
      throw new Error(
        'unparseable in.json remainder: ' + JSON.stringify(rest.slice(0, 80)))
    }

    docs.push(JSON.parse(rest.slice(0, cut)))
    rest = rest.slice(cut).trim()
  }

  return docs
}


// Canonicalise a parse result for STRICT structural comparison:
// null-prototype and ordered-map objects become plain objects, and the
// non-finite numbers YAML can spell but JSON cannot become marker strings.
// Nothing here coerces between types — a string '1' stays a string and will
// NOT match the number 1.
function canon(v: any): any {
  if ('number' === typeof v && !Number.isFinite(v)) {
    return Number.isNaN(v) ? '@@NaN' : 0 < v ? '@@Infinity' : '@@-Infinity'
  }
  if (undefined === v) return '@@UNDEFINED'
  if (null === v || 'object' !== typeof v) return v
  if (Array.isArray(v)) return v.map(canon)
  const out: Record<string, any> = {}
  for (const k of Object.keys(v)) out[k] = canon(v[k])
  return out
}


function readInput(dir: string): string {
  return readFileSync(join(dir, 'in.yaml'), 'utf8').replace(/\r\n/g, '\n')
}

function show(v: any): string {
  const s = JSON.stringify(v)
  return undefined === s ? String(v) : 200 < s.length ? s.slice(0, 200) + '...' : s
}

function parse(src: string): any {
  return new Tabnas().use(jsonic).use(Yaml).parse(src)
}


// A ledger id that no longer names a case in this group would silently excuse
// a case that does not exist. Both ledgers are held to that.
function ledgerIdsAreKnown(file: string, ledger: Set<string>, cases: TestCase[]) {
  const known = new Set(cases.map(c => c.id))
  const unknown = [...ledger].filter(id => !known.has(id)).sort()
  if (0 < unknown.length) {
    throw new Error(
      file + ' lists ids that are not cases in its group: ' + unknown.join(' '))
  }
}


describe('yaml-test-suite', () => {
  const allCases = gatherTests()

  const validCases = allCases.filter(c => c.hasJson && !c.hasError)
  const errorCases = allCases.filter(c => c.hasError)
  const novalueCases = allCases.filter(c => !c.hasJson && !c.hasError)

  // Valid documents must parse AND produce the correct value, strictly, for
  // every document in the stream.
  describe('valid-parse', () => {
    for (const tc of validCases) {
      test(`${tc.id}: ${tc.name}`, () => {
        const inYaml = readInput(tc.dir)
        const docs = parseJsonStream(
          readFileSync(join(tc.dir, 'in.json'), 'utf8'))

        let actual: any
        try {
          actual = parse(inYaml)
        }
        catch (e: any) {
          throw new Error(
            `${tc.id} (${tc.name}): valid document REJECTED: ${e.message}\n` +
            `  input: ${JSON.stringify(inYaml)}`)
        }

        // A zero-document stream cannot be spelled as a JSON value at all, so
        // the assertion is "the parse yielded no document" — which this
        // plugin spells as undefined (empty input) or null (comments only).
        if (0 === docs.length) {
          assert.ok(undefined === actual || null === actual,
            `${tc.id} (${tc.name}): expected NO document (in.json is empty), ` +
            `got ${show(canon(actual))}\n` +
            `  input: ${JSON.stringify(inYaml)}`)
          return
        }

        // The plugin yields the bare value for a single-document stream and
        // an array of values for a multi-document one.
        const expected = 1 === docs.length ? docs[0] : docs

        assert.deepStrictEqual(canon(actual), canon(expected),
          `${tc.id} (${tc.name}): wrong value\n` +
          `  input:    ${JSON.stringify(inYaml)}\n` +
          `  expected: ${show(canon(expected))}\n` +
          `  actual:   ${show(canon(actual))}`)
      })
    }
  })

  describe('expected-errors', () => {
    const lenient = loadLedger(LENIENT_FILE)

    test('lenient ledger has no unknown ids', () => {
      ledgerIdsAreKnown(LENIENT_FILE, lenient, errorCases)
    })

    for (const tc of errorCases) {
      test(`${tc.id}: ${tc.name}`, () => {
        const inYaml = readInput(tc.dir)

        let threw = false
        let actual: any
        try {
          actual = parse(inYaml)
        } catch {
          threw = true
        }

        if (lenient.has(tc.id)) {
          // A documented leniency: the parser is known to accept this
          // spec-invalid input. If it now rejects it, that is progress —
          // delete the line so the case is held to the strict expectation.
          if (threw) {
            throw new Error(
              `${tc.id} (${tc.name}) is now REJECTED. Remove its line from ` +
              LENIENT_FILE + ' so the strict expectation applies from here on.')
          }
          return
        }

        // Not on the ledger: the parser must reject it.
        if (!threw) {
          throw new Error(
            `${tc.id} (${tc.name}) parsed without error, but the suite marks ` +
            'it invalid and it is not listed in ' + LENIENT_FILE + '.\n' +
            `  input:  ${JSON.stringify(inYaml)}\n` +
            `  parsed: ${show(canon(actual))}`)
        }
      })
    }
  })

  // The suite publishes no expected value for these, so the value cannot be
  // checked — but they ARE valid YAML, so "it parses" is a real assertion,
  // just a partial one. These 29 cases used to be gathered and then dropped
  // without any assertion at all.
  describe('valid-parse-novalue', () => {
    const unparsed = loadLedger(UNPARSED_FILE)

    test('unparsed ledger has no unknown ids', () => {
      ledgerIdsAreKnown(UNPARSED_FILE, unparsed, novalueCases)
    })

    for (const tc of novalueCases) {
      test(`${tc.id}: ${tc.name}`, () => {
        const inYaml = readInput(tc.dir)

        let threw = false
        try {
          parse(inYaml)
        } catch {
          threw = true
        }

        if (unparsed.has(tc.id)) {
          // A documented gap: this valid document is still rejected. If it
          // now parses, that is progress — delete the line.
          if (!threw) {
            throw new Error(
              `${tc.id} (${tc.name}) now PARSES. Remove its line from ` +
              UNPARSED_FILE + ' so the expectation applies from here on.')
          }
          return
        }

        if (threw) {
          throw new Error(
            `${tc.id} (${tc.name}): the suite says this is valid YAML but it ` +
            'was REJECTED, and it is not listed in ' + UNPARSED_FILE + '.\n' +
            `  input: ${JSON.stringify(inYaml)}`)
        }
      })
    }
  })

  // Not a conformance score — a guard that the corpus scored against is the
  // whole, expected one. Without it a truncated checkout silently shrinks the
  // denominator and every ratio above it improves for free.
  test('suite-census', () => {
    assert.strictEqual(allCases.length, 402, 'total cases')
    assert.strictEqual(validCases.length, 279, 'value-checked cases')
    assert.strictEqual(errorCases.length, 94, 'must-fail cases')
    assert.strictEqual(novalueCases.length, 29, 'parse-only cases')
  })
})
