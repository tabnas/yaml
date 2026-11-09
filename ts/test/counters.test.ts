/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


// THE RULE PASSES NAME ANCHORS AND MERGED KEYS.
//
// A host that reads the rule passes (tabnas/transduce's
// ParserEventSource, and YAML's transducer part over it) sees no anchor:
// the lexer reads `&name` and hands the name to the val that claims it.
// To write an alias's value again, the part keeps the events of the
// anchored value, so the val that claims an anchor names it in its
// counters: `&name`, whose value is the val's own instance number
// (`rule.i`), and `yamlAnchored`, of the same value, which marks the val,
// on its open and its close pass. A counter passes to the rules below the
// val with the val's number, so the anchored val is the one whose
// `yamlAnchored` is its own `i`. And a merge key's members are folded
// into its mapping at the map's close, after the passes: the map names
// each key it merged in its counters, `<<key`, on its close pass, whose
// value is the number of the source it took the key from (1 for the
// mapping a `<<` names; for a sequence, the place of the mapping among
// the sequence's mappings, from 1), so the part writes those members of
// those sources, and no other, as the mapping's last. The Go and Rust
// twins are go/counters_test.go and rs/tests/counters_test.rs.

// What the passes name: the anchors each val names as its own, in the
// order the vals open, and the keys each map names as merged at its
// close, each with its source, sorted, in the order the maps close.
function named(src: string): { anchors: string[], merged: [string, number][][] } {
  const j = new Tabnas().use(jsonic).use(Yaml)
  const anchors: string[] = []
  const merged: [string, number][][] = []
  j.sub({
    ruleDone: (rule: any, _ctx: any, done: any) => {
      const n = rule.n
      const counters = Object.keys(n)
      if ('val' === rule.name && 'o' === done.state) {
        const own = counters.filter((k) => k.startsWith('&') && n[k] === rule.i).map((k) => k.slice(1))
        assert.strictEqual(n.yamlAnchored === rule.i, 0 < own.length, `val ${rule.i}: ${JSON.stringify(n)}`)
        anchors.push(...own)
      }
      if ('map' === rule.name && 'c' === done.state) {
        const keys = counters.filter((k) => k.startsWith('<<')).map((k) => k.slice(2))
        if (0 < keys.length) merged.push(keys.sort().map((k): [string, number] => [k, n['<<' + k]]))
      }
    },
  })
  j.parse(src)
  return { anchors, merged }
}


describe('counters', () => {

  test('an anchored val names its anchors on its own passes', () => {
    assert.deepStrictEqual(named('a: &x [1, 2]\nb: &y\n  p: 1\nc: *x\nd: &z 3\n').anchors, ['x', 'y', 'z'])
    assert.deepStrictEqual(named('a: &o [&i [1], 2]\nb: *o\n').anchors, ['o', 'i'])
    assert.deepStrictEqual(named('- &a\n  - 1\n- *a\n').anchors, ['a'])
    assert.deepStrictEqual(named('a: [1, 2]\nb: {c: 3}\n').anchors, [])
  })

  test('the anchored val names the anchor on its close pass too', () => {
    const j = new Tabnas().use(jsonic).use(Yaml)
    const passes: string[] = []
    j.sub({
      ruleDone: (rule: any, _ctx: any, done: any) => {
        if ('val' === rule.name && rule.i === rule.n['&x'] && rule.i === rule.n.yamlAnchored) passes.push(done.state)
      },
    })
    j.parse('a: &x {p: [1]}\nb: *x\n')
    assert.deepStrictEqual(passes, ['o', 'c'])
  })

  test('a map names the keys its merge brought, and only those, with their sources', () => {
    assert.deepStrictEqual(named('a: &a {x: 1, y: 1}\nb:\n  y: 2\n  <<: *a\n').merged, [[['x', 1]]])
    assert.deepStrictEqual(
      named('a: &a {x: 1}\nb: &b {y: 2, x: 3}\nc: {<<: [*a, *b], z: 3}\n').merged,
      [[['x', 1], ['y', 2]]],
    )
    // Only a sequence's mappings are sources, and only they are counted.
    assert.deepStrictEqual(
      named('a: &a {x: 1}\nb: &b {y: 2, x: 3}\nc: {<<: [1, {}, *a, [w], *b]}\n').merged,
      [[['x', 2], ['y', 3]]],
    )
    assert.deepStrictEqual(named('a: &a {x: 1}\nb:\n  x: 2\n  <<: *a\n').merged, [])
    assert.deepStrictEqual(named('"<<": {x: 1}\nb: 2\n').merged, [])
    assert.deepStrictEqual(named('- <<: {x: 1}\n  b: 2\n').merged, [])
    assert.deepStrictEqual(named('a: {<<: 1, b: 2}\n').merged, [])
  })
})
