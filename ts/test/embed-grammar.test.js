// The grammar embedder is ALL OR NOTHING.
//
// `embed-grammar.js` copies yaml-grammar.jsonic into three runtimes.
// Each target constrains the grammar text: Go cannot hold a backtick in
// a raw string, Rust cannot hold a `"##` run in an `r##` one. A run that
// checked one target after rewriting another would leave the tree half
// embedded: two runtimes carrying a grammar the third does not have,
// with nothing red until someone builds the third.
//
// This runs the real script against a throwaway copy of the repository
// layout, so it exercises the shipped file and touches nothing.

const test = require('node:test')
const assert = require('node:assert')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const { execFileSync } = require('node:child_process')

const BEGIN = '// --- BEGIN EMBEDDED yaml-grammar.jsonic ---'
const END = '// --- END EMBEDDED yaml-grammar.jsonic ---'

const TARGETS = {
  'ts/src/yaml.ts': 'ts',
  'go/yaml.go': 'go',
  'rs/src/lib.rs': 'rs',
}

// A throwaway tree with the script and one stub per target.
function scaffold(grammar) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'embed-grammar-'))
  for (const relative of Object.keys(TARGETS)) {
    const file = path.join(root, relative)
    fs.mkdirSync(path.dirname(file), { recursive: true })
    fs.writeFileSync(file, 'before\n' + BEGIN + '\nSTALE\n' + END + '\nafter\n')
  }
  fs.writeFileSync(path.join(root, 'yaml-grammar.jsonic'), grammar)
  fs.copyFileSync(
    path.join(__dirname, '..', 'embed-grammar.js'),
    path.join(root, 'ts', 'embed-grammar.js')
  )
  return root
}

function run(root) {
  try {
    const out = execFileSync(process.execPath, ['embed-grammar.js'], {
      cwd: path.join(root, 'ts'),
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    return { code: 0, out }
  } catch (error) {
    return { code: error.status, out: String(error.stderr || '') }
  }
}

const read = (root, relative) => fs.readFileSync(path.join(root, relative), 'utf8')
const stale = (text) => text.includes('\n' + 'STALE' + '\n')

test('a grammar every target accepts is embedded in all three', () => {
  const root = scaffold("rule: 'x'\n")
  const result = run(root)
  assert.strictEqual(result.code, 0, result.out)
  for (const relative of Object.keys(TARGETS)) {
    assert.ok(!stale(read(root, relative)), relative + ' was not rewritten')
    assert.ok(read(root, relative).includes("rule: 'x'"), relative + ' lacks the grammar')
  }
  fs.rmSync(root, { recursive: true, force: true })
})

// The two content checks, each of which belongs to a target that is NOT
// the first one written. Before the ordering was fixed, tripping either
// left every earlier target rewritten.
for (const [label, grammar] of [
  ['a backtick, which Go cannot hold', "rule: 'x'\n# a ` backtick\n"],
  ['a `"##` run, which Rust cannot hold', 'rule: \'x\'\n# a "## run\n'],
]) {
  test('a grammar carrying ' + label + ' leaves every target untouched', () => {
    const root = scaffold(grammar)
    const before = Object.fromEntries(
      Object.keys(TARGETS).map((relative) => [relative, read(root, relative)])
    )
    const result = run(root)
    assert.strictEqual(result.code, 1, 'the embedder must refuse this grammar')
    assert.match(result.out, /^Error: /m)
    for (const relative of Object.keys(TARGETS)) {
      assert.strictEqual(
        read(root, relative),
        before[relative],
        relative + ' was rewritten by a run that refused the grammar'
      )
      assert.ok(stale(read(root, relative)), relative + ' lost its marker block')
    }
    fs.rmSync(root, { recursive: true, force: true })
  })
}

test('a target with no markers stops the run before any target is written', () => {
  const root = scaffold("rule: 'x'\n")
  // The LAST target loses its markers, so a run that wrote as it went
  // would already have rewritten the other two before noticing.
  fs.writeFileSync(path.join(root, 'rs', 'src', 'lib.rs'), 'no markers here\n')
  const before = Object.fromEntries(
    ['ts/src/yaml.ts', 'go/yaml.go'].map((relative) => [relative, read(root, relative)])
  )
  const result = run(root)
  assert.strictEqual(result.code, 1, 'a missing marker must stop the run')
  assert.match(result.out, /embedding markers not found/)
  for (const relative of Object.keys(before)) {
    assert.strictEqual(read(root, relative), before[relative], relative + ' was rewritten')
  }
  fs.rmSync(root, { recursive: true, force: true })
})

test('the Rust target is skipped, not required, when rs/ is absent', () => {
  const root = scaffold("rule: 'x'\n")
  fs.rmSync(path.join(root, 'rs'), { recursive: true, force: true })
  const result = run(root)
  assert.strictEqual(result.code, 0, result.out)
  assert.match(result.out, /skipping/)
  for (const relative of ['ts/src/yaml.ts', 'go/yaml.go']) {
    assert.ok(!stale(read(root, relative)), relative + ' was not rewritten')
  }
  fs.rmSync(root, { recursive: true, force: true })
})
