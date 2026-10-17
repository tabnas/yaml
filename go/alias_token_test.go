package tabnasyaml

import (
	"encoding/json"
	"fmt"
	"testing"

	tabnas "github.com/tabnas/parser/go"
)

// AN ALIAS TO A MAPPING OR A SEQUENCE IS ITS VALUE IN THE TOKEN.
//
// The canonical port's lexer hands an alias to an anchor it has seen as a
// token carrying the anchored value, a copy where that is a mapping or a
// sequence, and an alias to no anchor as the marker {__yamlAlias: name},
// which the val rule reads as the absent value. This port deferred the
// copy to the val rule's close through the same marker, so a consumer of
// the rule passes (a transducer part, tabnas/alchemy#81) could not tell
// an alias to a mapping, whose members the passes do not show and which
// it must refuse, from an alias to nothing, which is null. The token now
// carries the copy, as the canonical port's does; the value the document
// parses to is the same either way.
func TestAliasTokenCarriesTheValue(t *testing.T) {
	cases := []struct{ src, token, value string }{
		{"a: &m {x: 1}\nb: *m\n", "*tabnas.OrderedMap", `{"a":{"x":1},"b":{"x":1}}`},
		{"a: &q [1]\nb: *q\n", "[]interface {}", `{"a":[1],"b":[1]}`},
		{"a: &s hi\nb: *s\n", "string", `{"a":"hi","b":"hi"}`},
		{"a: 1\nb: *nope\n", "map[string]interface {}", `{"a":1,"b":null}`},
	}
	for _, c := range cases {
		var tokens []string
		j := MakeJsonic()
		j.SubRuleDone(func(rule *tabnas.Rule, _ *tabnas.Context, done tabnas.RuleDone) {
			if tabnas.OPEN == done.State && "val" == rule.Name && rule.ON > 0 &&
				len(rule.O[0].Src) > 0 && '*' == rule.O[0].Src[0] {
				tokens = append(tokens, fmt.Sprintf("%T", rule.O[0].Val))
			}
		})
		got, err := j.Parse(c.src)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		b, err := json.Marshal(specCanon(got))
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		if string(b) != c.value {
			t.Errorf("%q parses to %s, not %s", c.src, b, c.value)
		}
		if len(tokens) != 1 || tokens[0] != c.token {
			t.Errorf("%q: the alias token carries %v, not a %s", c.src, tokens, c.token)
		}
	}
}
