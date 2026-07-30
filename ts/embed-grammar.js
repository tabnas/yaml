#!/usr/bin/env node

// Embeds yaml-grammar.jsonic into src/yaml.ts and go/yaml.go.
// Run via: node embed-grammar.js

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

console.log('Embedded yaml-grammar.jsonic into src/yaml.ts and go/yaml.go')
