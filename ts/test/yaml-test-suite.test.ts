/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

/* Official YAML Test Suite conformance.
 *
 * Corpus: https://github.com/yaml/yaml-test-suite `data` branch, pinned at
 * commit 6ad3d2c62885d82fc349026c136ef560838fdf3d. It is NOT vendored — it is
 * fetched by ../../scripts/fetch-yaml-test-suite.sh (wired as `pretest`).
 *
 * Each case directory holds:
 *   in.yaml  — the input
 *   in.json  — the expected value, as a stream of JSON documents (valid cases)
 *   error    — a marker file: this input MUST be rejected
 *   ===      — a human-readable case name
 *
 * THREE GROUPS, ALL ASSERTED — none of them may silently pass:
 *   valid-parse       in.json present: must parse AND deep-equal that value.
 *   expected-errors   `error` present: must be REJECTED. (This half used to
 *                     be exercised and then assert nothing at all.)
 *   valid-parse-novalue
 *                     Neither file: the suite publishes no expected value for
 *                     these, so the strongest honest assertion available is
 *                     that a valid document parses without error. This is a
 *                     documented partial check, not a pass.
 *
 * The comparison is STRICT. An earlier version of this file used a
 * `deepLooseEqual` that treated the number 1 and the string "1" as equal, and
 * compared only the FIRST document of a multi-document stream. Both hid real
 * conformance failures; both are gone.
 */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { join } from 'node:path'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


const REPO_ROOT = join(__dirname, '..', '..')
const SUITE_DIR = join(REPO_ROOT, 'test', 'yaml-test-suite')
const FETCH_SCRIPT = join(REPO_ROOT, 'scripts', 'fetch-yaml-test-suite.sh')

// The corpus must be present. It is NEVER acceptable for this suite to skip:
// a conformance test that quietly does not run turns a green tick into a lie.
// Try the pinned fetch once, then fail loudly with instructions.
function requireSuite(): void {
  if (existsSync(join(SUITE_DIR, '229Q', 'in.yaml'))) return

  try {
    execFileSync('bash', [FETCH_SCRIPT], { stdio: 'inherit' })
  } catch (e: any) {
    // fall through to the hard failure below
  }

  if (!existsSync(join(SUITE_DIR, '229Q', 'in.yaml'))) {
    throw new Error(
      'yaml-test-suite corpus is MISSING at ' + SUITE_DIR + '.\n' +
      'It is deliberately not committed (third-party corpus). Fetch it with:\n' +
      '    bash scripts/fetch-yaml-test-suite.sh\n' +
      'This suite refuses to skip: without the corpus there is no conformance ' +
      'number, and a green run would be meaningless.'
    )
  }
}


interface TestCase {
  id: string
  dir: string
  name: string
  hasJson: boolean
  hasError: boolean
}

// Gather all case directories, including numbered sub-tests (AB12/00, ...).
function gatherTests(): TestCase[] {
  const cases: TestCase[] = []
  const entries = readdirSync(SUITE_DIR, { withFileTypes: true })
    .filter(e => e.isDirectory() && '.git' !== e.name)
    .map(e => e.name)
    .sort()

  for (const entry of entries) {
    const dir = join(SUITE_DIR, entry)

    const subDirs = readdirSync(dir, { withFileTypes: true })
      .filter(e => e.isDirectory() && /^\d+$/.test(e.name))
      .map(e => e.name)
      .sort()

    // Skip metadata directories ("name", "tags") that hold no cases.
    if (!existsSync(join(dir, 'in.yaml')) && !existsSync(join(dir, '==='))) {
      const hasNumberedSubs = 0 < subDirs.length &&
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

    if (0 < subDirs.length) {
      for (const sub of subDirs) {
        const subDir = join(dir, sub)
        if (!existsSync(join(subDir, 'in.yaml'))) continue
        cases.push(mk(`${entry}/${sub}`, subDir))
      }
    }
    else {
      cases.push(mk(entry, dir))
    }
  }

  return cases
}


// in.json is a STREAM of JSON documents, one per YAML document. Split it into
// all of them — the previous runner kept only the first, which meant every
// multi-document case was scored on a fraction of its expected output.
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

      // A document boundary must be end-of-input or whitespace, otherwise
      // the bare number 123 would be cut after its first digit.
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
      // Whatever is left is not parseable as a JSON document stream. Surface
      // it rather than silently comparing against null.
      throw new Error('unparseable in.json remainder: ' + JSON.stringify(rest.slice(0, 80)))
    }

    docs.push(JSON.parse(rest.slice(0, cut)))
    rest = rest.slice(cut).trim()
  }

  return docs
}


