/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


// A MAPPING'S MEMBER IS NAMED BEFORE ITS VALUE IS BUILT.
//
// A consumer that follows the engine's rule events, as tabnas/transduce's
// rule-event adapter does to stream a parse, learns of a mapping when the
// open pass of the rule that made it ends, and of a member's key when a
// LATER open pass on that open mapping ends with the key in `u.key`: the
// pass of a pair rule, which then pushes the rule that builds the value.
// jsonic's `pair` is such a rule, and so is `yamlElemPair`.
//
// A mapping that starts in a sequence entry (`- a: ...`, `[a: ...]`) used
// to name its first member in the pass that opened it, `yamlElemMap`'s, so
// that consumer was never told the key: a first member whose value is a
// collection opened its value ahead of its key, and the adapter refused
// the document (YAML Test Suite V9D5, tabnas/yaml#105). `yamlElemMap` now
// only opens the mapping, and `yamlElemPair` reads every pair, the first
// included. The parsed values do not change.
//
// `told` records what such a consumer is told, in order: `open mN` when a
// mapping first shows at the end of an open pass, and `mN key K` when a
// later open pass on the innermost open mapping carries the key K. A
// mapping stays open until a close pass at its depth, on it, that does
// not replace the rule. Sequences are left out, as in the Go and Rust
// twins of this test (go/key_before_value_test.go,
// rs/tests/key_before_value_test.rs): a Go sequence is a slice, which has
// no identity to follow.

function told(src: string): { value: any, told: string[] } {
  const j = new Tabnas().use(jsonic).use(Yaml)
  const open: { node: object, d: number, name: string }[] = []
  const lines: string[] = []
  let made = 0
  j.sub({
    ruleDone: (rule: any, _ctx: any, done: any) => {
      const node = rule.node
      if (null == node || 'object' !== typeof node || Array.isArray(node)) {
        return
      }
      const top = open[open.length - 1]
      if ('o' === done.state) {
        if (!open.some((m) => m.node === node)) {
          const name = 'm' + made++
          open.push({ node, d: rule.d, name })
          lines.push('open ' + name)
        } else if (top.node === node && 'string' === typeof rule.u.key) {
          lines.push(top.name + ' key ' + rule.u.key)
        }
      } else if (!done.alt?.r && top && top.node === node && top.d === rule.d) {
        open.pop()
      }
    },
  })
  const value = JSON.parse(JSON.stringify(j.parse(src)))
  return { value, told: lines }
}


describe('key-before-value', () => {

  test('an explicit key that is a mapping, with a mapping value (V9D5)', () => {
    assert.deepStrictEqual(told('- sun: yellow\n- ? earth: blue\n  : moon: white\n'), {
      value: [{ sun: 'yellow' }, { 'earth: blue': { moon: 'white' } }],
      told: [
        'open m0', 'm0 key sun',
        'open m1', 'm1 key earth: blue',
        'open m2', 'm2 key moon',
      ],
    })
  })

  test('an explicit key whose value is a sequence', () => {
    assert.deepStrictEqual(told('- ? a\n  : - b\n    - c\n'), {
      value: [{ a: ['b', 'c'] }],
      told: ['open m0', 'm0 key a'],
    })
  })

  test('an explicit key whose value is a flow collection', () => {
    assert.deepStrictEqual(told('- ? a\n  : {b: 1}\n'), {
      value: [{ a: { b: 1 } }],
      told: ['open m0', 'm0 key a', 'open m1', 'm1 key b'],
    })
    assert.deepStrictEqual(told('- ? a\n  : [b, c]\n'), {
      value: [{ a: ['b', 'c'] }],
      told: ['open m0', 'm0 key a'],
    })
  })

  test('an explicit key that is a flow collection', () => {
    assert.deepStrictEqual(told('- ? [a, b]\n  : c\n'), {
      value: [{ '[a, b]': 'c' }],
      told: ['open m0', 'm0 key [a, b]'],
    })
  })

  test('an explicit key with no value', () => {
    assert.deepStrictEqual(told('- ? a\n'), {
      value: [{ a: null }],
      told: ['open m0', 'm0 key a'],
    })
  })

  test('an implicit key whose value is a collection', () => {
    assert.deepStrictEqual(told('- a:\n    b: 1\n'), {
      value: [{ a: { b: 1 } }],
      told: ['open m0', 'm0 key a', 'open m1', 'm1 key b'],
    })
    assert.deepStrictEqual(told('- a:\n  - x\n'), {
      value: [{ a: ['x'] }],
      told: ['open m0', 'm0 key a'],
    })
  })

  test('a mapping in a flow sequence entry', () => {
    for (const src of ['[a: {b: 1}]', '[? a : {b: 1}]']) {
      assert.deepStrictEqual(told(src), {
        value: [{ a: { b: 1 } }],
        told: ['open m0', 'm0 key a', 'open m1', 'm1 key b'],
      }, src)
    }
  })

  test('every later pair is named as the first is', () => {
    assert.deepStrictEqual(told('- a: 1\n  b:\n    c: 2\n'), {
      value: [{ a: 1, b: { c: 2 } }],
      told: ['open m0', 'm0 key a', 'm0 key b', 'open m1', 'm1 key c'],
    })
  })

  test('a mapping outside a sequence was named first already', () => {
    assert.deepStrictEqual(told('? earth: blue\n: moon: white\n'), {
      value: { 'earth: blue': { moon: 'white' } },
      told: ['open m0', 'm0 key earth: blue', 'open m1', 'm1 key moon'],
    })
  })

})
