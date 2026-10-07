// Copyright (c) 2025 Richard Rodger and other contributors, MIT License

package tabnasyaml

// parity_test.go — cross-runtime conformance, driven by the shared
// `test/spec/*.tsv` fixtures at the repo root (see ../test/AGENTS.md).
//
// The fixture loader, the escape codec, the ERROR:<code> contract and the
// row loop all come from github.com/tabnas/support/go, whose TypeScript
// and Rust halves ts/test/parity.test.ts and rs/tests/parity_test.rs use to
// run the SAME files — so the implementations cannot drift without one of
// them going red, and neither can the loaders.
//
// What is left here is only what is specific to yaml: how to build the
// parser for a row's options, the non-finite-number encoding, and the
// opt-in key-order check.

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
	support "github.com/tabnas/support/go"
)

// TestSpec runs every fixture in the spec directory. FindSpecDir walks up
// from the package directory, and Dir discovers the files by listing, so
// adding a .tsv runs it in every runtime without touching any runner.
func TestSpec(t *testing.T) {
	dir, err := support.FindSpecDir("")
	if err != nil {
		t.Fatal(err)
	}

	specRunner(nil).Dir(t, dir)

	// The same fixtures again, each through a parser that has had a
	// SetOptions call naming nothing. SetOptions rebuilds the lexer config
	// from options, so a hook the plugin set anywhere else is gone after
	// it, and this pass is what shows it: the number and text checks were
	// once written onto the live config, and 360 of these rows failed
	// after such a call (#81).
	t.Run("after an unrelated SetOptions", func(t *testing.T) {
		specRunner(func(j *jsonic.Jsonic) { j.SetOptions(jsonic.Options{}) }).Dir(t, dir)
	})
}

// specRunner is the shared runner. prepare, when given, runs on each
// row's parser after the plugin is installed and before the parse.
func specRunner(prepare func(*jsonic.Jsonic)) support.Runner {
	return support.Runner{
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
			if prepare != nil {
				prepare(j)
			}
			got, err := j.Parse(input)
			// An ERROR row is the runner's business: a parse that should
			// have failed is reported as the value it returned.
			if err != nil || support.IsErrorExpect(row.Named("expected")) {
				return got, err
			}
			if ordered, err := keysOrdered(row); err != nil || !ordered {
				return got, err
			}
			return withKeyOrder(got)
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
		ParseExpected: func(expected string, row *support.Row) (any, error) {
			if "UNDEFINED" == expected {
				return nil, nil
			}
			ordered, err := keysOrdered(row)
			if err != nil {
				return nil, err
			}
			if !ordered {
				return support.ParseExpect(expected)
			}
			// encoding/json reads an object into a map, which forgets
			// the order the cell writes its keys in; the engine's
			// OrderedMap keeps it. Wrapping the cell lets one decode
			// read any JSON value, not only an object.
			holder := jsonic.NewOrderedMap()
			if err := json.Unmarshal([]byte(`{"v":`+expected+`}`), holder); err != nil {
				return nil, fmt.Errorf("invalid expected JSON: %q: %w", expected, err)
			}
			return withKeyOrder(holder.Vals["v"])
		},

		// specCanon FIRST, then the flattening: json.Marshal refuses a
		// non-finite float, so a document holding one would come back
		// unflattened — as an *OrderedMap the comparison cannot see into.
		Normalize: func(v any) any { return jsonFlatten(specCanon(v)) },
	}
}

// keyOrderMember is the member withKeyOrder adds to every mapping.
const keyOrderMember = "@@keys"

// integerLikeKey matches a key JavaScript enumerates first, in numeric
// order, whatever order the source wrote it in.
var integerLikeKey = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// keysOrdered reads a row's `keys` cell. KEY ORDER is opt-in, row by row:
// the runner's comparison ignores it, because ADR-15 keeps key order out
// of the value contract (a JavaScript object puts an integer-like key
// first). Where the order IS the behaviour under test, such as where a
// merge key puts the keys it brings in, the row writes `ordered` there,
// and withKeyOrder then runs on BOTH sides. The TypeScript and Rust
// runners do the same, so the row pins the order in every runtime. See
// ../test/AGENTS.md.
func keysOrdered(row *support.Row) (bool, error) {
	switch cell := row.Named("keys"); cell {
	case "":
		return false, nil
	case "ordered":
		return true, nil
	default:
		return false, fmt.Errorf("keys cell %q: write \"ordered\" or leave it empty", cell)
	}
}

// withKeyOrder copies a value so that every mapping lists its keys, in
// order, under keyOrderMember. The comparison sees that list as a slice,
// whose order counts. A key that cannot be ordered the same way in every
// runtime is refused rather than compared: an integer-like key, which
// JavaScript reorders, and keyOrderMember itself. So is a plain
// map[string]any, which has no order to compare.
func withKeyOrder(v any) (any, error) {
	switch x := v.(type) {
	case *jsonic.OrderedMap:
		out := make(map[string]any, len(x.Keys)+1)
		keys := make([]any, 0, len(x.Keys))
		for _, k := range x.Keys {
			if keyOrderMember == k || integerLikeKey.MatchString(k) {
				return nil, fmt.Errorf("the key %q cannot be ordered the same way "+
					"in every runtime, so a `keys: ordered` row cannot hold it (ADR-15)", k)
			}
			val, err := withKeyOrder(x.Vals[k])
			if err != nil {
				return nil, err
			}
			out[k] = val
			keys = append(keys, k)
		}
		out[keyOrderMember] = keys
		return out, nil
	case map[string]any:
		return nil, fmt.Errorf("a map[string]any has no key order to compare: %v", x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			val, err := withKeyOrder(item)
			if err != nil {
				return nil, err
			}
			out[i] = val
		}
		return out, nil
	}
	return v, nil
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
