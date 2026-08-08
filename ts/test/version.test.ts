/* Copyright (c) 2026 Richard Rodger, MIT License */

// The exported VERSION must equal package.json "version".
//
// This is the CI check for version drift. It exists because the constant HAS
// drifted: @tabnas/json exported Version = '1.0.0' for several releases while
// the package shipped 0.4.x, because nothing rewrote it and AGENTS.md wrongly
// claimed `make publish-go` kept it in sync. A release that bumps
// package.json and forgets the constant now fails here.

import { describe, test } from 'node:test'
import assert from 'node:assert'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'


// Deliberately throws (a hard test failure), never skips: a version check
// that silently does not run is the failure mode this test exists to prevent.
function readPackageJson(): { name: string; version: string } {
  const file = join(__dirname, '..', 'package.json')
  let raw: string
  try {
    raw = readFileSync(file, 'utf8')
  } catch (e: any) {
    throw new Error(
      `cannot read ${file}, so VERSION cannot be checked: ${e.message}`)
  }
  return JSON.parse(raw)
}


// The package root, resolved exactly as a consumer would resolve it, so this
// also proves VERSION is reachable from the published entry point.
const api = require('..')


describe('version', () => {

  test('VERSION matches package.json', () => {
    const pkg = readPackageJson()
    assert.notEqual(pkg.version, '', 'package.json has no version field')
    assert.equal(
      api.VERSION,
      pkg.version,
      `VERSION drift: ${pkg.name} exports ${api.VERSION} but package.json is ` +
      `${pkg.version}. Both are rewritten by admin/publish.sh at release; ` +
      `if you bumped one by hand, bump the other.`,
    )
  })

  test('VERSION is exported and looks like a semver', () => {
    assert.equal(
      typeof api.VERSION, 'string', 'VERSION must be exported as a string')
    assert.match(api.VERSION, /^\d+\.\d+\.\d+/, 'VERSION must be a semver')
  })

})
