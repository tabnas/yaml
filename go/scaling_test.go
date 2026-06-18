// Scaling tests: measure Parse() time at multiple input sizes and
// report per-byte cost so we can tell linear from super-linear shapes.
//
// Usage:
//   (cd go && go test -run TestScaling -v .)

package tabnasyaml

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// genBlockMap builds `n` "key_i: value i" lines.
func genBlockMap(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "key_%d: value %d\n", i, i)
	}
	return b.String()
}

// genFlowMap builds one line "doc: {k0: v0, k1: v1, ...}".
func genFlowMap(n int) string {
	var b strings.Builder
	b.WriteString("doc: {")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "k%d: v%d", i, i)
	}
	b.WriteString("}\n")
	return b.String()
}

// genLiteralBlock builds a literal block scalar with n lines.
func genLiteralBlock(n int) string {
	var b strings.Builder
	b.WriteString("content: |\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  line %d\n", i)
	}
	return b.String()
}

// genAnchorAlias builds one anchor with 20 keys and n aliases that
// reference it — stresses deepCopy.
func genAnchorAlias(n int) string {
	var b strings.Builder
	b.WriteString("base: &base\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "  k%d: v%d\n", i, i)
	}
	b.WriteString("refs:\n")
	for i := 0; i < n; i++ {
		b.WriteString("  - *base\n")
	}
	return b.String()
}

// timedMin returns the fastest of `iters` parses (in ms).
func timedMin(src string, iters int) float64 {
	best := time.Duration(1<<62 - 1)
	for i := 0; i < iters; i++ {
		start := time.Now()
		if _, err := Parse(src); err != nil {
			return -1
		}
		d := time.Since(start)
		if d < best {
			best = d
		}
	}
	return float64(best.Microseconds()) / 1000.0 // ms
}

// TestScaling is a "benchmark" that runs under go test so we can see the
// growth curve for each input shape. Not a pass/fail test.
func TestScaling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scaling benchmark in short mode")
	}

	shapes := map[string]func(int) string{
		"blockMap":     genBlockMap,
		"flowMap":      genFlowMap,
		"literalBlock": genLiteralBlock,
		"anchorAlias":  genAnchorAlias,
	}
	sizes := []int{100, 250, 500, 1000, 2000, 4000}

	t.Logf("Shape           N      bytes      time   µs/byte     slope")
	t.Logf("----------------------------------------------------------")
	for _, name := range []string{"blockMap", "flowMap", "literalBlock", "anchorAlias"} {
		gen := shapes[name]
		var prev float64 = -1
		for _, n := range sizes {
			src := gen(n)
			ms := timedMin(src, 3)
			if ms < 0 {
				t.Logf("%-14s %5d %10d   parse failed", name, n, len(src))
				continue
			}
			perByte := (ms * 1000) / float64(len(src))
			slope := "-"
			if prev >= 0 {
				slope = fmt.Sprintf("%.2fx", perByte/prev)
			}
			t.Logf("%-14s %5d %10d %8.2fms %8.3fµs %8s",
				name, n, len(src), ms, perByte, slope)
			prev = perByte
		}
		t.Log("")
	}
}
