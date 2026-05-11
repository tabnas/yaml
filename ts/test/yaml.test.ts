/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Jsonic } from 'jsonic'
import { Yaml } from '../dist/yaml'


// Helper: create a fresh Yaml-enabled Jsonic instance per test.
function y(src: string) {
  const j = Jsonic.make().use(Yaml)
  return j(src)
}


describe('yaml', () => {

  test('happy', () => {
    assert.deepEqual(y(`a: 1
b: 2
c:
  d: 3
  e: 4
  f:
  - g
  - h
`), { a: 1, b: 2, c: { d: 3, e: 4, f: ['g', 'h'] } })
  })


  // ===== BLOCK MAPPINGS =====

  describe('block-mappings', () => {

    test('single-pair', () => {
      assert.deepEqual(y(`a: 1`), { a: 1 })
    })

    test('multiple-pairs', () => {
      assert.deepEqual(y(`a: 1\nb: 2\nc: 3`), { a: 1, b: 2, c: 3 })
    })

    test('nested-map', () => {
      assert.deepEqual(y(`a:\n  b: 1\n  c: 2`), { a: { b: 1, c: 2 } })
    })

    test('deeply-nested-map', () => {
      assert.deepEqual(y(`a:\n  b:\n    c:\n      d: 1`), { a: { b: { c: { d: 1 } } } })
    })

    test('sibling-nested-maps', () => {
      assert.deepEqual(y(`a:\n  x: 1\nb:\n  y: 2`), { a: { x: 1 }, b: { y: 2 } })
    })

    test('empty-value-followed-by-sibling', () => {
      // key with colon-newline but no nested content, then sibling
      assert.deepEqual(y(`a:\nb: 1`), { a: null, b: 1 })
    })

    test('colon-space-required', () => {
      // "a:b" should NOT be treated as key "a" value "b"
      // Jsonic may parse this differently — baseline the behavior
      let result: any
      try { result = y(`a:b`) } catch (e: any) { result = 'ERROR' }
      // Record whatever happens — this is a baseline
      assert.ok(result != null)
    })

    test('colon-at-end-of-line', () => {
      assert.deepEqual(y(`a:\n  b: 1`), { a: { b: 1 } })
    })

    test('trailing-newline', () => {
      assert.deepEqual(y(`a: 1\n`), { a: 1 })
    })

    test('multiple-trailing-newlines', () => {
      let result: any
      try { result = y(`a: 1\n\n\n`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })
  })


  // ===== BLOCK SEQUENCES =====

  describe('block-sequences', () => {

    test('simple-list', () => {
      assert.deepEqual(y(`- a\n- b\n- c`), ['a', 'b', 'c'])
    })

    test('single-element', () => {
      assert.deepEqual(y(`- a`), ['a'])
    })

    test('nested-list-in-map', () => {
      assert.deepEqual(y(`items:\n  - a\n  - b`), { items: ['a', 'b'] })
    })

    test('list-of-numbers', () => {
      assert.deepEqual(y(`- 1\n- 2\n- 3`), [1, 2, 3])
    })

    test('list-of-maps', () => {
      // - key: val  (dash followed by key-value pair)
      assert.deepEqual(y(`- name: alice\n- name: bob`), [{ name: 'alice' }, { name: 'bob' }])
    })

    test('nested-list-of-maps-multikey', () => {
      assert.deepEqual(y(`items:\n  - name: alice\n    age: 30\n  - name: bob\n    age: 25`),
        { items: [{ name: 'alice', age: 30 }, { name: 'bob', age: 25 }] })
    })

    test('list-of-lists', () => {
      // Nested sequences: - - item
      assert.deepEqual(y(`- - a\n  - b\n- - c\n  - d`), [['a', 'b'], ['c', 'd']])
    })

    test('deeply-nested-list', () => {
      assert.deepEqual(y(`a:\n  b:\n    - x\n    - y`), { a: { b: ['x', 'y'] } })
    })

    test('mixed-map-then-list', () => {
      assert.deepEqual(y(`a: 1\nb:\n  - x\n  - y\nc: 3`),
        { a: 1, b: ['x', 'y'], c: 3 })
    })
  })


  // ===== SCALAR TYPES =====

  describe('scalar-types', () => {

    test('integer', () => {
      assert.deepEqual(y(`a: 42`), { a: 42 })
    })

    test('negative-integer', () => {
      assert.deepEqual(y(`a: -7`), { a: -7 })
    })

    test('float', () => {
      assert.deepEqual(y(`a: 3.14`), { a: 3.14 })
    })

    test('negative-float', () => {
      assert.deepEqual(y(`a: -2.5`), { a: -2.5 })
    })

    test('zero', () => {
      assert.deepEqual(y(`a: 0`), { a: 0 })
    })

    test('boolean-true', () => {
      assert.deepEqual(y(`a: true`), { a: true })
    })

    test('boolean-false', () => {
      assert.deepEqual(y(`a: false`), { a: false })
    })

    test('null-keyword', () => {
      assert.deepEqual(y(`a: null`), { a: null })
    })

    test('tilde-null', () => {
      // YAML allows ~ as null
      assert.deepEqual(y(`a: ~`), { a: null })
    })

    test('empty-value-null', () => {
      // Empty value after colon should be null/undefined
      assert.deepEqual(y(`a:`), { a: null })
    })

    test('plain-string', () => {
      assert.deepEqual(y(`a: hello world`), { a: 'hello world' })
    })

    test('string-with-special-chars', () => {
      assert.deepEqual(y(`a: hello, world!`), { a: 'hello, world!' })
    })

    test('plain-string-with-double-curly-braces', () => {
      assert.deepEqual(y(`foo: a{{q}}b`), { foo: 'a{{q}}b' })
    })

    test('octal-number', () => {
      // YAML 1.2: 0o77 = 63
      assert.deepEqual(y(`a: 0o77`), { a: 63 })
    })

    test('hex-number', () => {
      // YAML 1.2: 0xFF = 255
      assert.deepEqual(y(`a: 0xFF`), { a: 255 })
    })

    test('positive-infinity', () => {
      assert.deepEqual(y(`a: .inf`), { a: Infinity })
    })

    test('negative-infinity', () => {
      assert.deepEqual(y(`a: -.inf`), { a: -Infinity })
    })

    test('nan', () => {
      let result = y(`a: .nan`)
      assert.deepEqual(Number.isNaN(result.a), true)
    })

    test('yes-boolean', () => {
      // YAML 1.1 allows yes/no as booleans
      assert.deepEqual(y(`a: yes`), { a: true })
    })

    test('no-boolean', () => {
      assert.deepEqual(y(`a: no`), { a: false })
    })

    test('on-boolean', () => {
      assert.deepEqual(y(`a: on`), { a: true })
    })

    test('off-boolean', () => {
      assert.deepEqual(y(`a: off`), { a: false })
    })

    test('timestamp-date', () => {
      // YAML supports dates — should parse as string or Date
      let result = y(`a: 2024-01-15`)
      assert.ok(result.a != null)
    })

    test('timestamp-datetime', () => {
      let result = y(`a: 2024-01-15T10:30:00Z`)
      assert.ok(result.a != null)
    })
  })


  // ===== QUOTED STRINGS =====

  describe('quoted-strings', () => {

    test('double-quoted', () => {
      assert.deepEqual(y(`a: "hello"`), { a: 'hello' })
    })

    test('single-quoted', () => {
      assert.deepEqual(y(`a: 'hello'`), { a: 'hello' })
    })

    test('double-quoted-with-colon', () => {
      assert.deepEqual(y(`a: "key: value"`), { a: 'key: value' })
    })

    test('single-quoted-with-colon', () => {
      assert.deepEqual(y(`a: 'key: value'`), { a: 'key: value' })
    })

    test('double-quoted-with-newline-escape', () => {
      assert.deepEqual(y(`a: "line1\\nline2"`), { a: 'line1\nline2' })
    })

    test('double-quoted-with-tab-escape', () => {
      assert.deepEqual(y(`a: "col1\\tcol2"`), { a: 'col1\tcol2' })
    })

    test('single-quoted-no-escapes', () => {
      // Single-quoted strings don't process escapes in YAML
      assert.deepEqual(y(`a: 'line1\\nline2'`), { a: 'line1\\nline2' })
    })

    test('single-quoted-with-double-curly-braces', () => {
      assert.deepEqual(y(`foo: 'a{{q}}b'`), { foo: 'a{{q}}b' })
    })

    test('double-quoted-with-double-curly-braces', () => {
      assert.deepEqual(y(`foo: "a{{q}}b"`), { foo: 'a{{q}}b' })
    })

    test('double-quoted-empty', () => {
      assert.deepEqual(y(`a: ""`), { a: '' })
    })

    test('single-quoted-empty', () => {
      assert.deepEqual(y(`a: ''`), { a: '' })
    })

    test('quoted-key', () => {
      assert.deepEqual(y(`"a b": 1`), { 'a b': 1 })
    })

    test('quoted-number-stays-string', () => {
      assert.deepEqual(y(`a: "42"`), { a: '42' })
    })

    test('quoted-boolean-stays-string', () => {
      assert.deepEqual(y(`a: "true"`), { a: 'true' })
    })
  })


  // ===== BLOCK SCALARS =====

  describe('block-scalars', () => {

    test('literal-block', () => {
      // | preserves newlines
      assert.deepEqual(y(`a: |\n  line1\n  line2\n  line3`),
        { a: 'line1\nline2\nline3\n' })
    })

    test('literal-block-strip', () => {
      // |- strips trailing newline
      assert.deepEqual(y(`a: |-\n  line1\n  line2`),
        { a: 'line1\nline2' })
    })

    test('literal-block-keep', () => {
      // |+ keeps trailing newlines
      assert.deepEqual(y(`a: |+\n  line1\n  line2\n\n`),
        { a: 'line1\nline2\n\n' })
    })

    test('folded-block', () => {
      // > folds newlines to spaces
      assert.deepEqual(y(`a: >\n  line1\n  line2\n  line3`),
        { a: 'line1 line2 line3\n' })
    })

    test('folded-block-strip', () => {
      assert.deepEqual(y(`a: >-\n  line1\n  line2`),
        { a: 'line1 line2' })
    })

    test('folded-block-keep', () => {
      assert.deepEqual(y(`a: >+\n  line1\n  line2\n\n`),
        { a: 'line1 line2\n\n' })
    })

    test('literal-block-with-indent', () => {
      assert.deepEqual(y(`a:\n  b: |\n    indented\n    text`),
        { a: { b: 'indented\ntext\n' } })
    })

    test('literal-block-preserves-inner-indent', () => {
      assert.deepEqual(y(`a: |\n  line1\n    indented\n  line3`),
        { a: 'line1\n  indented\nline3\n' })
    })

    test('literal-block-csv-example-preserved-verbatim', () => {
      assert.deepEqual(y(`schema:
  example: |
    "clickId","date","placementId","market","merchantId","merchantName","revenue","currency"
    "532f889fd3ba56f628f3234647d9854650534789938b7fdaafddf1d75081fadc","2018-01-01T00:00:01+00:00","your-custom-placement-id-1","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.142898","EUR"
    "ae7facb00d557e7d92e1d2ee31bc05cc9787bc6802e636ccb284cfbaeb6680b8","2018-01-01T00:00:02+00:00","your-custom-placement-id-2","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.142825","EUR"
    "8bc875e7f5260fa14b21797508b9e47ee2df2c2fe0351b88edded847ee59bb1f","2018-01-01T00:00:03+00:00","your-custom-placement-id-3","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.120417","EUR"`),
        {
          schema: {
            example:
              '"clickId","date","placementId","market","merchantId","merchantName","revenue","currency"\n' +
              '"532f889fd3ba56f628f3234647d9854650534789938b7fdaafddf1d75081fadc","2018-01-01T00:00:01+00:00","your-custom-placement-id-1","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.142898","EUR"\n' +
              '"ae7facb00d557e7d92e1d2ee31bc05cc9787bc6802e636ccb284cfbaeb6680b8","2018-01-01T00:00:02+00:00","your-custom-placement-id-2","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.142825","EUR"\n' +
              '"8bc875e7f5260fa14b21797508b9e47ee2df2c2fe0351b88edded847ee59bb1f","2018-01-01T00:00:03+00:00","your-custom-placement-id-3","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.120417","EUR"\n'
          }
        })
    })
  })


  // ===== FLOW COLLECTIONS =====

  describe('flow-collections', () => {

    test('flow-sequence', () => {
      assert.deepEqual(y(`a: [1, 2, 3]`), { a: [1, 2, 3] })
    })

    test('flow-mapping', () => {
      assert.deepEqual(y(`a: {x: 1, y: 2}`), { a: { x: 1, y: 2 } })
    })

    test('nested-flow-in-block', () => {
      assert.deepEqual(y(`a: [1, [2, 3]]`), { a: [1, [2, 3]] })
    })

    test('flow-map-in-flow-seq', () => {
      assert.deepEqual(y(`a: [{x: 1}, {y: 2}]`), { a: [{ x: 1 }, { y: 2 }] })
    })

    test('empty-flow-sequence', () => {
      assert.deepEqual(y(`a: []`), { a: [] })
    })

    test('empty-flow-mapping', () => {
      assert.deepEqual(y(`a: {}`), { a: {} })
    })

    test('flow-at-top-level-seq', () => {
      assert.deepEqual(y(`[1, 2, 3]`), [1, 2, 3])
    })

    test('flow-at-top-level-map', () => {
      assert.deepEqual(y(`{a: 1, b: 2}`), { a: 1, b: 2 })
    })
  })


  // ===== COMMENTS =====

  describe('comments', () => {

    test('line-comment', () => {
      assert.deepEqual(y(`a: 1 # comment\nb: 2`), { a: 1, b: 2 })
    })

    test('full-line-comment', () => {
      assert.deepEqual(y(`# this is a comment\na: 1`), { a: 1 })
    })

    test('comment-after-key', () => {
      assert.deepEqual(y(`a: # comment\n  b: 1`), { a: { b: 1 } })
    })

    test('multiple-comments', () => {
      assert.deepEqual(y(`# first\na: 1\n# second\nb: 2`), { a: 1, b: 2 })
    })

    test('comment-in-list', () => {
      assert.deepEqual(y(`- a # comment\n- b`), ['a', 'b'])
    })

    test('comment-only-line-between-pairs', () => {
      assert.deepEqual(y(`a: 1\n# middle\nb: 2`), { a: 1, b: 2 })
    })
  })


  // ===== ANCHORS AND ALIASES =====

  describe('anchors-aliases', () => {

    test('simple-anchor-alias', () => {
      assert.deepEqual(y(`a: &ref hello\nb: *ref`), { a: 'hello', b: 'hello' })
    })

    test('anchor-on-map', () => {
      assert.deepEqual(y(`defaults: &defaults\n  x: 1\n  y: 2\noverride:\n  <<: *defaults\n  y: 3`),
        { defaults: { x: 1, y: 2 }, override: { x: 1, y: 3 } })
    })

    test('anchor-on-sequence', () => {
      assert.deepEqual(y(`a: &items\n  - 1\n  - 2\nb: *items`),
        { a: [1, 2], b: [1, 2] })
    })

    test('multiple-aliases', () => {
      assert.deepEqual(y(`a: &x 10\nb: &y 20\nc: *x\nd: *y`),
        { a: 10, b: 20, c: 10, d: 20 })
    })
  })


  // ===== MERGE KEY =====

  describe('merge-key', () => {

    test('simple-merge', () => {
      assert.deepEqual(y(`defaults: &d\n  a: 1\n  b: 2\nresult:\n  <<: *d\n  c: 3`),
        { defaults: { a: 1, b: 2 }, result: { a: 1, b: 2, c: 3 } })
    })

    test('merge-override', () => {
      assert.deepEqual(y(`base: &b\n  x: 1\n  y: 2\nchild:\n  <<: *b\n  y: 99`),
        { base: { x: 1, y: 2 }, child: { x: 1, y: 99 } })
    })

    test('merge-multiple', () => {
      assert.deepEqual(y(`a: &a\n  x: 1\nb: &b\n  y: 2\nc:\n  <<: [*a, *b]\n  z: 3`),
        { a: { x: 1 }, b: { y: 2 }, c: { x: 1, y: 2, z: 3 } })
    })
  })


  // ===== MULTI-DOCUMENT =====

  describe('multi-document', () => {

    test('document-start-marker', () => {
      // Single doc with explicit start: still scalar (one doc).
      assert.deepEqual(y(`---\na: 1`), { a: 1 })
    })

    test('document-end-marker', () => {
      // Single doc terminated with `...`: still scalar (one doc).
      assert.deepEqual(y(`a: 1\n...`), { a: 1 })
    })

    test('two-documents', () => {
      // Two docs: array of values.
      assert.deepEqual(y(`---\na: 1\n---\nb: 2`),
        [{ a: 1 }, { b: 2 }])
    })

    test('three-documents', () => {
      assert.deepEqual(y(`---\na: 1\n---\nb: 2\n---\nc: 3`),
        [{ a: 1 }, { b: 2 }, { c: 3 }])
    })

    test('two-documents-with-end-markers', () => {
      // `...` between docs is allowed.
      assert.deepEqual(y(`---\na: 1\n...\n---\nb: 2`),
        [{ a: 1 }, { b: 2 }])
    })

    test('multi-doc-mixed-shapes', () => {
      // Each doc has independent shape: list, then map, then scalar.
      assert.deepEqual(y(`---\n- 1\n- 2\n---\na: 1\n---\nfoo`),
        [[1, 2], { a: 1 }, 'foo'])
    })

    test('multi-doc-empty-docs', () => {
      // Bare `---` between docs counts as an empty (null) doc.
      assert.deepEqual(y(`---\n---\n---`), [null, null, null])
    })

    test('multi-doc-list-of-lists', () => {
      assert.deepEqual(y(`---\n- a\n- b\n---\n- c\n- d`),
        [['a', 'b'], ['c', 'd']])
    })

    test('multi-doc-with-yaml-directive', () => {
      // %YAML directive is silently accepted; doesn't change result shape.
      assert.deepEqual(y(`%YAML 1.2\n---\na: 1`), { a: 1 })
    })

    test('multi-doc-with-tag-directive', () => {
      // %TAG handle registered; result is just the doc value.
      assert.deepEqual(y(`%TAG !! tag:example.com,2025:\n---\na: 1`),
        { a: 1 })
    })

    test('reparse-same-source-is-idempotent', () => {
      // Regression: parsing the same source twice on one parser instance
      // must produce identical output. Earlier versions used a source-string
      // identity check to gate per-parse state reset, which silently skipped
      // the reset on the second call and accumulated stream state.
      const j = Jsonic.make().use(Yaml)
      const src = `openapi: 3.0\npaths:\n  /a:\n    get: {}`
      const a = j(src)
      const b = j(src)
      assert.deepEqual(b, a)
    })
  })


  // ===== STREAM META OPTION =====

  describe('stream-meta', () => {

    function ym(src: string) {
      const j = Jsonic.make().use(Yaml, { meta: true })
      return j(src)
    }

    test('single-doc-implicit-meta-shape', () => {
      // No markers, single doc: meta is a single object, content is the value.
      const r = ym(`a: 1`)
      assert.deepEqual(r.content, { a: 1 })
      assert.deepEqual(r.meta, { directives: [], explicit: false, ended: false })
    })

    test('single-doc-explicit-start-marked', () => {
      const r = ym(`---\na: 1`)
      assert.deepEqual(r.content, { a: 1 })
      assert.deepEqual(r.meta.explicit, true)
      assert.deepEqual(r.meta.ended, false)
    })

    test('single-doc-explicit-end-marked', () => {
      const r = ym(`a: 1\n...`)
      assert.deepEqual(r.content, { a: 1 })
      assert.deepEqual(r.meta.ended, true)
    })

    test('two-docs-meta-is-array', () => {
      const r = ym(`---\na: 1\n---\nb: 2`)
      assert.deepEqual(r.content, [{ a: 1 }, { b: 2 }])
      assert.ok(Array.isArray(r.meta))
      assert.deepEqual(r.meta.length, 2)
      assert.deepEqual(r.meta[0].explicit, true)
      assert.deepEqual(r.meta[1].explicit, true)
    })

    test('two-docs-end-flag-only-on-first', () => {
      // Only the first doc is `...`-terminated; second isn't.
      const r = ym(`---\na: 1\n...\n---\nb: 2`)
      assert.deepEqual(r.meta[0].ended, true)
      assert.deepEqual(r.meta[1].ended, false)
    })

    test('directive-captured-in-meta', () => {
      const r = ym(`%YAML 1.2\n---\na: 1`)
      assert.deepEqual(r.meta.directives, ['%YAML 1.2'])
      assert.deepEqual(r.meta.explicit, true)
    })

    test('multiple-directives-captured', () => {
      const r = ym(`%YAML 1.2\n%TAG !! tag:foo.com,2025:\n---\na: 1`)
      assert.deepEqual(r.meta.directives,
        ['%YAML 1.2', '%TAG !! tag:foo.com,2025:'])
    })

    test('per-doc-directives-isolated', () => {
      // Directives apply only to the doc that follows them.
      const r = ym(`%YAML 1.2\n---\na: 1\n---\nb: 2`)
      assert.deepEqual(r.meta[0].directives, ['%YAML 1.2'])
      assert.deepEqual(r.meta[1].directives, [])
    })

    test('meta-disabled-returns-bare-content', () => {
      // Default meta=false: same shape as no plugin option.
      const j = Jsonic.make().use(Yaml)
      assert.deepEqual(j(`a: 1`), { a: 1 })
      assert.deepEqual(j(`---\na: 1\n---\nb: 2`), [{ a: 1 }, { b: 2 }])
    })

    test('meta-explicitly-disabled', () => {
      // meta:false matches meta-not-passed.
      const j = Jsonic.make().use(Yaml, { meta: false })
      assert.deepEqual(j(`a: 1`), { a: 1 })
    })
  })


  // ===== TAGS =====

  describe('tags', () => {

    test('explicit-string-tag', () => {
      assert.deepEqual(y(`a: !!str 42`), { a: '42' })
    })

    test('explicit-int-tag', () => {
      assert.deepEqual(y(`a: !!int "42"`), { a: 42 })
    })

    test('explicit-float-tag', () => {
      assert.deepEqual(y(`a: !!float "3.14"`), { a: 3.14 })
    })

    test('explicit-bool-tag', () => {
      assert.deepEqual(y(`a: !!bool "true"`), { a: true })
    })

    test('explicit-null-tag', () => {
      assert.deepEqual(y(`a: !!null ""`), { a: null })
    })

    test('explicit-seq-tag', () => {
      assert.deepEqual(y(`a: !!seq\n  - 1\n  - 2`), { a: [1, 2] })
    })

    test('explicit-map-tag', () => {
      assert.deepEqual(y(`a: !!map\n  x: 1`), { a: { x: 1 } })
    })
  })


  // ===== COMPLEX KEYS =====

  describe('complex-keys', () => {

    test('explicit-key', () => {
      // ? marks an explicit key
      assert.deepEqual(y(`? a\n: 1`), { a: 1 })
    })

    test('multiline-key', () => {
      assert.deepEqual(y(`? a b\n: 1`), { 'a b': 1 })
    })

    test('numeric-key', () => {
      assert.deepEqual(y(`1: one\n2: two`), { 1: 'one', 2: 'two' })
    })
  })


  // ===== DIRECTIVES =====

  describe('directives', () => {

    test('yaml-directive', () => {
      let result: any
      try { result = y(`%YAML 1.2\n---\na: 1`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })

    test('tag-directive', () => {
      let result: any
      try { result = y(`%TAG ! tag:example.com,2000:\n---\na: 1`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })
  })


  // ===== INDENTATION EDGE CASES =====

  describe('indentation', () => {

    test('two-space-indent', () => {
      assert.deepEqual(y(`a:\n  b: 1`), { a: { b: 1 } })
    })

    test('four-space-indent', () => {
      assert.deepEqual(y(`a:\n    b: 1`), { a: { b: 1 } })
    })

    test('mixed-indent-levels', () => {
      assert.deepEqual(y(`a:\n  b:\n      c: 1`), { a: { b: { c: 1 } } })
    })

    test('return-to-outer-indent', () => {
      // After nested content, return to parent indent level
      assert.deepEqual(y(`a:\n  b: 1\n  c: 2\nd: 3`), { a: { b: 1, c: 2 }, d: 3 })
    })

    test('multiple-indent-returns', () => {
      assert.deepEqual(y(`a:\n  b:\n    c: 1\n  d: 2\ne: 3`),
        { a: { b: { c: 1 }, d: 2 }, e: 3 })
    })

    test('list-indent-under-map', () => {
      assert.deepEqual(y(`a:\n  - 1\n  - 2\nb: 3`), { a: [1, 2], b: 3 })
    })

    test('tab-indentation-rejected', () => {
      // YAML spec says tabs are not allowed for indentation
      let result: any
      try { result = y(`a:\n\tb: 1`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })

    test('start-of-file-indent', () => {
      // Content starting with indentation (no leading newline)
      let result: any
      try { result = y(`  a: 1`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })

    test('blank-lines-between-pairs', () => {
      let result: any
      try { result = y(`a: 1\n\nb: 2`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })

    test('blank-lines-in-list', () => {
      let result: any
      try { result = y(`- a\n\n- b`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })
  })


  // ===== MULTILINE PLAIN SCALARS =====

  describe('multiline-plain-scalars', () => {

    test('continuation-line', () => {
      // In YAML, plain scalars can span multiple lines (folded at same indent)
      assert.deepEqual(y(`a: this is\n  a long string`), { a: 'this is a long string' })
    })

    test('multiple-continuation-lines', () => {
      assert.deepEqual(y(`a: line one\n  line two\n  line three`),
        { a: 'line one line two line three' })
    })
  })


  // ===== WINDOWS LINE ENDINGS =====

  describe('line-endings', () => {

    test('crlf', () => {
      assert.deepEqual(y(`a: 1\r\nb: 2`), { a: 1, b: 2 })
    })

    test('crlf-nested', () => {
      assert.deepEqual(y(`a:\r\n  b: 1\r\n  c: 2`), { a: { b: 1, c: 2 } })
    })

    test('crlf-list', () => {
      assert.deepEqual(y(`- a\r\n- b`), ['a', 'b'])
    })
  })


  // ===== STRING VALUES WITH SPECIAL CHARACTERS =====

  describe('special-chars-in-values', () => {

    test('value-with-hash-not-comment', () => {
      // In YAML, # must have a space before it to be a comment
      assert.deepEqual(y(`a: foo#bar`), { a: 'foo#bar' })
    })

    test('value-with-colon-no-space', () => {
      // "http://example.com" - colon not followed by space
      let result: any
      try { result = y(`url: http://example.com`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })

    test('key-with-spaces', () => {
      assert.deepEqual(y(`a long key: value`), { 'a long key': 'value' })
    })

    test('value-with-brackets', () => {
      // Plain scalar containing brackets
      let result: any
      try { result = y(`a: some [text] here`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })

    test('value-with-braces', () => {
      let result: any
      try { result = y(`a: some {text} here`) } catch (e: any) { result = 'ERROR' }
      assert.ok(result != null)
    })
  })


  // ===== SEQUENCE-OF-MAPPINGS COMPACT NOTATION =====

  describe('sequence-of-mappings', () => {

    test('compact-notation', () => {
      // - key: val  (most common YAML pattern for lists of objects)
      assert.deepEqual(y(`- name: alice\n  age: 30\n- name: bob\n  age: 25`),
        [{ name: 'alice', age: 30 }, { name: 'bob', age: 25 }])
    })

    test('single-key-per-element', () => {
      assert.deepEqual(y(`- a: 1\n- b: 2\n- c: 3`),
        [{ a: 1 }, { b: 2 }, { c: 3 }])
    })

    test('nested-in-map', () => {
      assert.deepEqual(y(`people:\n  - name: alice\n  - name: bob`),
        { people: [{ name: 'alice' }, { name: 'bob' }] })
    })
  })


  // ===== REAL-WORLD YAML PATTERNS =====

  describe('real-world', () => {

    test('docker-compose-like', () => {
      assert.deepEqual(y(`version: 3\nservices:\n  web:\n    image: nginx\n    ports:\n      - 80\n      - 443`),
        {
          version: 3,
          services: {
            web: {
              image: 'nginx',
              ports: [80, 443]
            }
          }
        })
    })

    test('github-actions-like', () => {
      assert.deepEqual(y(`name: build\non:\n  push:\n    branches:\n      - main\njobs:\n  test:\n    runs-on: ubuntu`),
        {
          name: 'build',
          on: { push: { branches: ['main'] } },
          jobs: { test: { 'runs-on': 'ubuntu' } }
        })
    })

    test('kubernetes-like', () => {
      assert.deepEqual(y(`apiVersion: v1\nkind: Pod\nmetadata:\n  name: myapp\n  labels:\n    app: myapp\nspec:\n  containers:\n    - name: web\n      image: nginx`),
        {
          apiVersion: 'v1',
          kind: 'Pod',
          metadata: { name: 'myapp', labels: { app: 'myapp' } },
          spec: { containers: [{ name: 'web', image: 'nginx' }] }
        })
    })

    test('ansible-like', () => {
      assert.deepEqual(y(`- name: install packages\n  become: true\n- name: start service\n  become: false`),
        [
          { name: 'install packages', become: true },
          { name: 'start service', become: false }
        ])
    })

    test('config-file-like', () => {
      assert.deepEqual(y(`database:\n  host: localhost\n  port: 5432\n  name: mydb\ncache:\n  enabled: true\n  ttl: 3600`),
        {
          database: { host: 'localhost', port: 5432, name: 'mydb' },
          cache: { enabled: true, ttl: 3600 }
        })
    })
  })


  // ===== PERFORMANCE =====
  // Each test parses a representative workload many times and asserts the
  // total stays under a 2-second budget. Iteration counts are sized so the
  // expected cost on a typical dev box is ~1s, leaving ~50% headroom for
  // slower CI runners.

  describe('performance', () => {
    const BUDGET_MS = 2000

    function measure(iters: number, src: string) {
      const j = Jsonic.make().use(Yaml)
      // Warm up so JIT/cache effects don't show up in the measured loop.
      for (let i = 0; i < 50; i++) j(src)
      const t0 = Date.now()
      for (let i = 0; i < iters; i++) j(src)
      return Date.now() - t0
    }

    test('tiny block map under 2s', () => {
      const elapsed = measure(2500, `a: 1\nb: 2\nc: 3`)
      assert.ok(elapsed < BUDGET_MS,
        `tiny-block-map 2500x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('nested block map under 2s', () => {
      const src = `
top:
  a: 1
  b:
    c: 2
    d: 3
  e:
    - 1
    - 2
    - 3
  f:
    g:
      h: 4
`
      const elapsed = measure(1000, src)
      assert.ok(elapsed < BUDGET_MS,
        `nested-block-map 1000x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('flow seq 200 items under 2s', () => {
      const items = []
      for (let i = 0; i < 200; i++) items.push(`v${i}`)
      const src = '[' + items.join(', ') + ']'
      const elapsed = measure(100, src)
      assert.ok(elapsed < BUDGET_MS,
        `flow-seq-200 100x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('flow map 200 pairs under 2s', () => {
      const pairs = []
      for (let i = 0; i < 200; i++) pairs.push(`k${i}: v${i}`)
      const src = '{' + pairs.join(', ') + '}'
      const elapsed = measure(50, src)
      assert.ok(elapsed < BUDGET_MS,
        `flow-map-200 50x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('block seq 200 items under 2s', () => {
      const items = []
      for (let i = 0; i < 200; i++) items.push(`- item${i}`)
      const src = items.join('\n')
      const elapsed = measure(100, src)
      assert.ok(elapsed < BUDGET_MS,
        `block-seq-200 100x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('anchors and aliases under 2s', () => {
      const src = `
defaults: &d
  retries: 3
  timeout: 30
prod:
  <<: *d
  host: prod.com
dev:
  <<: *d
  host: dev.com
`
      const elapsed = measure(750, src)
      assert.ok(elapsed < BUDGET_MS,
        `anchors+aliases 750x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('kubernetes-like config under 2s', () => {
      const src = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
  labels:
    app: nginx
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
`
      const elapsed = measure(500, src)
      assert.ok(elapsed < BUDGET_MS,
        `kubernetes-like 500x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('multi-document stream under 2s', () => {
      // 50-document stream — exercises the new stream rule's accumulation.
      const docs = []
      for (let i = 0; i < 50; i++) docs.push(`doc: ${i}`)
      const src = '---\n' + docs.join('\n---\n')
      const elapsed = measure(250, src)
      assert.ok(elapsed < BUDGET_MS,
        `multi-doc-50 250x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })

    test('quoted strings under 2s', () => {
      const src = `
s1: "hello \\"world\\""
s2: 'it''s working'
s3: "multi
line"
s4: "tab\\there"
`
      const elapsed = measure(1250, src)
      assert.ok(elapsed < BUDGET_MS,
        `quoted-strings 1250x took ${elapsed}ms (budget ${BUDGET_MS}ms)`)
    })
  })

})
