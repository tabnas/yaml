package tabnasyaml

import (
	"encoding/json"
	"testing"

	tabnas "github.com/tabnas/parser/go"
)

// AN ANCHORED VALUE HOLDS A STREAMING HOST'S PRUNING UNTIL ITS ANCHOR IS
// RECORDED (tabnas/yaml#138). The Go twin of ts/test/prune-hold.test.ts,
// which says why.

// pruned parses src with every list emptied at its close pass while no
// hold is raised, as a pruning host empties them, in place, and returns
// the value, read back as JSON, with the hold each list's close pass saw,
// in order.
func pruned(t *testing.T, src string) (map[string]any, []int) {
	t.Helper()
	var holds []int
	j := MakeJsonic()
	j.SubRuleDone(func(rule *tabnas.Rule, ctx *tabnas.Context, done tabnas.RuleDone) {
		if tabnas.OPEN == done.State {
			return
		}
		list, ok := rule.NodeCell().Node.([]any)
		if !ok {
			return
		}
		hold, _ := ctx.U[pruneHold].(int)
		holds = append(holds, hold)
		if hold == 0 && len(list) > 0 {
			clear(list)
			rule.SetNode(list[:0])
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
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("%q: %s: %v", src, out, err)
	}
	return m, holds
}

// An alias used as a key names the anchored sequence's text, as the whole
// parse names it, though the host empties every list it may: the hold is
// raised while the anchored value is open, and released once it is
// recorded.
func TestAnAliasKeyToAnAnchoredSequenceSurvivesPruning(t *testing.T) {
	for _, c := range []struct {
		src  string
		keys map[string]string
	}{
		{"a: &l [1, 2]\n*l: x\n", map[string]string{"1,2": "x"}},
		{"a: &o [&i [1, 2], 3]\n*o: x\n*i: y\n", map[string]string{"1,2,3": "x", "1,2": "y"}},
	} {
		value, holds := pruned(t, c.src)
		for key, want := range c.keys {
			if value[key] != want {
				t.Errorf("%q: %q is %v, want %q (%v)", c.src, key, value[key], want, value)
			}
		}
		if _, ok := value[""]; ok {
			t.Errorf("%q: an empty key: %v", c.src, value)
		}
		raised := false
		for _, h := range holds {
			raised = raised || h > 0
		}
		if !raised || len(holds) == 0 || holds[len(holds)-1] != 0 {
			t.Errorf("%q: the holds %v", c.src, holds)
		}
	}
}

// The hold counts the open anchored values: an anchored value inside an
// anchored value raises it a second time.
func TestNestedAnchorsRaiseTheHoldTwice(t *testing.T) {
	_, holds := pruned(t, "a: &o [&i [1, 2], 3]\n*o: x\n*i: y\n")
	top := 0
	for _, h := range holds {
		top = max(top, h)
	}
	if top != 2 {
		t.Errorf("the holds %v", holds)
	}
}

// A merge key's sources are merged at its mapping's close, after the list
// that names them has closed: its pair holds the pruning from its open to
// its close, so the list is merged whole, and the list after it is
// emptied as any other.
func TestAMergeKeyHoldsPruningUntilItsSourcesAreRead(t *testing.T) {
	value, holds := pruned(t, "a: &a {x: 1}\nb: &b {y: 2}\nc:\n  <<: [*a, *b]\n  z: [3]\n")
	out, _ := json.Marshal(value["c"])
	if string(out) != `{"x":1,"y":2,"z":[]}` {
		t.Errorf("c is %s", out)
	}
	raised := false
	for _, h := range holds {
		raised = raised || h > 0
	}
	if !raised || len(holds) == 0 || holds[len(holds)-1] != 0 {
		t.Errorf("the holds %v", holds)
	}
}

// A document with no anchor raises no hold, so the host empties every list
// it would.
func TestADocumentWithNoAnchorHoldsNothing(t *testing.T) {
	value, holds := pruned(t, "a: [1, [2, 3]]\nb: [4]\n")
	if len(holds) == 0 {
		t.Fatalf("no list closed")
	}
	for _, h := range holds {
		if h != 0 {
			t.Errorf("the holds %v", holds)
		}
	}
	out, _ := json.Marshal(value)
	if string(out) != `{"a":[],"b":[]}` {
		t.Errorf("%s", out)
	}
}
