/* Copyright (c) 2025 Richard Rodger and other contributors, MIT License */

// Cross-runtime conformance, driven by the shared `test/spec/*.tsv` fixtures
// at the repo root (see ../../test/AGENTS.md).
//
// The fixture loader, the escape codec, the `ERROR:<code>` contract and the
// row loop all come from @tabnas/support, whose Go and Rust halves
// `go/parity_test.go` and `rs/tests/parity_test.rs` use to run the SAME
// files — so the implementations cannot drift without one of them going
// red, and neither can the loaders.
//
// What is left here is only what is specific to yaml: how to build the
// parser for a row's options, the non-finite-number encoding, and the
// opt-in key-order check.

import { describe } from 'node:test'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import {
  SpecRow, findSpecDir, isErrorExpect, makeRunner, parseExpect,
} from '@tabnas/support'

import { Yaml } from '../dist/yaml'

// YAML has non-finite numbers (.inf / .nan) that JSON cannot spell, and they
// can appear at any depth. Fixtures encode them as the marker strings
// "@@Infinity", "@@-Infinity" and "@@NaN"; this maps a parse result into the
// same encoding so the two sides compare structurally. See
// ../../test/AGENTS.md.
//
// As the runner's `normalize` hook it is applied to every node on BOTH
// sides, outermost first — so it must leave an already-encoded marker
// alone, which it does: a string is returned unchanged.
function canon(v: any): any {
  if ('number' === typeof v && !Number.isFinite(v)) {
    return Number.isNaN(v) ? '@@NaN' : 0 < v ? '@@Infinity' : '@@-Infinity'
  }
  return v
}

// KEY ORDER is opt-in, row by row. The runner's comparison ignores it:
// ADR-15 keeps key order out of the value contract, because a JavaScript
// object puts an integer-like key first, in numeric order, whatever the
// source says. Where the order IS the behaviour under test, such as where
// a merge key puts the keys it brings in, the row writes `ordered` in a
// `keys` column. Then every mapping on BOTH sides gains a `@@keys` member
// listing its keys in order, and the comparison sees that list as an
// array, whose order counts. The Go and Rust runners do the same, so the
// row pins the order in every runtime. See ../../test/AGENTS.md.
const KEYS = '@@keys'

function keysOrdered(row: SpecRow): boolean {
  const cell = row.named('keys')
  if ('' === cell) return false
  if ('ordered' === cell) return true
  throw new Error(`${row.where()}: keys cell ${JSON.stringify(cell)}: ` +
    'write "ordered" or leave it empty')
}

// A copy of `v` in which every mapping lists its keys under `@@keys`. A
// key this cannot order the same way in every runtime is refused rather
// than compared: an integer-like key, which JavaScript reorders, and
// `@@keys` itself.
function withKeyOrder(v: any): any {
  if (Array.isArray(v)) {
    return v.map(withKeyOrder)
  }
  if (null == v || 'object' !== typeof v) {
    return v
  }
  const keys = Object.keys(v)
  const out: any = Object.create(null)
  for (const k of keys) {
    if (KEYS === k || /^(0|[1-9][0-9]*)$/.test(k)) {
      throw new Error(`the key ${JSON.stringify(k)} cannot be ordered ` +
        'the same way in every runtime, so a `keys: ordered` row cannot ' +
        'hold it (ADR-15)')
    }
    out[k] = withKeyOrder(v[k])
  }
  out[KEYS] = keys
  return out
}

function parseRow(input: string, row: SpecRow, tn: Tabnas): any {
  const got = tn.parse(input)
  // An ERROR row is the runner's business: a parse that should have
  // failed is reported as the value it returned.
  if (isErrorExpect(row.named('expected')) || !keysOrdered(row)) {
    return got
  }
  return withKeyOrder(got)
}

// Input that yields no value at all cannot be spelled in JSON, and it is a
// different result from a document whose value is null — so the fixtures
// write the bare token UNDEFINED, and a row that says `null` still must
// not be satisfied by a parse that produced nothing.
function parseExpectedRow(expected: string, row: SpecRow): any {
  const want = 'UNDEFINED' === expected ? undefined : parseExpect(expected)
  return keysOrdered(row) ? withKeyOrder(want) : want
}

function yamlFor(row: SpecRow): Tabnas {
  const opts = row.named('opts')
  return new Tabnas()
    .use(jsonic)
    .use(Yaml, '' === opts.trim() ? {} : JSON.parse(opts))
}

makeRunner({
  // A fresh Tabnas per row: the `opts` column is per-case, and plugin
  // options must not leak from one row into the next.
  parse: (input, row) => parseRow(input, row, yamlFor(row)),
  parseExpected: parseExpectedRow,
  normalize: canon,
})
  // `findSpecDir` walks up from this file — `dist-test/` at runtime — to the
  // repo root's `test/spec`, so moving the suite does not mean recounting
  // `..` hops. `dir` then auto-discovers every fixture in it, so adding a
  // .tsv runs it in every runtime without touching any runner.
  .dir(findSpecDir(__dirname))


// The same fixtures again, each through a parser that has had an options()
// call naming nothing. Twin of the "after an unrelated SetOptions" pass in
// go/parity_test.go: the Go port lost its number and text checks to
// exactly such a call (#81).
describe('after an unrelated options() call', () => {
  makeRunner({
    parse: (input, row) => {
      const tn = yamlFor(row)
      tn.options({})
      return parseRow(input, row, tn)
    },
    parseExpected: parseExpectedRow,
    normalize: canon,
  }).dir(findSpecDir(__dirname))
})
