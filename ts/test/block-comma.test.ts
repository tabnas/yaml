/* Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


function y(src: string) {
  return new Tabnas().use(jsonic).use(Yaml).parse(src)
}

function eq(actual: any, expected: any) {
  const norm = (v: any) => undefined === v ? '@@UNDEFINED' :
    JSON.parse(JSON.stringify(v ?? null))
  assert.deepStrictEqual(norm(actual), norm(expected))
}


// A COMMA IS NOT A SEPARATOR IN BLOCK CONTEXT.
//
// Flow indicators only indicate inside a flow collection, so in block context
// `example: 1,2,3` is the plain scalar "1,2,3".
//
// Only a comma at END of line was accepted before, so `1,2,3` fell through to
// the number matcher: it took the `1`, the `,` became a structural token, and
// the parse died on the `2` with "unexpected character(s): 3". Intercom's
// OpenAPI document carries `example: 1,2,3` and did not parse at all.

describe('block-comma', () => {

  test('digits with internal commas are one plain scalar', () => {
    eq(y('a: 1,2,3'), { a: '1,2,3' })
    eq(y('a: 1, 2'), { a: '1, 2' })
    eq(y('a: 1,2,3\nb: 4'), { a: '1,2,3', b: 4 })
  })

  test('a trailing comma still works', () => {
    eq(y('a: 12,'), { a: '12,' })
  })

  test('digits then a comma then words', () => {
    eq(y('a: 12, hexadecimal.'), { a: '12, hexadecimal.' })
    eq(y('a: 64 characters, hexadecimal.'), { a: '64 characters, hexadecimal.' })
  })

  test('in FLOW context a comma is still a separator', () => {
    eq(y('a: [1,2,3]'), { a: [1, 2, 3] })
    eq(y('a: {x: 1, y: 2}'), { a: { x: 1, y: 2 } })
    eq(y('a: [1, 2, 3]'), { a: [1, 2, 3] })
  })

  test('plain numbers are still numbers', () => {
    eq(y('a: 12'), { a: 12 })
    eq(y('a: 1.5'), { a: 1.5 })
    eq(y('a: -3'), { a: -3 })
  })

  test('the shape that broke intercom', () => {
    eq(y([
      'parameters:',
      '- name: tag_ids',
      '  in: query',
      '  example: 1,2,3',
      '  schema:',
      '    type: array',
    ].join('\n')), {
      parameters: [
        { name: 'tag_ids', in: 'query', example: '1,2,3', schema: { type: 'array' } }
      ]
    })
  })

})
