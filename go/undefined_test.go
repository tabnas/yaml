// Copyright (c) 2026 Richard Rodger and other contributors, MIT License

package tabnasyaml

import "testing"

// TestUndefinedIsIndistinguishableFromNull pins the divergence that
// go/parity_test.go and test/AGENTS.md both say it pins.
//
// Until now it pinned nothing, because it did not exist. Both files
// named it as the thing keeping the UNDEFINED allowance honest — the
// ParseExpected hook in parity_test.go maps the bare token UNDEFINED to
// nil, so an UNDEFINED fixture cannot fail in Go — and a grep for the
// name returned two confident citations and no function. That is worse
// than no gate: someone checking whether the allowance is guarded finds
// the sentences and stops.
//
// This is a REGISTER row, not a fixture. It asserts the divergence is
// still live, so it fails on REPAIR as loudly as on regression, and the
// repair is the signal to delete this test, the allowance in
// ParseExpected, and the paragraph in test/AGENTS.md together.
//
// Measured, both ports:
//
//	input     TypeScript   Go
//	""        undefined    nil
//	"null"    null         nil
//	"---\n"   null         nil
//	"~"       null         nil
//
// So the divergence is specifically the EMPTY DOCUMENT. TypeScript
// distinguishes "no document at all" from "a document whose value is
// null"; Go has one nil and cannot.
func TestUndefinedIsIndistinguishableFromNull(t *testing.T) {
	empty, err := Parse("")
	if err != nil {
		t.Fatalf("empty document: %v", err)
	}
	null, err := Parse("null")
	if err != nil {
		t.Fatalf("null document: %v", err)
	}

	if empty != nil || null != nil {
		t.Fatalf("Go now returns %#v for an empty document and %#v for a "+
			"null one. If those differ, the port has grown a real "+
			"undefined result: DELETE this test, the UNDEFINED allowance "+
			"in parity_test.go's ParseExpected, and the paragraph in "+
			"test/AGENTS.md that describes them", empty, null)
	}

	// The guard that keeps the assertion above from being vacuous: a
	// document that IS a value must still come back as that value, or
	// "both are nil" would be satisfied by a parser that returns nil for
	// everything.
	val, err := Parse("a: 1")
	if err != nil {
		t.Fatalf("value document: %v", err)
	}
	if val == nil {
		t.Fatal("sanity: a document with a value parsed to nil, so " +
			"comparing two nils above proves nothing")
	}
}
