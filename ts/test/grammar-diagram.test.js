/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */

// doc/grammar.{txt,svg} are generated from the live grammar. A grammar
// change that does not regenerate them leaves the published diagram
// describing a grammar that no longer exists, so this fails until
// `npm run diagram` is run.

const Assert = require('node:assert')
const { test } = require('node:test')

const { stale } = require('../scripts/grammar-diagram.cjs')

test('the grammar diagram matches the grammar', () => {
  const names = stale()
  Assert.deepStrictEqual(names, [],
    'stale: ' + names.map((n) => 'doc/' + n).join(', ') +
    ' (regenerate with: npm run diagram)')
})
