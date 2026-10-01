package tabnasyaml

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
	tabnas "github.com/tabnas/parser/go"
)

// A MAPPING'S MEMBER IS NAMED BEFORE ITS VALUE IS BUILT (tabnas/yaml#105).
// The Go twin of ts/test/key-before-value.test.ts, which says what is
// recorded and why: a consumer that follows the rule events is told a
// member's key by a later open pass on the open mapping, the pass of the
// pair rule that pushes the value's rule. A mapping that starts in a
// sequence entry used to name its first member in the pass that opened
// it (yamlElemMap's), so that consumer was never told the key; now
// yamlElemPair reads every pair, the first included.

// told parses src and returns its value as JSON, with what such a
// consumer is told, in order: "open mN" when a mapping first shows at the
// end of an open pass, and "mN key K" when a later open pass on the
// innermost open mapping carries the key K. A mapping stays open until a
// close pass at its depth, on it, that does not replace the rule.
// Sequences are left out: a Go sequence is a slice, which has no identity
// to follow.
func told(t *testing.T, src string) (string, []string) {
	t.Helper()
	type openMap struct {
		node *jsonic.OrderedMap
		d    int
		name string
	}
	var open []openMap
	lines := []string{}
	made := 0
	j := MakeJsonic()
	j.SubRuleDone(func(rule *tabnas.Rule, _ *tabnas.Context, done tabnas.RuleDone) {
		node, ok := rule.Node.(*jsonic.OrderedMap)
		if !ok {
			return
		}
		if tabnas.OPEN == done.State {
			for _, m := range open {
				if m.node == node {
					top := open[len(open)-1]
					if key, ok := rule.U["key"].(string); ok && top.node == node {
						lines = append(lines, top.name+" key "+key)
					}
					return
				}
			}
			name := fmt.Sprintf("m%d", made)
			made++
			open = append(open, openMap{node: node, d: rule.D, name: name})
			lines = append(lines, "open "+name)
			return
		}
		replaces := done.Alt != nil && done.Alt.R != ""
		if n := len(open); !replaces && n > 0 && open[n-1].node == node && open[n-1].d == rule.D {
			open = open[:n-1]
		}
	})
	value, err := j.Parse(src)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return string(out), lines
}

func TestKeyBeforeValue(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		value string
		told  []string
	}{
		{"an explicit key that is a mapping, with a mapping value (V9D5)",
			"- sun: yellow\n- ? earth: blue\n  : moon: white\n",
			`[{"sun":"yellow"},{"earth: blue":{"moon":"white"}}]`,
			[]string{"open m0", "m0 key sun", "open m1", "m1 key earth: blue", "open m2", "m2 key moon"}},
		{"an explicit key whose value is a sequence",
			"- ? a\n  : - b\n    - c\n",
			`[{"a":["b","c"]}]`,
			[]string{"open m0", "m0 key a"}},
		{"an explicit key whose value is a flow mapping",
			"- ? a\n  : {b: 1}\n",
			`[{"a":{"b":1}}]`,
			[]string{"open m0", "m0 key a", "open m1", "m1 key b"}},
		{"an explicit key whose value is a flow sequence",
			"- ? a\n  : [b, c]\n",
			`[{"a":["b","c"]}]`,
			[]string{"open m0", "m0 key a"}},
		{"an explicit key that is a flow collection",
			"- ? [a, b]\n  : c\n",
			`[{"[a, b]":"c"}]`,
			[]string{"open m0", "m0 key [a, b]"}},
		{"an explicit key with no value",
			"- ? a\n",
			`[{"a":null}]`,
			[]string{"open m0", "m0 key a"}},
		{"an implicit key whose value is a mapping",
			"- a:\n    b: 1\n",
			`[{"a":{"b":1}}]`,
			[]string{"open m0", "m0 key a", "open m1", "m1 key b"}},
		{"an implicit key whose value is a sequence",
			"- a:\n  - x\n",
			`[{"a":["x"]}]`,
			[]string{"open m0", "m0 key a"}},
		{"a mapping in a flow sequence entry",
			"[a: {b: 1}]",
			`[{"a":{"b":1}}]`,
			[]string{"open m0", "m0 key a", "open m1", "m1 key b"}},
		{"an explicit key in a flow sequence entry",
			"[? a : {b: 1}]",
			`[{"a":{"b":1}}]`,
			[]string{"open m0", "m0 key a", "open m1", "m1 key b"}},
		{"every later pair is named as the first is",
			"- a: 1\n  b:\n    c: 2\n",
			`[{"a":1,"b":{"c":2}}]`,
			[]string{"open m0", "m0 key a", "m0 key b", "open m1", "m1 key c"}},
		{"a mapping outside a sequence was named first already",
			"? earth: blue\n: moon: white\n",
			`{"earth: blue":{"moon":"white"}}`,
			[]string{"open m0", "m0 key earth: blue", "open m1", "m1 key moon"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			value, lines := told(t, c.src)
			if value != c.value {
				t.Errorf("%q value:\n got  %s\n want %s", c.src, value, c.value)
			}
			if !reflect.DeepEqual(lines, c.told) {
				t.Errorf("%q told:\n got  %q\n want %q", c.src, lines, c.told)
			}
		})
	}
}
