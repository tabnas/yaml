package tabnasyaml

import (
	"encoding/json"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
)

// TestGuideRelaxedJSONRecipe holds go/doc/guide.md's "Parse relaxed JSON
// without the YAML grammar" section to what it says (tabnas/yaml#83): a
// second instance without the plugin parses relaxed JSON, and excluding
// the yaml rule group from a YAML instance does not.
func TestGuideRelaxedJSONRecipe(t *testing.T) {
	asJSON := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := `{"a":1,"b":["x","y"]}`

	yaml := MakeJsonic()
	relaxed := jsonic.Make()

	got, err := yaml.Parse("a: 1\nb: [x, y]\n")
	if err != nil || asJSON(got) != want {
		t.Fatalf("yaml instance: got %v, %v; want %s", got, err, want)
	}
	got, err = relaxed.Parse("{a: 1, b: [x, y]}")
	if err != nil || asJSON(got) != want {
		t.Fatalf("relaxed instance: got %v, %v; want %s", got, err, want)
	}

	// The recipe the guide used to give. If this starts working, the
	// guide can offer it again.
	excluded := MakeJsonic()
	excluded.SetOptions(jsonic.Options{Rule: &jsonic.RuleOptions{Exclude: "yaml"}})
	if v, err := excluded.Parse("{a: 1, b: [x, y]}"); err == nil {
		t.Fatalf("excluding the yaml group now parses (%s): update go/doc/guide.md", asJSON(v))
	}
}
