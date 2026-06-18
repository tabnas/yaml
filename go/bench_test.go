// Benchmark harness for the YAML parser.
//
// Run all benchmarks:
//   (cd go && go test -bench=. -benchmem -benchtime=3s -run=^$ .)
//
// Focus on one fixture:
//   (cd go && go test -bench=BlockMapWide -benchmem -run=^$ .)
//
// CPU profile a specific fixture:
//   (cd go && go test -bench=BlockMapWide -run=^$ -cpuprofile=cpu.out .)
//   go tool pprof -http=: go/cpu.out
//
// Memory profile:
//   (cd go && go test -bench=BlockMapWide -run=^$ -memprofile=mem.out .)
//   go tool pprof -http=: go/mem.out
//
// Fixtures are produced by bench/fixtures/generate.mjs. Each benchmark loads
// its fixture once (outside the timer) and times only Parse(src).

package tabnasyaml

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fixtureDir(tb testing.TB) string {
	tb.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "bench", "fixtures")
}

func loadFixture(tb testing.TB, name string) string {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir(tb), name+".yaml"))
	if err != nil {
		tb.Fatalf("fixture %s: %v", name, err)
	}
	return string(data)
}

func runParseBench(b *testing.B, fixture string) {
	src := loadFixture(b, fixture)
	// Warm-up parse outside the timer; also a smoke test.
	if _, err := Parse(src); err != nil {
		b.Fatalf("warm-up parse failed: %v", err)
	}
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(src); err != nil {
			b.Fatalf("parse failed: %v", err)
		}
	}
}

func BenchmarkBlockMapWide(b *testing.B)       { runParseBench(b, "block_map_wide") }
func BenchmarkBlockSeqLong(b *testing.B)       { runParseBench(b, "block_seq_long") }
func BenchmarkNestedMapDeep(b *testing.B)      { runParseBench(b, "nested_map_deep") }
func BenchmarkLiteralBlockLong(b *testing.B)   { runParseBench(b, "literal_block_long") }
func BenchmarkFoldedBlockLong(b *testing.B)    { runParseBench(b, "folded_block_long") }
func BenchmarkPlainMultilineLong(b *testing.B) { runParseBench(b, "plain_multiline_long") }
func BenchmarkFlowMapLarge(b *testing.B)       { runParseBench(b, "flow_map_large") }
func BenchmarkFlowSeqLarge(b *testing.B)       { runParseBench(b, "flow_seq_large") }
func BenchmarkAnchorAliasHeavy(b *testing.B)   { runParseBench(b, "anchor_alias_heavy") }
func BenchmarkDqStringsEscaped(b *testing.B)   { runParseBench(b, "dq_strings_escaped") }
func BenchmarkMixedRealistic(b *testing.B)     { runParseBench(b, "mixed_realistic") }
