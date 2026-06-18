/* Copyright (c) 2025 Voxgig Ltd, MIT License */

// Regression tests for TS/Go parity bugs that produced "unexpected
// character(s)" errors in real-world OpenAPI/Swagger YAML files.
// Each case is a TS-valid YAML snippet that the Go parser previously rejected.

package tabnasyaml

import (
	"reflect"
	"testing"
)

// TestParity_TrailingCommaAfterDigitInBlock — captured from the GitHub
// OpenAPI spec (`example: id: 12,` lines under `examples:` blocks).
//
// Trigger: a value that starts with a digit and ends with a comma in
// block context (not a flow collection). The number matcher would grab
// `12` and leave `,` as a stray fixed token, causing a grammar error on
// the next line.
//
// Fix: handleNumericColon now detects `hasTrailingComma` and emits the
// whole `12,` as a TX token before the number matcher fires (mirrors
// src/yaml.ts:2204-2266).
func TestParity_TrailingCommaAfterDigitInBlock(t *testing.T) {
	src := "value:\n  id: 12,\n  public_repo: false,\n  title: Intro\n"
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	value, ok := got.(map[string]any)["value"].(map[string]any)
	if !ok {
		t.Fatalf("missing value map: %#v", got)
	}
	if value["id"] != "12," {
		t.Errorf("id: want %q, got %#v", "12,", value["id"])
	}
	if value["public_repo"] != "false," {
		t.Errorf("public_repo: want %q, got %#v", "false,", value["public_repo"])
	}
	if value["title"] != "Intro" {
		t.Errorf("title: want %q, got %#v", "Intro", value["title"])
	}
}

// TestParity_ExplicitKeyInlineBlockMapping — captured from the GitLab
// Swagger spec (path keys with the `? long-key\n: get:\n  ... put: ...`
// pattern).
//
// Trigger: an explicit-key (`?`) entry whose value is on the next line
// starting with `: <key>:` — meaning the value is a block mapping that
// begins on the same line as the explicit-key colon. The Go parser used
// to set `pendingExplicitCL = true` and emit only a single CL token,
// which left no IN token to establish the inner mapping's indent
// context, so the second method (`put:`) was rejected.
//
// Fix: handleExplicitKey detects inline content that itself opens a
// block mapping/sequence and pushes CL+IN to pendingTokens so jsonic
// core sees a proper begin-block sequence (mirrors src/yaml.ts:1891-1931).
func TestParity_ExplicitKeyInlineBlockMapping(t *testing.T) {
	src := `paths:
  ? "/api/foo"
  : get:
      summary: get foo
    put:
      summary: put foo
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	paths, ok := got.(map[string]any)["paths"].(map[string]any)
	if !ok {
		t.Fatalf("missing paths map: %#v", got)
	}
	// The TS implementation keeps the surrounding quotes as part of the
	// explicit-key text. We assert the same shape for parity.
	wantKey := `"/api/foo"`
	pathVal, ok := paths[wantKey]
	if !ok {
		// Fall back to checking unquoted form too — older TS builds may
		// strip the quotes; either is acceptable as long as the value
		// shape is correct.
		pathVal, ok = paths["/api/foo"]
	}
	if !ok {
		t.Fatalf("missing %q (or unquoted) in paths: %#v", wantKey, paths)
	}
	pathMap, ok := pathVal.(map[string]any)
	if !ok {
		t.Fatalf("path value is not a map: %#v", pathVal)
	}
	get, gok := pathMap["get"].(map[string]any)
	put, pok := pathMap["put"].(map[string]any)
	if !gok || !pok {
		t.Fatalf("expected both get and put: %#v", pathMap)
	}
	if got, want := get["summary"], "get foo"; !reflect.DeepEqual(got, want) {
		t.Errorf("get.summary: want %q, got %#v", want, got)
	}
	if got, want := put["summary"], "put foo"; !reflect.DeepEqual(got, want) {
		t.Errorf("put.summary: want %q, got %#v", want, got)
	}
}

// TestParity_ApostropheInsideDoubleQuotedString — captured from the
// Codat OpenAPI spec (description strings containing `[Codat's...]`).
//
// Trigger: an apostrophe inside a double-quoted YAML string is processed
// by the incremental flow-context scanner. Without distinguishing
// apostrophes-in-words from single-quote openers, the scanner would
// enter "in single quote" state and miscount flow depth across the rest
// of the file, eventually producing an unexpected-token error far away.
//
// Fix: flowScanState.advance now skips apostrophes that follow a letter
// or digit (mirrors src/yaml.ts:941-947).
func TestParity_ApostropheInsideDoubleQuotedString(t *testing.T) {
	src := `paths:
  /a:
    get:
      description: "See [Codat's docs](https://example.com/x) for more info."
      tags:
        - api
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	paths, ok := got.(map[string]any)["paths"].(map[string]any)
	if !ok {
		t.Fatalf("missing paths: %#v", got)
	}
	a, ok := paths["/a"].(map[string]any)
	if !ok {
		t.Fatalf("missing /a: %#v", paths)
	}
	get, ok := a["get"].(map[string]any)
	if !ok {
		t.Fatalf("missing get: %#v", a)
	}
	tags, ok := get["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "api" {
		t.Errorf("tags: want [api], got %#v", get["tags"])
	}
}
