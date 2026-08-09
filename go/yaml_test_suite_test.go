/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

// Official YAML Test Suite conformance — the Go half of the repo's
// conformance dial, and a faithful port of ts/test/yaml-test-suite.test.ts:
// same case gathering, same three groups, same STRICT comparison, same two
// shared ledger files. Read that file's header for the full rationale.
//
// Corpus: https://github.com/yaml/yaml-test-suite (`data` branch), vendored
// at test/yaml-test-suite (relative to the repo root).
//
// EVERY case is asserted. There is no skip list and no group that is merely
// gathered: a conformance suite that quietly does not run reports green while
// measuring nothing.
//
//	valid-parse          in.json present: must parse AND deep-equal it,
//	                     strictly, across EVERY document in the stream.
//	expected-errors      `error` present: must be REJECTED, unless listed in
//	                     test/yaml-test-suite-lenient.tsv.
//	valid-parse-novalue  neither file: must PARSE, unless listed in
//	                     test/yaml-test-suite-unparsed.tsv.
//
// The value comparison used to be a `deepLooseEqual` that equated the number
// 1 with the string "1", and an array with an object carrying the same index
// keys, and it compared only the FIRST document of a multi-document stream.
// All of that hid real conformance failures and is gone.

package tabnasyaml

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
)

var suiteDir = filepath.Join("..", "test", "yaml-test-suite")

// lenientFile is the shared ledger of suite `error` cases this parser accepts.
// unparsedFile is the shared ledger of parse-only cases it still rejects.
// The TS runner (ts/test/yaml-test-suite.test.ts) reads the same two files, so
// the two runtimes cannot drift. See each file's own header.
var (
	lenientFile  = filepath.Join("..", "test", "yaml-test-suite-lenient.tsv")
	unparsedFile = filepath.Join("..", "test", "yaml-test-suite-unparsed.tsv")
)

// loadLedger reads `<case id> <TAB> <description>`, ignoring # comments and
// blank lines.
func loadLedger(t *testing.T, file string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("cannot read %s: %v", file, err)
	}
	out := map[string]bool{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[strings.SplitN(line, "\t", 2)[0]] = true
	}
	return out
}

