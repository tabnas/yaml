/* Copyright (c) 2021-2026 Richard Rodger and other contributors, MIT License */

/*  debug-model.test.ts
 *  Composition test: the YAML grammar plugin layered with the official
 *  @tabnas/debug plugin. @tabnas/debug is a devDependency, but this still
 *  resolves it dynamically and SKIPS when it is absent so the suite stays
 *  runnable outside the package; set TABNAS_DEBUG_PATH to a sibling
 *  checkout's built plugin to force a specific build.
 */

import { test, describe } from 'node:test'
import assert from 'node:assert'

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { Yaml } from '../dist/yaml'


// Dynamically resolve @tabnas/debug, skipping cleanly when it is absent.
function loadDebug(): any {
  const candidates = [process.env.TABNAS_DEBUG_PATH, '@tabnas/debug'].filter(
    Boolean,
  ) as string[]
  for (const c of candidates) {
    try {
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      return require(c).Debug
    } catch {
      /* try next */
    }
  }
  return null
}

const Debug = loadDebug()
const skip = Debug ? false : '@tabnas/debug not available (set TABNAS_DEBUG_PATH)'


// Helper: create a fresh Yaml-enabled Tabnas instance, matching the
// repo's other tests, with the debug plugin installed.
function makeInstance(): any {
  const tn: any = new Tabnas().use(jsonic).use(Yaml)
  tn.use(Debug, { print: false, trace: false })
  return tn
}


describe('debug-model', () => {

  test('parses normally with the debug plugin installed', { skip }, () => {
    const tn = makeInstance()
    assert.deepEqual(tn.parse(`a: 1\nb:\n- x\n- y`), { a: 1, b: ['x', 'y'] })
  })


  test('debug.model() returns the structured YAML grammar', { skip }, () => {
    const tn = makeInstance()
    const m = tn.debug.model()

    // The structured rule set: jsonic's shared val/map/list/pair/elem/indent
    // rules plus the YAML-specific block rules contributed by this plugin.
    assert.deepStrictEqual(
      m.rules.map((r: any) => r.name).sort(),
      [
        'elem',
        'indent',
        'list',
        'map',
        'pair',
        'stream',
        'val',
        'yamlBlockElem',
        'yamlBlockList',
        'yamlElemMap',
        'yamlElemPair',
      ],
    )

    // The YAML grammar enters at its own `stream` start rule (not jsonic's
    // default `val`), and the debug plugin lists this plugin.
    assert.equal(m.config.start, 'stream')
    assert.ok(
      m.plugins.some((p: any) => p.name === 'Yaml'),
      'plugins should list Yaml',
    )

    // Structural fact 1: the `stream` start rule opens into the shared `val`
    // rule (a YAML document is a value), per the rule graph.
    const streamEdges = m.graph.find((e: any) => e.name === 'stream')
    assert.ok(streamEdges, 'stream rule should be present in graph')
    assert.ok(
      streamEdges.openPush.includes('val'),
      'stream should push val on open',
    )

    // Structural fact 2: the YAML block-list rule pushes into the
    // YAML-specific yamlElemMap rule (block sequence items may be maps).
    const blockListEdges = m.graph.find((e: any) => e.name === 'yamlBlockList')
    assert.ok(blockListEdges, 'yamlBlockList rule should be present in graph')
    assert.ok(
      blockListEdges.openPush.includes('yamlElemMap'),
      'yamlBlockList should push yamlElemMap on open',
    )

    // The grammar portion is JSON-serialisable and round-trips.
    const grammar = {
      tokens: m.tokens,
      rules: m.rules,
      graph: m.graph,
      config: m.config,
      abnf: m.abnf,
    }
    assert.deepStrictEqual(JSON.parse(JSON.stringify(grammar)).rules, m.rules)
  })

})
