/* Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'

// Error COLUMNS after a non-ASCII character.
//
// This port sets a token's column from its offset: how far the token
// stands past its line's start, in UTF-16 units, from 1. So a column is
// exact whatever came before it on the line. It was once kept up as each
// token was taken, a quantity added at each of forty sites, and those
// drifted: every `: ` on a line put the columns after it one too far, and
// the columns below were each one more than they are now.
//
// The GO port wrote the SAME EXPRESSIONS over BYTE indices:
//
//     pnt.cI += nameEnd     // TS: nameEnd is a UTF-16 index
//     pnt.CI += nameEnd     // Go: nameEnd is a byte index
//
// so a 2-byte `é` charged two columns, a 3-byte `€` three and an astral
// character four. The two lines look identical, which is why it survived
// a port review: a transliteration is not a port when the two languages
// index strings differently. Found by the fleet parity probe.
//
// go/column_units_test.go asserts the same sixteen cases. Fifteen agree
// exactly; the astral row is the recorded engine divergence — this port
// counts UTF-16 units (an astral character is 2), Go counts runes (1).
// See parser/DIVERGENCE.md, "Column positions for astral characters".
// Before the repair that row was off by THREE rather than one, so a live
// defect was hiding inside a difference the register already excused.
//
// Deliberately its own file: `yaml-test-suite.test.ts` needs a fetched
// corpus, and these sixteen short strings need nothing.

describe('column-units', () => {
  test('error columns count characters, not bytes', () => {
    // label, source, this port's column, Go's column
    const cases: [string, string, number, number][] = [
      // Controls: ASCII only, where every unit coincides. Without them,
      // "columns count characters" is also satisfied by never counting.
      ['inline-ascii', '{a: xx, ]', 9, 9],
      ['block-key-ascii', 'a: 1\n]', 1, 1],

      // The plain-scalar path — where the bug lived in Go.
      ['inline-latin1', '{a: é, ]', 8, 8],
      ['inline-bmp', '{a: €, ]', 8, 8],

      // A non-ASCII KEY, which reaches a different site again.
      ['key-latin1', '{é: 1, ]', 8, 8],
      ['key-bmp', '{€€: 1, ]', 9, 9],

      // Quoted scalars: their own two handlers.
      ['dq-latin1', '{a: "é", ]', 10, 10],
      ['dq-bmp', '{a: "€€", ]', 11, 11],
      ['sq-latin1', "{a: 'é', ]", 10, 10],

      // Anchors and tags: two more handlers.
      ['anchor-latin1', '{a: &x é, ]', 11, 11],
      ['tag-latin1', '{a: !!str é, ]', 14, 14],

      // The flow-collection newline skip. Not a unit error in Go but a
      // CONSTANT: it set the column to 0 — not a valid 1-based column at
      // all — where this port computes the characters since the last
      // newline, so a newline plus three spaces puts the next token at
      // column 4.
      ['flow-nl-1sp', '[a,\n }', 2, 2],
      ['flow-nl-3sp', '[a,\n   }', 4, 4],
      ['flow-nl-latin1', '[é,\n }', 2, 2],

      // The recorded divergence, and the ONLY row where the two ports
      // still differ.
      ['inline-astral', '{a: \u{1F600}, ]', 9, 8],
    ]

    for (const [label, src, col, go] of cases) {
      const j = new Tabnas().use(jsonic).use(Yaml)
      let err: any = null
      try {
        j.parse(src)
      }
      catch (e) {
        err = e
      }
      assert.ok(null != err,
        `${label}: ${JSON.stringify(src)} parsed, expected a diagnostic`)

      // Read the SERIALISED diagnostic, not the thrown object: `col` is
      // part of the JSON contract and is not an own enumerable property,
      // so `err.col` is `undefined` and an assertion against it would
      // compare nothing.
      const diag = JSON.parse(JSON.stringify(err))
      assert.equal(diag.col, col,
        `${label}: ${JSON.stringify(src)} col — Go says ${go}`)
      // And the column is the one the diagnostic's own offset gives, in
      // UTF-16 units: so a row above cannot pin a drift.
      const line = src.lastIndexOf('\n', diag.pos - 1) + 1
      assert.equal(diag.col, diag.pos - line + 1,
        `${label}: ${JSON.stringify(src)} col is not its offset's`)
    }
  })
})