// suiteCase mirrors the TS TestCase shape.
type suiteCase struct {
	id       string
	dir      string
	name     string
	hasJSON  bool
	hasError bool
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var numericSubdirRe = regexp.MustCompile(`^\d+$`)

// gatherSuiteCases mirrors gatherTests() in ts/test/yaml-test-suite.test.ts:
// gathers all test case directories, including numbered sub-tests
// (AB12/00, AB12/01, ...), skipping non-test directories.
func gatherSuiteCases(t *testing.T) []suiteCase {
	t.Helper()

	entries, err := os.ReadDir(suiteDir)
	if err != nil {
		t.Fatalf("cannot read yaml-test-suite dir %s: %v", suiteDir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var cases []suiteCase
	for _, entry := range names {
		dir := filepath.Join(suiteDir, entry)

		// Check for sub-tests (00/, 01/, ...).
		subEntries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("cannot read case dir %s: %v", dir, err)
		}
		var subDirs []string
		for _, se := range subEntries {
			if se.IsDir() && numericSubdirRe.MatchString(se.Name()) {
				subDirs = append(subDirs, se.Name())
			}
		}
		sort.Strings(subDirs)

		// Skip non-test directories (e.g. "tags" metadata directory).
		if !fileExists(filepath.Join(dir, "in.yaml")) && !fileExists(filepath.Join(dir, "===")) {
			hasNumberedSubs := len(subDirs) > 0 &&
				fileExists(filepath.Join(dir, subDirs[0], "in.yaml"))
			if !hasNumberedSubs {
				continue
			}
		}

		mk := func(id, d string) suiteCase {
			return suiteCase{
				id:       id,
				dir:      d,
				name:     readCaseName(d, id),
				hasJSON:  fileExists(filepath.Join(d, "in.json")),
				hasError: fileExists(filepath.Join(d, "error")),
			}
		}

		if len(subDirs) > 0 {
			for _, sub := range subDirs {
				subDir := filepath.Join(dir, sub)
				if !fileExists(filepath.Join(subDir, "in.yaml")) {
					continue
				}
				cases = append(cases, mk(entry+"/"+sub, subDir))
			}
		} else {
			cases = append(cases, mk(entry, dir))
		}
	}

	return cases
}

// readCaseName reads the '===' description file, falling back to the id.
func readCaseName(dir, fallback string) string {
	data, err := os.ReadFile(filepath.Join(dir, "==="))
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(data))
}

// parseJSONStream mirrors parseJsonStream() in the TS runner. in.json is a
// STREAM of JSON documents, one per YAML document — not a single JSON value.
// An empty file is a stream of zero documents, which is a real expectation
// ("this input yields no document"), not a parse failure.
func parseJSONStream(raw string) ([]any, error) {
	var docs []any
	rest := strings.TrimSpace(raw)

	for len(rest) > 0 {
		depth := 0
		inString := false
		escape := false
		cut := -1

		for i := 0; i < len(rest); i++ {
			ch := rest[i]

			switch {
			case escape:
				escape = false
			case inString:
				if ch == '\\' {
					escape = true
				} else if ch == '"' {
					inString = false
				}
			case ch == '"':
				inString = true
			case ch == '{' || ch == '[':
				depth++
			case ch == '}' || ch == ']':
				depth--
			}

			if inString || escape || depth != 0 {
				continue
			}

			// A document boundary must be end-of-input or whitespace,
			// otherwise the bare number 123 would be cut after its first digit.
			if i+1 < len(rest) && !isSpaceByte(rest[i+1]) {
				continue
			}

			var v any
			if err := json.Unmarshal([]byte(rest[:i+1]), &v); err == nil {
				docs = append(docs, v)
				cut = i + 1
				break
			}
		}

		if cut == -1 {
			// Not a JSON document stream. Surface it rather than silently
			// comparing against nil, which is what the old runner did.
			end := 80
			if len(rest) < end {
				end = len(rest)
			}
			return nil, fmt.Errorf("unparseable in.json remainder: %q", rest[:end])
		}

		rest = strings.TrimSpace(rest[cut:])
	}

	return docs, nil
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

// canon mirrors canon() in the TS runner: canonicalise a parse result for
// STRICT structural comparison. The engine's insertion-ordered *OrderedMap
// becomes a plain map (order is not part of the expectation — in.json is
// JSON), and the non-finite numbers YAML can spell but JSON cannot become
// marker strings. Nothing here coerces between types: a string "1" stays a
// string and will NOT match the number 1.
func canon(v any) any {
	switch c := v.(type) {
	case nil:
		return nil
	case float64:
		return canonFloat(c)
	case float32:
		return canonFloat(float64(c))
	case int:
		return float64(c)
	case int64:
		return float64(c)
	case json.Number:
		f, err := c.Float64()
		if err != nil {
			return c.String()
		}
		return canonFloat(f)
	case *jsonic.OrderedMap:
		if c == nil {
			return nil
		}
		return canonMap(c.Vals)
	case jsonic.OrderedMap:
		return canonMap(c.Vals)
	case map[string]any:
		return canonMap(c)
	case []any:
		out := make([]any, len(c))
		for i, e := range c {
			out[i] = canon(e)
		}
		return out
	}
	if jsonic.IsUndefined(v) {
		return "@@UNDEFINED"
	}
	return v
}

func canonFloat(f float64) any {
	if math.IsNaN(f) {
		return "@@NaN"
	}
	if math.IsInf(f, 1) {
		return "@@Infinity"
	}
	if math.IsInf(f, -1) {
		return "@@-Infinity"
	}
	return f
}

func canonMap(m map[string]any) any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = canon(v)
	}
	return out
}

// noDocument reports whether a parse result represents "no document at all",
// which JSON cannot spell. Go yields nil (or the engine's undefined marker).
func noDocument(v any) bool {
	return v == nil || jsonic.IsUndefined(v)
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}

