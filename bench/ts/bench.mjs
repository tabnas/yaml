// Benchmark the TypeScript YAML parser against synthetic fixtures.
//
// Runs each fixture through a timed harness and reports:
//   - bytes parsed
//   - wall-time per iteration (median, best)
//   - parsed-bytes/s throughput
//   - heap delta (Node's process.memoryUsage().heapUsed) for a single parse
//
// Also calls --prof-process-friendly runs if --profile is passed, producing
// V8 CPU profile data (.cpuprofile) that can be loaded into chrome://inspect.
//
// Usage:
//   npm run build
//   node bench/ts/bench.mjs
//   node bench/ts/bench.mjs --only=block_map_wide,flow_map_large
//   node bench/ts/bench.mjs --profile=block_map_wide

import { readFileSync, writeFileSync, readdirSync } from 'node:fs'
import { performance } from 'node:perf_hooks'
import { Session } from 'node:inspector/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const root      = join(__dirname, '..', '..')
const fixtures  = join(root, 'bench', 'fixtures')

const { Jsonic } = await import(join(root, 'node_modules', '@tabnas', 'jsonic', 'dist', 'jsonic.js'))
const { Yaml }   = await import(join(root, 'dist', 'yaml.js'))

function makeParser() {
  return Jsonic.make().use(Yaml)
}

function parseOrigin(parser, src, label) {
  try { return parser(src) }
  catch (e) {
    console.warn(`[warn] ${label} parse failed: ${e.message?.split('\n')[0]}`)
    return null
  }
}

function median(xs) {
  const s = xs.slice().sort((a, b) => a - b)
  return s[Math.floor(s.length / 2)]
}

function mean(xs) { return xs.reduce((a, b) => a + b, 0) / xs.length }
function stddev(xs) {
  const m = mean(xs)
  return Math.sqrt(xs.reduce((a, b) => a + (b - m) ** 2, 0) / xs.length)
}

function fmtBytes(n) {
  if (n < 1024) return n + ' B'
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
  return (n / 1024 / 1024).toFixed(2) + ' MB'
}

function fmtMs(ms) { return ms.toFixed(2) + ' ms' }

// Run one fixture many times, returning timing statistics.
// We make a fresh Jsonic instance each iteration, matching the
// real entry point `Yaml.Parse` which re-creates plugin state.
function measure(src, { iters, warmup, freshParserPerIter }) {
  // Warm-up: lets V8 JIT compile the hot paths.
  const warmParser = makeParser()
  for (let i = 0; i < warmup; i++) warmParser(src)

  const samples = []
  for (let i = 0; i < iters; i++) {
    const parser = freshParserPerIter ? makeParser() : warmParser
    const t0 = performance.now()
    parser(src)
    const t1 = performance.now()
    samples.push(t1 - t0)
  }
  return samples
}

async function profile(src, outPath) {
  const session = new Session()
  session.connect()
  await session.post('Profiler.enable')
  await session.post('Profiler.start')
  const parser = makeParser()
  // Run several iterations so the profile has enough samples.
  for (let i = 0; i < 50; i++) parser(src)
  const { profile } = await session.post('Profiler.stop')
  writeFileSync(outPath, JSON.stringify(profile))
  session.disconnect()
}

const args    = Object.fromEntries(
  process.argv.slice(2).map(a => {
    const [k, v] = a.replace(/^--/, '').split('=')
    return [k, v ?? true]
  }))
const onlySet = args.only ? new Set(args.only.split(',')) : null
const iters   = Number(args.iters  ?? 15)
const warmup  = Number(args.warmup ??  3)
const freshParserPerIter = args.reuseParser ? false : true

const fixtureFiles = readdirSync(fixtures)
  .filter(f => f.endsWith('.yaml'))
  .filter(f => !onlySet || onlySet.has(f.replace(/\.yaml$/, '')))
  .sort()

console.log('TypeScript YAML parser benchmark')
console.log(`node ${process.version} | iters=${iters} warmup=${warmup}`)
console.log(`freshParserPerIter=${freshParserPerIter}`)
console.log('='.repeat(78))

const rows = []
for (const file of fixtureFiles) {
  const name = file.replace(/\.yaml$/, '')
  const src  = readFileSync(join(fixtures, file), 'utf8')
  const bytes = Buffer.byteLength(src)

  if (args.profile === name || args.profile === true) {
    const cpuPath = join(__dirname, `${name}.cpuprofile`)
    await profile(src, cpuPath)
    console.log(`profile saved: ${cpuPath}`)
  }

  if (global.gc) global.gc()
  const heapBefore = process.memoryUsage().heapUsed
  const parser     = makeParser()
  const t0         = performance.now()
  const result     = parseOrigin(parser, src, name)
  const t1         = performance.now()
  if (global.gc) global.gc()
  const heapAfter  = process.memoryUsage().heapUsed

  if (result === null) {
    rows.push({ name, bytes, ok: false, ms: NaN, throughput: 0, heapDelta: 0 })
    console.log(`${name.padEnd(24)} FAILED TO PARSE`)
    continue
  }

  const samples = measure(src, { iters, warmup, freshParserPerIter })
  const med     = median(samples)
  const best    = Math.min(...samples)
  const sd      = stddev(samples)
  const through = bytes / (med / 1000) // bytes per second

  rows.push({
    name, bytes,
    ok: true,
    ms: med, best, sd,
    throughput: through,
    heapDelta: heapAfter - heapBefore,
    firstParseMs: t1 - t0,
  })

  console.log(
    `${name.padEnd(24)} ` +
    `${fmtBytes(bytes).padStart(10)} ` +
    `median ${fmtMs(med).padStart(10)} ` +
    `best ${fmtMs(best).padStart(10)} ` +
    `±${fmtMs(sd).padStart(8)} ` +
    `${(through / 1024 / 1024).toFixed(2).padStart(6)} MB/s ` +
    `heapΔ ${fmtBytes(heapAfter - heapBefore).padStart(10)}`
  )
}

console.log('='.repeat(78))

if (args.json) {
  writeFileSync(args.json, JSON.stringify({
    node: process.version,
    iters, warmup, freshParserPerIter,
    rows,
  }, null, 2))
  console.log('wrote', args.json)
}
