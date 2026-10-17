/* Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas, keyOrder } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


// With `map.ordered` on, the engine records each member its pair rule
// stores in a key-order side channel (`keyOrder`), which a reader that
// walks a mapping in source order follows: alchemy-cli walks a value so.
// A member this grammar stores itself is recorded the same way, or it
// walks after every recorded one, out of the document's order: a key with
// no value in a flow mapping, a sequence entry's pair (whose integer-like
// keys Object.keys would reorder), and a merge key's members, which follow
// the mapping's own, the `<<` itself gone from the record.
function ordered(src: string): any {
  return new Tabnas({ map: { ordered: true } }).use(jsonic).use(Yaml).parse(src)
}


describe('key order with map.ordered', () => {
  test('a key with no value in a flow mapping stands where the document put it', () => {
    assert.deepStrictEqual(keyOrder(ordered('{? a, b: 1}')), ['a', 'b'])
    assert.deepStrictEqual(keyOrder(ordered('{a, b: c, d}')), ['a', 'b', 'd'])
    assert.deepStrictEqual(keyOrder(ordered('{b: 1, ? a}')), ['b', 'a'])
  })

  test("a sequence entry's pairs keep source order, integer-like keys included", () => {
    assert.deepStrictEqual(keyOrder(ordered('- 2: x\n  1: y\n')[0]), ['2', '1'])
    assert.deepStrictEqual(keyOrder(ordered('- a: 1\n  b: 2\n')[0]), ['a', 'b'])
    assert.deepStrictEqual(keyOrder(ordered('- a:\n- b\n')[0]), ['a'])
  })

  test("a merge key's members follow the mapping's own, and << is not recorded", () => {
    const v = ordered('base: &b\n  x: 1\n  z: 3\nd:\n  <<: *b\n  y: 2\n')
    assert.deepStrictEqual(keyOrder(v.d), ['y', 'x', 'z'])
    assert.deepStrictEqual(Object.keys(v.d), ['y', 'x', 'z'])
  })

  test('without the option, no record is kept', () => {
    const v = new Tabnas().use(jsonic).use(Yaml).parse('{? a, b: 1}')
    assert.deepStrictEqual(keyOrder(v), ['a', 'b'])
  })
})