// Canonicalise a parse result for structural comparison: null-prototype and
// OrderedMap objects become plain objects, and YAML's non-finite numbers
// (which JSON cannot spell) become marker strings. Nothing here coerces
// between types — a string "1" stays a string and will not match the number 1.
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


// Top-level, NOT inside describe(): a throw here fails the whole test FILE to
// load, which node:test reports and exits non-zero. A throw inside a
// describe() body prints a stack trace and still exits 0 — the exact "green
// tick that is a lie" this suite exists to prevent.
requireSuite()


describe('yaml-test-suite', () => {
  const allCases = gatherTests()

  assert.ok(300 < allCases.length,
    `yaml-test-suite: only ${allCases.length} cases discovered — the corpus ` +
    'is truncated or the layout changed; refusing to report a bogus number.')

  const validCases = allCases.filter(c => c.hasJson && !c.hasError)
  const errorCases = allCases.filter(c => c.hasError)
  const novalueCases = allCases.filter(c => !c.hasJson && !c.hasError)

  // Valid documents must parse AND produce the correct value.
  describe('valid-parse', () => {
    for (const tc of validCases) {
      test(`${tc.id}: ${tc.name}`, () => {
        const inYaml = readInput(tc.dir)
        const docs = parseJsonStream(readFileSync(join(tc.dir, 'in.json'), 'utf8'))

        // The plugin yields the bare value for a single-document stream and
        // an array of values for a multi-document one.
        const expected = 1 === docs.length ? docs[0] : docs

        let actual: any
        try {
          actual = new Tabnas().use(jsonic).use(Yaml).parse(inYaml)
        }
        catch (e: any) {
          throw new Error(
            `${tc.id} (${tc.name}): valid document REJECTED: ${e.message}\n` +
            `  input:    ${JSON.stringify(inYaml)}\n` +
            `  expected: ${show(expected)}`)
        }

        assert.deepStrictEqual(canon(actual), canon(expected),
          `${tc.id} (${tc.name}): wrong value\n` +
          `  input:    ${JSON.stringify(inYaml)}\n` +
          `  expected: ${show(canon(expected))}\n` +
          `  actual:   ${show(canon(actual))}`)
      })
    }
  })

  // Invalid documents must be REJECTED. This block previously ran the parse
  // and then asserted nothing whatsoever, so all 94 cases "passed" forever.
  describe('expected-errors', () => {
    for (const tc of errorCases) {
      test(`${tc.id}: ${tc.name}`, () => {
        const inYaml = readInput(tc.dir)

        let threw = false
        let actual: any
        try {
          actual = new Tabnas().use(jsonic).use(Yaml).parse(inYaml)
        }
        catch {
          threw = true
        }

        assert.ok(threw,
          `${tc.id} (${tc.name}): invalid document ACCEPTED (must be rejected)\n` +
          `  input:  ${JSON.stringify(inYaml)}\n` +
          `  parsed: ${show(canon(actual))}`)
      })
    }
  })

  // No expected value is published for these, so the value cannot be checked.
  // They are still valid YAML, so the parse itself must succeed — that is a
  // real assertion, just a partial one. Recorded as a known coverage gap.
  describe('valid-parse-novalue (no in.json in the suite: parse-only check)', () => {
    for (const tc of novalueCases) {
      test(`${tc.id}: ${tc.name}`, () => {
        const inYaml = readInput(tc.dir)
        assert.doesNotThrow(
          () => new Tabnas().use(jsonic).use(Yaml).parse(inYaml),
          `${tc.id} (${tc.name}): valid document REJECTED\n` +
          `  input: ${JSON.stringify(inYaml)}`)
      })
    }
  })

  test('suite-census', () => {
    // Not a pass/fail conformance number — a guard that the corpus we scored
    // against is the pinned one. If these move, the fetch pin moved with them.
    assert.strictEqual(allCases.length, 402, 'total cases')
    assert.strictEqual(validCases.length, 279, 'value-checked cases')
    assert.strictEqual(errorCases.length, 94, 'must-fail cases')
    assert.strictEqual(novalueCases.length, 29, 'parse-only cases')
  })
})
