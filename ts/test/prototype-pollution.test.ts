/* Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License */

// PROTOTYPE POLLUTION
//
// Every map keyed by a name the *document* controls must be allocated without
// a prototype, the way the core allocates nodes (@tabnas/parser builtins:
// "no prototype, like JSON"). On a plain {} literal the name __proto__ is not
// an ordinary key: reading it yields Object.prototype and writing it runs the
// Object.prototype setter. That turns a parsed document into a write onto
// Object.prototype, visible to every object in the process.
//
// These tests pin both halves: no global pollution, and __proto__ surviving
// as an ordinary key (which is what jsonic, json5 and zon already do).
//
// Expectations are compared as JSON TEXT on purpose. The same hazard applies
// to the test itself: in a JavaScript object literal `{ __proto__: x }` sets
// the prototype and defines no key at all, so an expected literal written
// that way silently asserts the wrong thing.

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'

const p = (src: string) => new Tabnas().use(jsonic).use(Yaml).parse(src)

// Fails loudly rather than leaking a poisoned prototype into sibling tests.
function noGlobalPollution() {
  const leaked = ['pwned', 'polluted', 'injected']
    .filter((k) => undefined !== ({} as any)[k])
  for (const k of leaked) delete (Object.prototype as any)[k]
  assert.deepEqual(leaked, [], 'Object.prototype was polluted: ' + leaked)
}

describe('prototype-pollution', () => {

  test('anchor named __proto__ round-trips a scalar', () => {
    const out = p('a: &__proto__ 1\nb: *__proto__\n')
    noGlobalPollution()
    assert.deepEqual(out, { a: 1, b: 1 })
  })

  test('anchor named __proto__ does not reparent the anchor table', () => {
    // The alias *toString was never defined. Before the fix the map anchored
    // as __proto__ became the anchor table's prototype, so this resolved to
    // the injected value instead of staying undefined.
    const out = p('a: &__proto__\n  toString: pwned\nb: *toString\n')
    noGlobalPollution()
    assert.equal(out.b, null)
  })

  test('key named __proto__ is an ordinary key', () => {
    const out = p('__proto__:\n  polluted: yes\n')
    noGlobalPollution()
    assert.equal(JSON.stringify(out), '{"__proto__":{"polluted":true}}')
  })

})
