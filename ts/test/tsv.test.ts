/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { Jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


// Helper: create a fresh Yaml-enabled Jsonic instance per test.
function y(src: string) {
  const j = Jsonic.make().use(Yaml)
  return j(src)
}


// Deep-equal comparison that ignores object key order.
function deepEqual(a: any, b: any): boolean {
  if (a === b) return true
  if (a === null || b === null) return a === b
  if (typeof a !== typeof b) return false
  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false
    return a.every((v: any, i: number) => deepEqual(v, b[i]))
  }
  if (typeof a === 'object') {
    const ka = Object.keys(a).sort()
    const kb = Object.keys(b).sort()
    if (ka.length !== kb.length) return false
    return ka.every((k: string, i: number) => k === kb[i] && deepEqual(a[k], b[k]))
  }
  return false
}


// Unescape literal \n, \r, \t sequences in TSV input fields.
function unescape(s: string): string {
  let out = ''
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && i + 1 < s.length) {
      const next = s[i + 1]
      if (next === 'n') { out += '\n'; i++; continue }
      if (next === 'r') { out += '\r'; i++; continue }
      if (next === 't') { out += '\t'; i++; continue }
      if (next === '\\') { out += '\\'; i++; continue }
    }
    out += s[i]
  }
  return out
}


// Load and parse a TSV file into test cases.
function loadTSV(filename: string): { name: string, input: string, expected: any }[] {
  const filepath = join(__dirname, '..', '..', 'test', filename)
  const content = readFileSync(filepath, 'utf8')
  const cases: { name: string, input: string, expected: any }[] = []
  for (const line of content.split('\n')) {
    if (line.trim() === '' || line.startsWith('#')) continue
    const parts = line.split('\t')
    if (parts.length < 3) continue
    const name = parts[0]
    const input = unescape(parts[1])
    const expected = JSON.parse(unescape(parts[2]))
    cases.push({ name, input, expected })
  }
  return cases
}


// Discover all .tsv files in the test directory.
const tsvDir = join(__dirname, '..', '..', 'test')
const tsvFiles = readdirSync(tsvDir).filter(f => f.endsWith('.tsv')).sort()


for (const tsvFile of tsvFiles) {
  const suiteName = tsvFile.replace('.tsv', '')
  const cases = loadTSV(tsvFile)

  describe(`tsv/${suiteName}`, () => {
    for (const tc of cases) {
      test(tc.name, () => {
        const result = y(tc.input)
        if (!deepEqual(result, tc.expected)) {
          const resultJSON = JSON.stringify(result)
          const expectedJSON = JSON.stringify(tc.expected)
          throw new Error(`Mismatch:\n  Got:  ${resultJSON}\n  Want: ${expectedJSON}`)
        }
      })
    }
  })
}
