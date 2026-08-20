/* Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'

// Error COLUMNS after a non-ASCII character.
//
// This port advances `pnt.cI` by UTF-16 index quantities, which count
// characters. The GO port wrote the SAME EXPRESSIONS over BYTE indices
// across forty `pnt.CI` sites:
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
      ['inline-ascii', '{a: xx, ]', 10, 10],
      ['block-key-ascii', 'a: 1\n]', 1, 1],

      // The plain-scalar path — where the bug lived in Go.
      ['inline-latin1', '{a: é, ]', 9, 9],
      ['inline-bmp', '{a: €, ]', 9, 9],

      // A non-ASCII KEY, which reaches a different site again.
      ['key-latin1', '{é: 1, ]', 9, 9],
      ['key-bmp', '{€€: 1, ]', 10, 10],

      // Quoted scalars: their own two handlers.
      ['dq-latin1', '{a: "é", ]', 11, 11],
      ['dq-bmp', '{a: "€€", ]', 12, 12],
      ['sq-latin1', "{a: 'é', ]", 11, 11],

      // Anchors and tags: two more handlers.
      ['anchor-latin1', '{a: &x é, ]', 12, 12],
      ['tag-latin1', '{a: !!str é, ]', 15, 15],

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
      ['inline-astral', '{a: \u{1F600}, ]', 10, 9],
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
    }
  })
})
