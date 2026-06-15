// Scaling benchmark: measure parse time at multiple input sizes and
// report the per-byte cost. If per-byte time grows with N, the parser
// is super-linear (likely O(N^2)) on that shape of input.
//
// Usage:
//   node bench/ts/scaling.mjs

import { performance } from 'node:perf_hooks'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const root      = join(__dirname, '..', '..')

const { Jsonic } = await import(join(root, 'node_modules', '@tabnas', 'jsonic', 'dist', 'jsonic.js'))
const { Yaml }   = await import(join(root, 'dist', 'yaml.js'))

function makeParser() { return Jsonic.make().use(Yaml) }

// Generators for each shape we want to profile.
// Each returns a source string for a given N.
const shapes = {
  // Block map: triggers flow-depth rescan per plain scalar.
  blockMap(n) {
    const lines = []
    for (let i = 0; i < n; i++) lines.push(`key_${i}: value ${i}`)
    return lines.join('\n') + '\n'
  },

  // Flow map: triggers preprocessFlowCollections.
  flowMap(n) {
    const entries = []
    for (let i = 0; i < n; i++) entries.push(`k${i}: v${i}`)
    return 'doc: {' + entries.join(', ') + '}\n'
  },

  // Block scalar: pure line collection & optional folding (TS folding concats strings).
  literalBlock(n) {
    return 'content: |\n' +
      Array.from({ length: n }, (_, i) => `  line ${i}`).join('\n') + '\n'
  },

  foldedBlock(n) {
    const parts = []
    for (let i = 0; i < n; i++) {
      if (i % 17 === 0) parts.push('')
      else if (i % 23 === 0) parts.push('    more indented ' + i)
      else parts.push(`word${i}`)
    }
    return 'content: >\n' + parts.map(l => l ? '  ' + l : '').join('\n') + '\n'
  },

  // Aliases to a shared anchor — amplifies deep-copy cost.
  anchorAlias(n) {
    const lines = ['base: &base', ...Array.from({ length: 20 }, (_, i) => `  k${i}: v${i}`)]
    lines.push('refs:')
    for (let i = 0; i < n; i++) lines.push('  - *base')
    return lines.join('\n') + '\n'
  },
}

const sizes = [100, 250, 500, 1000, 2000, 4000]

function timed(src, iters = 3) {
  const times = []
  for (let i = 0; i < iters; i++) {
    const parser = makeParser()
    const t0 = performance.now()
    try { parser(src) } catch { /* fall through; still report wall time */ }
    times.push(performance.now() - t0)
  }
  return Math.min(...times)
}

console.log('Shape           N      bytes      time      µs/byte     slope (vs N/2)')
console.log('-'.repeat(78))
for (const [name, gen] of Object.entries(shapes)) {
  let prevPerByte = null
  for (const n of sizes) {
    const src   = gen(n)
    const bytes = Buffer.byteLength(src)
    const ms    = timed(src)
    const perByte = (ms * 1000) / bytes
    const slope   = prevPerByte == null ? '-' : (perByte / prevPerByte).toFixed(2) + 'x'
    console.log(
      name.padEnd(14) +
      ' ' + String(n).padStart(5) +
      ' ' + String(bytes).padStart(10) +
      ' ' + ms.toFixed(2).padStart(8) + ' ms' +
      ' ' + perByte.toFixed(3).padStart(8) + ' µs' +
      ' ' + slope.padStart(8)
    )
    prevPerByte = perByte
  }
  console.log()
}
