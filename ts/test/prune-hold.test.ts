/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


// AN ANCHORED VALUE HOLDS A STREAMING HOST'S PRUNING UNTIL ITS ANCHOR IS
// RECORDED (tabnas/yaml#138).
//
// A host that streams a document through tabnas/transduce's
// ParserEventSource empties each array behind the stream once the close
// pass of the rule that built it has gone by, so that a long document's
// tree holds no record the stream has passed. An anchor is recorded only
// when its value closes, after the arrays inside it have closed, so the
// record held them emptied, and an alias used as a key named the emptied
// text: `a: &l [1, 2]` then `*l: x` read the key as "" where the whole
// parse reads "1,2". While an anchored value is open the grammar raises
// `pruneHold` in the parse context's bag, a count of the open anchored
// values, and lowers it once the anchor is recorded; a host that honours
// it empties no array while it is above zero. `pruned` empties arrays as
// such a host does, in place, and records the hold each array's close
// pass saw. The Go and Rust twins are go/prune_hold_test.go and
// rs/tests/prune_hold_test.rs.

function pruned(src: string): { value: any, holds: number[] } {
  const j = new Tabnas().use(jsonic).use(Yaml)
  const holds: number[] = []
  j.sub({
    ruleDone: (rule: any, ctx: any, done: any) => {
      if ('o' === done.state || !Array.isArray(rule.node)) {
        return
      }
      const hold = 'number' === typeof ctx.u.pruneHold ? ctx.u.pruneHold : 0
      holds.push(hold)
      if (0 === hold) {
        rule.node.length = 0
      }
    },
  })
  return { value: JSON.parse(JSON.stringify(j.parse(src))), holds }
}


describe('prune-hold', () => {

  test('an alias key to an anchored sequence survives pruning', () => {
    const cases: Array<[string, Record<string, string>]> = [
      ['a: &l [1, 2]\n*l: x\n', { '1,2': 'x' }],
      ['a: &o [&i [1, 2], 3]\n*o: x\n*i: y\n', { '1,2,3': 'x', '1,2': 'y' }],
    ]
    for (const [src, keys] of cases) {
      const { value, holds } = pruned(src)
      for (const [key, want] of Object.entries(keys)) {
        assert.strictEqual(value[key], want, `${JSON.stringify(src)}: ${JSON.stringify(value)}`)
      }
      assert.ok(!('' in value), JSON.stringify(value))
      assert.ok(holds.some((h) => h > 0), JSON.stringify(holds))
      assert.strictEqual(holds[holds.length - 1], 0, `released: ${JSON.stringify(holds)}`)
    }
  })

  test('nested anchors raise the hold twice', () => {
    const { holds } = pruned('a: &o [&i [1, 2], 3]\n*o: x\n*i: y\n')
    assert.strictEqual(Math.max(...holds), 2, JSON.stringify(holds))
  })

  // A merge key's sources are merged at its mapping's close, after the
  // sequence that lists them has closed: its pair holds the pruning from
  // its open to its close, so the sequence is merged whole, and the
  // sequence after it is emptied as any other.
  test('a merge key holds pruning until its sources are read', () => {
    const { value, holds } = pruned('a: &a {x: 1}\nb: &b {y: 2}\nc:\n  <<: [*a, *b]\n  z: [3]\n')
    assert.deepStrictEqual(value.c, { z: [], x: 1, y: 2 }, JSON.stringify(value))
    assert.ok(holds.some((h) => h > 0), JSON.stringify(holds))
    assert.strictEqual(holds[holds.length - 1], 0, `released: ${JSON.stringify(holds)}`)
  })

  test('a document with no anchor holds nothing', () => {
    const { value, holds } = pruned('a: [1, [2, 3]]\nb: [4]\n')
    assert.ok(0 < holds.length && holds.every((h) => 0 === h), JSON.stringify(holds))
    assert.deepStrictEqual(value, { a: [], b: [] })
  })
})
