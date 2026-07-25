package tabnasyaml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tsvCase represents a single test case from a TSV file.
type tsvCase struct {
	name     string
	input    string
	expected string
}

// unescapeTSV converts literal \n, \r, \t sequences into actual characters.
func unescapeTSV(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				out.WriteByte('\n')
				i++
				continue
			case 'r':
				out.WriteByte('\r')
				i++
				continue
			case 't':
				out.WriteByte('\t')
				i++
				continue
			case '\\':
				out.WriteByte('\\')
				i++
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// loadTSV reads a TSV file and returns test cases.
func loadTSV(t *testing.T, filename string) []tsvCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "test", filename))
	if err != nil {
		t.Fatalf("Failed to read TSV file %s: %v", filename, err)
	}
	var cases []tsvCase
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		cases = append(cases, tsvCase{
			name:     parts[0],
			input:    unescapeTSV(parts[1]),
			expected: unescapeTSV(parts[2]),
		})
	}
	return cases
}

// runTSVSuite runs all test cases from a TSV file.
func runTSVSuite(t *testing.T, filename string) {
	cases := loadTSV(t, filename)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse error: %v\nInput: %q", err, tc.input)
			}
			// The parser now preserves source key order (parsed objects are
			// insertion-ordered *jsonic.OrderedMap), and the expected JSON in
			// these fixtures is authored in source order. Marshaling the
			// result therefore reproduces the fixture byte-for-byte, so we
			// compare against the RAW expected string — this verifies both
			// the values AND the key order. (Re-marshaling the expected via a
			// plain map[string]any would reorder keys alphabetically and hide
			// the order, so we validate the expected fixture is itself valid
			// JSON, then compare strings directly.)
			gotBytes, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("Failed to marshal result: %v", err)
			}
			var expectedVal any
			if err := json.Unmarshal([]byte(tc.expected), &expectedVal); err != nil {
				t.Fatalf("Failed to parse expected JSON %q: %v", tc.expected, err)
			}
			if string(gotBytes) != tc.expected {
				t.Errorf("Mismatch:\n  Got:  %s\n  Want: %s", gotBytes, tc.expected)
			}
		})
	}
}

func TestTSVBasic(t *testing.T) {
	runTSVSuite(t, "basic.tsv")
}

func TestTSVScalars(t *testing.T) {
	runTSVSuite(t, "scalars.tsv")
}

func TestTSVFlow(t *testing.T) {
	runTSVSuite(t, "flow.tsv")
}

func TestTSVStructure(t *testing.T) {
	runTSVSuite(t, "structure.tsv")
}

func TestTSVRealworld(t *testing.T) {
	runTSVSuite(t, "realworld.tsv")
}
