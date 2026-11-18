package tabnasyaml

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	tabnas "github.com/tabnas/parser/go"
)

// THE RULE PASSES NAME ANCHORS AND MERGED KEYS. The Go twin of
// ts/test/counters.test.ts, which says why.

// named parses src and answers what its passes name: the anchors each val
// names as its own on its open pass, in the order the vals open, and the
// keys each map names as merged on its close pass, each with the number
// of its source, as "key=source", sorted, in the order the maps close.
func named(t *testing.T, src string) ([]string, [][]string) {
	t.Helper()
	anchors := []string{}
	merged := [][]string{}
	j := MakeJsonic()
	j.SubRuleDone(func(rule *tabnas.Rule, _ *tabnas.Context, done tabnas.RuleDone) {
		if rule.Name == "val" && done.State == tabnas.OPEN {
			var own []string
			for k, v := range rule.N {
				if strings.HasPrefix(k, "&") && v == rule.I {
					own = append(own, k[1:])
				}
			}
			if marked, ok := rule.N["yamlAnchored"]; (ok && marked == rule.I) != (len(own) > 0) {
				t.Errorf("%q: val %d: %v", src, rule.I, rule.N)
			}
			sort.Strings(own)
			anchors = append(anchors, own...)
		}
		if rule.Name == "map" && done.State == tabnas.CLOSE {
			var keys []string
			for k, v := range rule.N {
				if strings.HasPrefix(k, "<<") {
					keys = append(keys, fmt.Sprintf("%s=%d", k[2:], v))
				}
			}
			if len(keys) > 0 {
				sort.Strings(keys)
				merged = append(merged, keys)
			}
		}
	})
	if _, err := j.Parse(src); err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return anchors, merged
}

func TestAnAnchoredValNamesItsAnchorsOnItsOwnPasses(t *testing.T) {
	for _, c := range []struct {
		src  string
		want []string
	}{
		{"a: &x [1, 2]\nb: &y\n  p: 1\nc: *x\nd: &z 3\n", []string{"x", "y", "z"}},
		{"a: &o [&i [1], 2]\nb: *o\n", []string{"o", "i"}},
		{"- &a\n  - 1\n- *a\n", []string{"a"}},
		{"a: [1, 2]\nb: {c: 3}\n", []string{}},
	} {
		if anchors, _ := named(t, c.src); !reflect.DeepEqual(anchors, c.want) {
			t.Errorf("%q: %v, want %v", c.src, anchors, c.want)
		}
	}
}

func TestTheAnchoredValNamesTheAnchorOnItsClosePassToo(t *testing.T) {
	var passes []tabnas.RuleState
	j := MakeJsonic()
	j.SubRuleDone(func(rule *tabnas.Rule, _ *tabnas.Context, done tabnas.RuleDone) {
		if v, ok := rule.N["&x"]; ok && rule.Name == "val" && v == rule.I && rule.N["yamlAnchored"] == rule.I {
			passes = append(passes, done.State)
		}
	})
	if _, err := j.Parse("a: &x {p: [1]}\nb: *x\n"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(passes, []tabnas.RuleState{tabnas.OPEN, tabnas.CLOSE}) {
		t.Errorf("%v", passes)
	}
}

func TestAMapNamesTheKeysItsMergeBroughtAndOnlyThoseWithTheirSources(t *testing.T) {
	for _, c := range []struct {
		src  string
		want [][]string
	}{
		{"a: &a {x: 1, y: 1}\nb:\n  y: 2\n  <<: *a\n", [][]string{{"x=1"}}},
		{"a: &a {x: 1}\nb: &b {y: 2, x: 3}\nc: {<<: [*a, *b], z: 3}\n", [][]string{{"x=1", "y=2"}}},
		// Only a sequence's mappings are sources, and only they are counted.
		{"a: &a {x: 1}\nb: &b {y: 2, x: 3}\nc: {<<: [1, {}, *a, [w], *b]}\n", [][]string{{"x=2", "y=3"}}},
		{"a: &a {x: 1}\nb:\n  x: 2\n  <<: *a\n", [][]string{}},
		{"\"<<\": {x: 1}\nb: 2\n", [][]string{}},
		{"- <<: {x: 1}\n  b: 2\n", [][]string{}},
		{"a: {<<: 1, b: 2}\n", [][]string{}},
	} {
		if _, merged := named(t, c.src); !reflect.DeepEqual(merged, c.want) {
			t.Errorf("%q: %v, want %v", c.src, merged, c.want)
		}
	}
}

// aliases parses src and answers what its passes show of an alias: each
// rule whose first open token is one, with the value that token carries,
// as "rule=json". The part reads an alias to a mapping or a sequence from
// that token, on the open pass of the rule that reads it: a val, or, for
// an alias on the line after its key, the indent the val pushed.
func aliases(t *testing.T, src string) []string {
	t.Helper()
	seen := []string{}
	j := MakeJsonic()
	j.SubRuleDone(func(rule *tabnas.Rule, _ *tabnas.Context, done tabnas.RuleDone) {
		if done.State == tabnas.OPEN && rule.ON > 0 &&
			len(rule.O[0].Src) > 0 && '*' == rule.O[0].Src[0] {
			b, err := json.Marshal(specCanon(rule.O[0].Val))
			if err != nil {
				t.Fatalf("%q: %v", src, err)
			}
			seen = append(seen, rule.Name+"="+string(b))
		}
	})
	if _, err := j.Parse(src); err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return seen
}

func TestAnAliasIsTheFirstTokenOfTheRuleThatReadsItAValOrAnIndent(t *testing.T) {
	for _, c := range []struct {
		src  string
		want []string
	}{
		{"a: &q [1]\nb: *q\n", []string{"val=[1]"}},
		{"x: &x [1]\na:\n  *x\n", []string{"indent=[1]"}},
		{"x: &m {p: 1}\na:\n  *m\n", []string{`indent={"p":1}`}},
		{"x: &s hi\na:\n  *s\n", []string{`indent="hi"`}},
		{"x: &x [1]\nl:\n  -\n    *x\n", []string{"indent=[1]"}},
		{"x: &x [1]\na:\n  *x\nb: *x\n", []string{"indent=[1]", "val=[1]"}},
	} {
		if got := aliases(t, c.src); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: the passes show %v, not %v", c.src, got, c.want)
		}
	}
}