func readSuiteYaml(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "in.yaml"))
	if err != nil {
		t.Fatalf("cannot read in.yaml in %s: %v", dir, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// suiteParse runs Parse, converting a panic into an error-like failure
// marker so a single bad case cannot abort the whole run (the TS runner
// wraps parse in try/catch, which catches everything).
func suiteParse(src string) (result any, err error, panicked any) {
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
	}()
	result, err = Parse(src)
	return
}

// ledgerIdsAreKnown holds a ledger to its group: an id that no longer names a
// case there would silently excuse a case that does not exist.
func ledgerIdsAreKnown(t *testing.T, file string, ledger map[string]bool, cases []suiteCase) {
	t.Helper()
	known := make(map[string]bool, len(cases))
	for _, c := range cases {
		known[c.id] = true
	}
	var unknown []string
	for id := range ledger {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Errorf("%s lists ids that are not cases in its group: %s",
			file, strings.Join(unknown, " "))
	}
}

// TestYamlTestSuite is the Go port of ts/test/yaml-test-suite.test.ts.
func TestYamlTestSuite(t *testing.T) {
	allCases := gatherSuiteCases(t)

	var validCases, errorCases, novalueCases []suiteCase
	for _, c := range allCases {
		switch {
		case c.hasJSON && !c.hasError:
			validCases = append(validCases, c)
		case c.hasError:
			errorCases = append(errorCases, c)
		default:
			novalueCases = append(novalueCases, c)
		}
	}

	t.Run("valid-parse", func(t *testing.T) {
		for _, tc := range validCases {
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				inYaml := readSuiteYaml(t, tc.dir)
				inJSONRaw, err := os.ReadFile(filepath.Join(tc.dir, "in.json"))
				if err != nil {
					t.Fatalf("cannot read in.json: %v", err)
				}
				docs, err := parseJSONStream(string(inJSONRaw))
				if err != nil {
					t.Fatalf("%s (%s): %v", tc.id, tc.name, err)
				}

				actual, err, panicked := suiteParse(inYaml)
				if panicked != nil {
					t.Fatalf("Parse panicked for %s (%s): %v", tc.id, tc.name, panicked)
				}
				if err != nil {
					t.Fatalf("%s (%s): valid document REJECTED: %v\n  input: %q",
						tc.id, tc.name, err, inYaml)
				}

				// A zero-document stream cannot be spelled as a JSON value at
				// all, so the assertion is "the parse yielded no document".
				if len(docs) == 0 {
					if !noDocument(actual) {
						t.Errorf("%s (%s): expected NO document (in.json is empty), got %s\n  input: %q",
							tc.id, tc.name, show(canon(actual)), inYaml)
					}
					return
				}

				// The plugin yields the bare value for a single-document
				// stream and a slice of values for a multi-document one.
				var expected any = docs
				if len(docs) == 1 {
					expected = docs[0]
				}

				if !reflect.DeepEqual(canon(actual), canon(expected)) {
					t.Errorf("%s (%s): wrong value\n  input:    %q\n  expected: %s\n  actual:   %s",
						tc.id, tc.name, inYaml, show(canon(expected)), show(canon(actual)))
				}
			})
		}
	})

	t.Run("expected-errors", func(t *testing.T) {
		lenient := loadLedger(t, lenientFile)

		t.Run("lenient ledger has no unknown ids", func(t *testing.T) {
			ledgerIdsAreKnown(t, lenientFile, lenient, errorCases)
		})

		for _, tc := range errorCases {
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				inYaml := readSuiteYaml(t, tc.dir)
				actual, err, panicked := suiteParse(inYaml)
				if panicked != nil {
					t.Fatalf("Parse panicked for %s (%s): %v", tc.id, tc.name, panicked)
				}

				if lenient[tc.id] {
					// A documented leniency: the parser is known to accept
					// this spec-invalid input. If it now rejects it, that is
					// progress — delete the line so the case is held to the
					// strict expectation.
					if err != nil {
						t.Errorf("%s (%s) is now REJECTED (%v). Remove its line from %s "+
							"so the strict expectation applies from here on.",
							tc.id, tc.name, err, lenientFile)
					}
					return
				}

				// Not on the ledger: the parser must reject it.
				if err == nil {
					t.Errorf("%s (%s) parsed without error, but the suite marks it "+
						"invalid and it is not listed in %s.\n  input:  %q\n  parsed: %s",
						tc.id, tc.name, lenientFile, inYaml, show(canon(actual)))
				}
			})
		}
	})

	// The suite publishes no expected value for these, so the value cannot be
	// checked — but they ARE valid YAML, so "it parses" is a real assertion,
	// just a partial one. These 29 cases used to be gathered and then dropped
	// without any assertion at all.
	t.Run("valid-parse-novalue", func(t *testing.T) {
		unparsed := loadLedger(t, unparsedFile)

		t.Run("unparsed ledger has no unknown ids", func(t *testing.T) {
			ledgerIdsAreKnown(t, unparsedFile, unparsed, novalueCases)
		})

		for _, tc := range novalueCases {
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				inYaml := readSuiteYaml(t, tc.dir)
				_, err, panicked := suiteParse(inYaml)
				if panicked != nil {
					t.Fatalf("Parse panicked for %s (%s): %v", tc.id, tc.name, panicked)
				}

				if unparsed[tc.id] {
					// A documented gap: this valid document is still
					// rejected. If it now parses, that is progress.
					if err == nil {
						t.Errorf("%s (%s) now PARSES. Remove its line from %s "+
							"so the expectation applies from here on.",
							tc.id, tc.name, unparsedFile)
					}
					return
				}

				if err != nil {
					t.Errorf("%s (%s): the suite says this is valid YAML but it was "+
						"REJECTED (%v), and it is not listed in %s.\n  input: %q",
						tc.id, tc.name, err, unparsedFile, inYaml)
				}
			})
		}
	})

	// Not a conformance score — a guard that the corpus scored against is the
	// whole, expected one. Without it a truncated checkout silently shrinks
	// the denominator and every ratio above it improves for free.
	t.Run("suite-census", func(t *testing.T) {
		for _, c := range []struct {
			label string
			got   int
			want  int
		}{
			{"total cases", len(allCases), 402},
			{"value-checked cases", len(validCases), 279},
			{"must-fail cases", len(errorCases), 94},
			{"parse-only cases", len(novalueCases), 29},
		} {
			if c.got != c.want {
				t.Errorf("%s: got %d, want %d — the corpus at %s is not the "+
					"expected one; refusing to report a conformance number "+
					"against a different denominator",
					c.label, c.got, c.want, suiteDir)
			}
		}
	})
}
