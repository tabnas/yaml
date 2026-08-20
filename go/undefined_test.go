// Copyright (c) 2026 Richard Rodger and other contributors, MIT License

package tabnasyaml

import (
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
)

// parityParse builds the parser the way parity_test.go's runSpecFile
// does, which is the construction the UNDEFINED allowance actually
// governs.
//
// It is NOT Parse(). Parse goes through MakeJsonic, which forces
// Lex.EmptyResult to nil (yaml.go:195), so an empty document is nil there
// by construction whatever the document-end handling does. A gate built
// on Parse would stay green while the path the fixtures run grew a real
// undefined result — the exact repair this is supposed to catch.
func parityParse(t *testing.T, src string) any {
	t.Helper()
	j := jsonic.Make()
	if err := j.Use(Yaml, map[string]any{}); err != nil {
		t.Fatalf("plugin: %v", err)
	}
	v, err := j.Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return v
}

// TestUndefinedIsIndistinguishableFromNull pins the divergence that
// go/parity_test.go and test/AGENTS.md both say it pins.
//
// Until now it pinned nothing, because it did not exist. Both files
// named it as the thing keeping the UNDEFINED allowance honest — the
// ParseExpected hook in parity_test.go maps the bare token UNDEFINED to
// nil, so an UNDEFINED fixture cannot fail in Go — and a grep for the
// name returned two confident citations and no function.
//
// This is a REGISTER row, not a fixture. It asserts the divergence is
// still live, so it fails on REPAIR as loudly as on regression, and the
// repair is the signal to delete this test, the allowance in
// ParseExpected, and the paragraph in test/AGENTS.md together.
//
// Measured in both ports, through the parity construction:
//
//	input        TypeScript   Go
//	""           undefined    nil
//	"..."        undefined    nil
//	"# c\n..."   undefined    nil
//	"null"       null         nil
//
// The first three are the documents that yield no value at all — `...`
// and `# c\n...` are the two UNDEFINED rows in
// test/spec/multi-document.tsv, so they are the inputs the allowance
// exists for. TypeScript distinguishes "no document" from "a document
// whose value is null"; Go has one nil and cannot.
func TestUndefinedIsIndistinguishableFromNull(t *testing.T) {
	// The fixture inputs themselves, not a stand-in. specCanon in
	// parity_test.go normalises a result before comparing, so a sentinel
	// appearing here would be flattened back to nil there — these raw
	// results are the only place it would show.
	for _, src := range []string{"", "...", "# c\n..."} {
		got := parityParse(t, src)
		if got != nil {
			t.Fatalf("a document yielding no value now parses to %#v for "+
				"input %q. Go has grown a real undefined result: DELETE "+
				"this test, the UNDEFINED allowance in parity_test.go's "+
				"ParseExpected, and the paragraph in test/AGENTS.md that "+
				"describes them", got, src)
		}
	}

	// And it is indistinguishable from an explicit null — which is the
	// divergence, not merely that both are nil.
	if null := parityParse(t, "null"); null != nil {
		t.Fatalf("an explicit null now parses to %#v, so it is no longer "+
			"indistinguishable from a document that yielded nothing; this "+
			"test and the allowance it guards both need revisiting", null)
	}

	// The guard that keeps all of the above from being vacuous: a
	// document that IS a value must still come back as that value, or
	// "everything is nil" would be satisfied by a parser that returns nil
	// for every input.
	if val := parityParse(t, "a: 1"); val == nil {
		t.Fatal("sanity: a document with a value parsed to nil through the " +
			"parity construction, so the nil comparisons above prove nothing")
	}
}
