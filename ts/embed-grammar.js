#!/usr/bin/env node

// Embeds yaml-grammar.jsonic into src/yaml.ts, go/yaml.go and
// rs/src/lib.rs.
// Run via: node embed-grammar.js
//
// Each runtime parses the embedded text with its own jsonic at load
// time, so the three grammars are not three grammars: they are THE
// grammar, in one file, copied verbatim.
//
// EVERY check runs before the FIRST write. A rejected grammar has to
// leave all three files as it found them: a run that validated one
// target after rewriting another would leave the tree half embedded,
// with two runtimes carrying a grammar the third does not have.

const fs = require('fs')
const path = require('path')

const grammar = fs.readFileSync(path.join(__dirname, '..', 'yaml-grammar.jsonic'), 'utf8')

const BEGIN = '// --- BEGIN EMBEDDED yaml-grammar.jsonic ---'
const END = '// --- END EMBEDDED yaml-grammar.jsonic ---'

function fail(message) {
  console.error('Error: ' + message)
  process.exit(1)
}

// --- The targets, and what each one needs of the grammar text. --------

// TypeScript: template literal (escape backslashes, backticks, ${).
const tsContent = grammar
  .replace(/\\/g, '\\\\')
  .replace(/`/g, '\\`')
  .replace(/\$\{/g, '\\${')

const RS_FILE = path.join(__dirname, '..', 'rs', 'src', 'lib.rs')
const hasRust = fs.existsSync(RS_FILE)

const targets = [
  {
    file: path.join(__dirname, 'src', 'yaml.ts'),
    content: 'const grammarText = `\n' + tsContent + '`',
    check: null,
  },
  {
    file: path.join(__dirname, '..', 'go', 'yaml.go'),
    // Go: raw string (backticks cannot appear in content).
    // The trailing newline leaves a blank line before END, which keeps
    // the result gofmt-clean (gofmt wants a blank line between the const
    // declaration and the trailing comment).
    content: 'const grammarText = `\n' + grammar + '`\n',
    check: () =>
      grammar.includes('`') &&
      'grammar file contains backticks, cannot embed in Go raw string',
  },
]

// Rust: a raw string literal, which has no escapes at all. The hash
// count has to clear the longest `"#...` run the content holds; the
// grammar quotes its token names with single quotes, so `"#` never
// appears, and two hashes leave room.
if (hasRust) {
  targets.push({
    file: RS_FILE,
    content: 'const GRAMMAR_TEXT: &str = r##"\n' + grammar + '"##;',
    check: () =>
      grammar.includes('"##') &&
      'grammar file contains `"##`, cannot embed in an r## raw string',
  })
}

// --- Validate every target, then write every target. ------------------

const prepared = []
for (const target of targets) {
  const problem = target.check && target.check()
  if (problem) fail(problem)
  const src = fs.readFileSync(target.file, 'utf8')
  const beginIdx = src.indexOf(BEGIN)
  const endIdx = src.indexOf(END)
  if (beginIdx === -1 || endIdx === -1) {
    fail('embedding markers not found in ' + target.file)
  }
  const replacement = BEGIN + '\n' + target.content + '\n' + END
  prepared.push({
    file: target.file,
    src: src.substring(0, beginIdx) + replacement + src.substring(endIdx + END.length),
  })
}

for (const { file, src } of prepared) {
  fs.writeFileSync(file, src)
}

if (hasRust) {
  console.log('Embedded yaml-grammar.jsonic into src/yaml.ts, go/yaml.go and rs/src/lib.rs')
} else {
  console.log('Embedded yaml-grammar.jsonic into src/yaml.ts and go/yaml.go')
  console.log('No Rust source at ' + RS_FILE + ' - skipping')
}
