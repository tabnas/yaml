// Copyright (c) 2025 Richard Rodger and other contributors, MIT License

package tabnasyaml

// parity_test.go — cross-runtime conformance, driven by the shared
// `test/spec/*.tsv` fixtures at the repo root (see ../test/AGENTS.md).
//
// The fixture loader, the escape codec, the ERROR:<code> contract and the
// row loop all come from github.com/tabnas/support/go, whose TypeScript
// half ts/test/parity.test.ts uses to run the SAME files — so the two
// implementations cannot drift without one of them going red, and neither
// can the two loaders.
//
// What is left here is only what is specific to yaml: how to build the
// parser for a row's options, and the non-finite-number encoding.

import (
	"encoding/json"
	"math"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
	support "github.com/tabnas/support/go"
)

// TestSpec runs every fixture in the spec directory. FindSpecDir walks up
// from the package directory, and Dir discovers the files by listing, so
// adding a .tsv runs it in both runtimes without touching either runner.
func TestSpec(t *testing.T) {
	dir, err := support.FindSpecDir("")
	if err != nil {
		t.Fatal(err)
	}

	support.Runner{
		// A fresh parser per row: the `opts` column is per-case, and
		// plugin options must not leak from one row into the next.
		ParseRow: func(input string, row *support.Row) (any, error) {
			opts := map[string]any{}
			if raw := row.Named("opts"); "" != raw {
				if err := json.Unmarshal([]byte(raw), &opts); err != nil {
					return nil, err
				}
			}

			j := jsonic.Make()
			if err := j.Use(Yaml, opts); err != nil {
				return nil, err
			}
			return j.Parse(input)
		},

		// Input that yields no value at all cannot be spelled in JSON, so
		// the fixtures write the bare token UNDEFINED.
		//
		// In TypeScript that is `undefined`, and distinct from a document
		// whose value is null. Go returns a bare nil for both today, so
		// an UNDEFINED fixture cannot fail here — a divergence recorded,
		// not hidden, by TestUndefinedIsIndistinguishableFromNull, which
		// fails the moment Go grows a real undefined result. That is the
		// signal to tighten this.
		ParseExpected: func(expected string, _ *support.Row) (any, error) {
			if "UNDEFINED" == expected {
				return nil, nil
			}
			return support.ParseExpect(expected)
		},

		// specCanon FIRST, then the flattening: json.Marshal refuses a
		// non-finite float, so a document holding one would come back
		// unflattened — as an *OrderedMap the comparison cannot see into.
		Normalize: func(v any) any { return jsonFlatten(specCanon(v)) },
	}.Dir(t, dir)
}

// specCanon encodes YAML's non-finite numbers (.inf / .nan), which JSON
// cannot spell and which can appear at any depth, as the marker strings
// the fixtures use. See ../test/AGENTS.md.
func specCanon(v any) any {
	// The engine's undefined sentinel, where one reaches here, is the same
	// "no value" an UNDEFINED cell asks for — see ParseExpected above.
	if nil != v && jsonic.IsUndefined(v) {
		return nil
	}

	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) {
			return "@@NaN"
		}
		if math.IsInf(x, 1) {
			return "@@Infinity"
		}
		if math.IsInf(x, -1) {
			return "@@-Infinity"
		}
		return x
	case *jsonic.OrderedMap:
		out := jsonic.NewOrderedMap()
		for _, k := range x.Keys {
			out.Set(k, specCanon(x.Vals[k]))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = specCanon(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = specCanon(val)
		}
		return out
	}
	return v
}

// jsonFlatten renders a value as JSON and reads it back as plain
// map/slice/float64/string/bool/nil. A value that will not marshal is
// returned as it is: the comparison then fails and prints it, which says
// more than a panic here would.
func jsonFlatten(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}
