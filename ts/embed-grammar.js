#!/usr/bin/env node

// Embeds yaml-grammar.jsonic into src/yaml.ts, go/yaml.go and
// rs/src/lib.rs.
// Run via: node embed-grammar.js
//
// Each runtime parses the embedded text with its own jsonic at load
// time, so the three grammars are not three grammars: they are THE
// grammar, in one file, copied verbatim.

const fs = require('fs')
const path = require('path')

const grammar = fs.readFileSync(path.join(__dirname, '..', 'yaml-grammar.jsonic'), 'utf8')

const BEGIN = '// --- BEGIN EMBEDDED yaml-grammar.jsonic ---'
const END = '// --- END EMBEDDED yaml-grammar.jsonic ---'

function embed(file, wrapContent) {
  let src = fs.readFileSync(file, 'utf8')
  const beginIdx = src.indexOf(BEGIN)
  const endIdx = src.indexOf(END)
  if (beginIdx === -1 || endIdx === -1) {
    console.error('Error: embedding markers not found in ' + file)
    process.exit(1)
  }
  const replacement = BEGIN + '\n' + wrapContent + '\n' + END
  src = src.substring(0, beginIdx) + replacement + src.substring(endIdx + END.length)
  fs.writeFileSync(file, src)
}

// TypeScript: template literal (escape backslashes, backticks, ${).
const tsContent = grammar
  .replace(/\\/g, '\\\\')
  .replace(/`/g, '\\`')
  .replace(/\$\{/g, '\\${')
embed(
  path.join(__dirname, 'src', 'yaml.ts'),
  'const grammarText = `\n' + tsContent + '`'
)

// Go: raw string (backticks cannot appear in content).
if (grammar.includes('`')) {
  console.error('Error: grammar file contains backticks, cannot embed in Go raw string')
  process.exit(1)
}
// The trailing newline leaves a blank line before END, which keeps the
// result gofmt-clean (gofmt wants a blank line between the const
// declaration and the trailing comment).
embed(
  path.join(__dirname, '..', 'go', 'yaml.go'),
  'const grammarText = `\n' + grammar + '`\n'
)

// Rust: a raw string literal, which has no escapes at all. The hash
// count has to clear the longest `"#...` run the content holds; the
// grammar quotes its token names with single quotes, so `"#` never
// appears, and two hashes leave room.
const RS_FILE = path.join(__dirname, '..', 'rs', 'src', 'lib.rs')
if (fs.existsSync(RS_FILE)) {
  if (grammar.includes('"##')) {
    console.error('Error: grammar file contains `"##`, cannot embed in an r## raw string')
    process.exit(1)
  }
  embed(RS_FILE, 'const GRAMMAR_TEXT: &str = r##"\n' + grammar + '"##;')
  console.log('Embedded yaml-grammar.jsonic into src/yaml.ts, go/yaml.go and rs/src/lib.rs')
} else {
  console.log('Embedded yaml-grammar.jsonic into src/yaml.ts and go/yaml.go')
  console.log('No Rust source at ' + RS_FILE + ' - skipping')
}
