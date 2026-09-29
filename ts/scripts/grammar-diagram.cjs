// Regenerate the grammar's railroad diagram, doc/grammar.{txt,svg}, from
// the live grammar: this plugin installed on jsonic, read back by
// @tabnas/railroad. `npm run diagram` writes both files; with --check it
// writes nothing and exits 1 when either is stale, which is what
// test/grammar-diagram.test.js runs.

const Fs = require('node:fs')
const Path = require('node:path')

const { Tabnas } = require('@tabnas/parser')
const { jsonic } = require('@tabnas/jsonic')
const { railroad } = require('@tabnas/railroad')
const { Yaml } = require('../dist/yaml.js')

const DOC = Path.join(__dirname, '..', 'doc')

function render() {
  const tn = new Tabnas().use(jsonic).use(Yaml).use(railroad)
  return {
    'grammar.txt': tn.railroad.toAscii(),
    'grammar.svg': tn.railroad.toSvg(),
  }
}

function stale() {
  const out = []
  for (const [name, text] of Object.entries(render())) {
    const file = Path.join(DOC, name)
    const have = Fs.existsSync(file) ? Fs.readFileSync(file, 'utf8') : null
    if (have !== text) out.push(name)
  }
  return out
}

module.exports = { render, stale }

if (require.main === module) {
  if (process.argv.includes('--check')) {
    const names = stale()
    if (names.length > 0) {
      console.error('stale: ' + names.map((n) => 'doc/' + n).join(', ') +
        ' (regenerate with: npm run diagram)')
      process.exit(1)
    }
    console.log('doc/grammar.{txt,svg} match the grammar')
  } else {
    for (const [name, text] of Object.entries(render())) {
      Fs.writeFileSync(Path.join(DOC, name), text)
      console.log('wrote doc/' + name)
    }
  }
}
