package tabnasyaml

// YAML Test Schema conformance: how plain and tagged scalars resolve.
//
// Corpus: https://github.com/perlpunk/yaml-test-schema (MIT, Tina Müller),
// vendored byte-identical at test/yaml-test-schema; see its README.md for
// the source commit. Mirrors ts/test/yaml-test-schema.test.ts, which holds
// the full description: every case must read as the YAML 1.2 core schema
// says unless its input is on the checked ledger
// test/yaml-test-schema-deviations.tsv, and a listed input must still read
// differently, so a repair fails until its line is deleted.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var (
	schemaCorpus = filepath.Join("..", "test", "yaml-test-schema", "schema-core.json")
	schemaLedger = filepath.Join("..", "test", "yaml-test-schema-deviations.tsv")
)

func loadSchemaLedger(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(schemaLedger)
	if err != nil {
		t.Fatalf("read %s: %v", schemaLedger, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var input string
		if err := json.Unmarshal([]byte(strings.SplitN(line, "\t", 2)[0]), &input); err != nil {
			t.Fatalf("ledger line %q: %v", line, err)
		}
		out[input] = true
	}
	return out
}

// schemaExpected is the value the core schema gives, as Parse represents
// it: bool, nil, float64 (infinities and NaN included) or string.
func schemaExpected(t *testing.T, c [3]string) any {
	switch c[0] {
	case "bool":
		return c[1] == "true()"
	case "null":
		return nil
	case "inf":
		if c[1] == "inf()" {
			return math.Inf(1)
		}
		return math.Inf(-1)
	case "nan":
		return math.NaN()
	case "int", "float":
		var f float64
		if err := json.Unmarshal([]byte(c[1]), &f); err != nil {
			t.Fatalf("corpus number %q: %v", c[1], err)
		}
		return f
	case "str":
		return c[1]
	}
	t.Fatalf("unknown type in corpus: %q", c[0])
	return nil
}

// schemaSame is strict: same type and value, NaN equal to NaN.
func schemaSame(got, want any) bool {
	switch w := want.(type) {
	case float64:
		var g float64
		switch n := got.(type) {
		case float64:
			g = n
		case int:
			g = float64(n)
		case int64:
			g = float64(n)
		default:
			return false
		}
		return g == w || (math.IsNaN(g) && math.IsNaN(w))
	case nil:
		return got == nil
	default:
		return got == want
	}
}

func TestYamlTestSchema(t *testing.T) {
	raw, err := os.ReadFile(schemaCorpus)
	if err != nil {
		t.Fatalf("yaml-test-schema corpus is MISSING (%v): restore it with "+
			"`git checkout -- test/yaml-test-schema`", err)
	}
	var corpus map[string][3]string
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	ledger := loadSchemaLedger(t)

	t.Run("census", func(t *testing.T) {
		types := map[string]int{}
		for _, c := range corpus {
			types[c[0]]++
		}
		want := map[string]int{"bool": 12, "float": 32, "inf": 18, "int": 35, "nan": 6, "null": 10, "str": 132}
		if len(corpus) != 245 {
			t.Errorf("corpus has %d cases, want 245", len(corpus))
		}
		for k, n := range want {
			if types[k] != n {
				t.Errorf("corpus has %d %s cases, want %d", types[k], k, n)
			}
		}
		for k := range ledger {
			if _, ok := corpus[k]; !ok {
				t.Errorf("ledger lists an input the corpus does not have: %q", k)
			}
		}
	})

	inputs := make([]string, 0, len(corpus))
	for k := range corpus {
		inputs = append(inputs, k)
	}
	sort.Strings(inputs)
	for _, input := range inputs {
		want := schemaExpected(t, corpus[input])
		listed := ledger[input]
		name, _ := json.Marshal(input)
		t.Run(string(name), func(t *testing.T) {
			got, err := Parse(input)
			matches := err == nil && schemaSame(got, want)
			if listed && matches {
				t.Errorf("%s now reads as the core schema says: DELETE its line "+
					"from test/yaml-test-schema-deviations.tsv", name)
			}
			if !listed && !matches {
				t.Errorf("%s: want %#v, got %#v (err %v)", name, want, got, err)
			}
		})
	}
}
