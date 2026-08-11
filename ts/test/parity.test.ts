/* Copyright (c) 2025 Richard Rodger and other contributors, MIT License */

// Cross-runtime conformance, driven by the shared `test/spec/*.tsv` fixtures
// at the repo root (see ../../test/AGENTS.md).
//
// The fixture loader, the escape codec, the `ERROR:<code>` contract and the
// row loop all come from @tabnas/support, whose Go half `go/parity_test.go`
// uses to run the SAME files — so the two implementations cannot drift
// without one of them going red, and neither can the two loaders.
//
// What is left here is only what is specific to yaml: how to build the
// parser for a row's options, and the non-finite-number encoding.

import { Tabnas } from '@tabnas/parser'
import { jsonic } from '@tabnas/jsonic'
import { findSpecDir, makeRunner, parseExpect } from '@tabnas/support'

import { Yaml } from '../dist/yaml'

// YAML has non-finite numbers (.inf / .nan) that JSON cannot spell, and they
// can appear at any depth. Fixtures encode them as the marker strings
// "@@Infinity", "@@-Infinity" and "@@NaN"; this maps a parse result into the
// same encoding so the two sides compare structurally. See
// ../../test/AGENTS.md.
//
// As the runner's `normalize` hook it is applied to every node on BOTH
// sides, outermost first — so it must leave an already-encoded marker
// alone, which it does: a string is returned unchanged.
function canon(v: any): any {
  if ('number' === typeof v && !Number.isFinite(v)) {
    return Number.isNaN(v) ? '@@NaN' : 0 < v ? '@@Infinity' : '@@-Infinity'
  }
  return v
}

makeRunner({
  // A fresh Tabnas per row: the `opts` column is per-case, and plugin
  // options must not leak from one row into the next.
  parse: (input, row) => {
    const opts = row.named('opts')
    return new Tabnas()
      .use(jsonic)
      .use(Yaml, '' === opts.trim() ? {} : JSON.parse(opts))
      .parse(input)
  },

  // Input that yields no value at all cannot be spelled in JSON, and it is
  // a different result from a document whose value is null — so the
  // fixtures write the bare token UNDEFINED, and a row that says `null`
  // still must not be satisfied by a parse that produced nothing.
  parseExpected: (expected) =>
    'UNDEFINED' === expected ? undefined : parseExpect(expected),

  normalize: canon,
})
  // `findSpecDir` walks up from this file — `dist-test/` at runtime — to the
  // repo root's `test/spec`, so moving the suite does not mean recounting
  // `..` hops. `dir` then auto-discovers every fixture in it, so adding a
  // .tsv runs it in both runtimes without touching either runner.
  .dir(findSpecDir(__dirname))
