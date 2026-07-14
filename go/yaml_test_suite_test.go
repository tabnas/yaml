/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

// Official YAML Test Suite integration tests.
// Test data from: https://github.com/yaml/yaml-test-suite (data branch),
// shared with the TS runner at test/yaml-test-suite (relative to repo root).
//
// Each test case has:
//   in.yaml  — input YAML
//   in.json  — expected JSON output (if valid parse)
//   error    — marker file indicating expected parse failure
//
// Tests without in.json or error are skipped (no way to validate output).
//
// Port of ts/test/yaml-test-suite.test.ts — same gathering rules, same
// loose comparison semantics, same error-case handling (error cases are
// exercised but not asserted: Jsonic is intentionally lenient, so some
// invalid YAML is silently accepted; we record but don't fail).

package tabnasyaml

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var suiteDir = filepath.Join("..", "test", "yaml-test-suite")

// suiteSkip lists known-failing test IDs, skipped with reasons.
// The TS runner's SKIP list is currently empty; entries here are
// Go-side-only known behavior differences. These exercise YAML spec
// features beyond this Jsonic-based subset parser, or edge cases where
// Jsonic's base grammar conflicts with YAML semantics. As parser
// coverage improves, entries should be removed and tests should pass.
var suiteSkip = map[string]string{}

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
		if e.IsDir() {
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

		if len(subDirs) > 0 {
			for _, sub := range subDirs {
				subDir := filepath.Join(dir, sub)
				if !fileExists(filepath.Join(subDir, "in.yaml")) {
					continue
				}
				cases = append(cases, suiteCase{
					id:       entry + "/" + sub,
					dir:      subDir,
					name:     readCaseName(subDir, entry+"/"+sub),
					hasJSON:  fileExists(filepath.Join(subDir, "in.json")),
					hasError: fileExists(filepath.Join(subDir, "error")),
				})
			}
		} else {
			cases = append(cases, suiteCase{
				id:       entry,
				dir:      dir,
				name:     readCaseName(dir, entry),
				hasJSON:  fileExists(filepath.Join(dir, "in.json")),
				hasError: fileExists(filepath.Join(dir, "error")),
			})
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

// parseExpectedJSON mirrors parseExpectedJson() in the TS runner.
// Some test cases produce multiple JSON values (one per YAML document);
// only the first document is extracted for comparison.
func parseExpectedJSON(raw string) (value any, multiDoc bool) {
	trimmed := strings.TrimSpace(raw)

	// Try parsing as a single JSON value first.
	var single any
	if err := json.Unmarshal([]byte(trimmed), &single); err == nil {
		return single, false
	}

	// Multi-document: multiple JSON values concatenated.
	// Try to extract just the first one by scanning for a balanced
	// top-level value prefix.
	depth := 0
	inString := false
	escape := false

	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]

		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}

		if ch == '{' || ch == '[' {
			depth++
		}
		if ch == '}' || ch == ']' {
			depth--
		}

		if depth == 0 && i > 0 {
			// We might be at the end of a top-level value.
			candidate := trimmed[:i+1]
			var v any
			if err := json.Unmarshal([]byte(candidate), &v); err == nil {
				return v, true
			}
		}
	}

	// Last resort: just return nil.
	return nil, true
}

// asFloat reports whether v is a numeric value, normalizing to float64.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// jsNumberString renders a float64 the way JavaScript's String(n) does
// (shortest round-trip form), which is what the TS runner compares
// against when one side is a string and the other a number.
func jsNumberString(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	abs := math.Abs(f)
	if f != 0 && (abs >= 1e21 || abs < 1e-6) {
		// JS switches to exponential notation outside [1e-6, 1e21) and
		// prints the exponent without leading zeros (e.g. "1.5e-7").
		s := strconv.FormatFloat(f, 'e', -1, 64)
		s = strings.Replace(s, "e+0", "e+", 1)
		s = strings.Replace(s, "e-0", "e-", 1)
		return s
	}
	// Decimal form, shortest round-trip (matches JS String(n)).
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// looseNumEq mirrors Object.is on JS numbers: NaN equals NaN, and
// +0 / -0 are distinct.
func looseNumEq(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	return a == b && math.Signbit(a) == math.Signbit(b)
}

// asKeyMap views a JSON-ish composite value ([]any or map[string]any) as
// a string-keyed map, matching JS Object.keys semantics where array
// indices become string keys.
func asKeyMap(v any) (map[string]any, bool) {
	switch c := v.(type) {
	case map[string]any:
		return c, true
	case []any:
		m := make(map[string]any, len(c))
		for i, e := range c {
			m[strconv.Itoa(i)] = e
		}
		return m, true
	}
	return nil, false
}

// deepLooseEqual mirrors deepLooseEqual() in the TS runner: a deep
// comparison tolerant of number/string type differences that arise from
// YAML type resolution differences.
func deepLooseEqual(actual, expected any) bool {
	if actual == nil && expected == nil {
		return true
	}
	if actual == nil || expected == nil {
		return false
	}

	af, aNum := asFloat(actual)
	ef, eNum := asFloat(expected)

	// Compare numbers loosely (string "1" vs number 1).
	if aNum && eNum {
		return looseNumEq(af, ef)
	}
	as, aStr := actual.(string)
	es, eStr := expected.(string)
	if eNum && aStr {
		return jsNumberString(ef) == as
	}
	if eStr && aNum {
		return es == jsNumberString(af)
	}

	// Boolean strict comparison.
	ab, aBool := actual.(bool)
	eb, eBool := expected.(bool)
	if aBool || eBool {
		return aBool && eBool && ab == eb
	}

	// Composites (arrays/objects). Mirrors the TS object branch, where
	// Object.keys treats arrays and plain objects uniformly, so an array
	// may loosely equal an object with the same index keys.
	am, aComp := asKeyMap(actual)
	em, eComp := asKeyMap(expected)
	if aComp && eComp {
		// Array-vs-array compares lengths first in TS; the key-map view
		// preserves that (same key count requirement).
		if len(am) != len(em) {
			return false
		}
		for k, ev := range em {
			if !deepLooseEqual(am[k], ev) {
				return false
			}
		}
		return true
	}

	// String comparison fallback.
	if aStr && eStr {
		return as == es
	}
	return false
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

// TestYamlTestSuite is the Go port of ts/test/yaml-test-suite.test.ts.
func TestYamlTestSuite(t *testing.T) {
	allCases := gatherSuiteCases(t)

	var validCases, errorCases, skipCases []suiteCase
	for _, c := range allCases {
		switch {
		case c.hasJSON && !c.hasError:
			validCases = append(validCases, c)
		case c.hasError:
			errorCases = append(errorCases, c)
		default:
			skipCases = append(skipCases, c)
		}
	}

	t.Run("valid-parse", func(t *testing.T) {
		for _, tc := range validCases {
			tc := tc
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				if reason, skip := suiteSkip[tc.id]; skip {
					t.Skipf("known failure: %s", reason)
				}

				inYaml := readSuiteYaml(t, tc.dir)
				inJSONRaw, err := os.ReadFile(filepath.Join(tc.dir, "in.json"))
				if err != nil {
					t.Fatalf("cannot read in.json: %v", err)
				}
				expected, multiDoc := parseExpectedJSON(string(inJSONRaw))

				actual, err, panicked := suiteParse(inYaml)
				if panicked != nil {
					t.Fatalf("Parse panicked for %s (%s): %v", tc.id, tc.name, panicked)
				}
				if err != nil {
					t.Fatalf("Parse failed for %s (%s): %v", tc.id, tc.name, err)
				}

				// The plugin returns a slice when the source has multiple
				// documents and a scalar otherwise. parseExpectedJSON only
				// extracts the first expected document, so for multi-doc
				// inputs compare actual[0].
				actualForCompare := actual
				if multiDoc {
					if arr, ok := actual.([]any); ok && len(arr) > 0 {
						actualForCompare = arr[0]
					}
				}

				if !deepLooseEqual(actualForCompare, expected) {
					tag := ""
					if multiDoc {
						tag = " [first doc only]"
					}
					expJSON, _ := json.Marshal(expected)
					actJSON, _ := json.Marshal(actual)
					t.Errorf("Mismatch for %s (%s)%s:\n  Expected: %s\n  Actual:   %s",
						tc.id, tc.name, tag, expJSON, actJSON)
				}
			})
		}
	})

	t.Run("expected-errors", func(t *testing.T) {
		accepted := 0
		for _, tc := range errorCases {
			tc := tc
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				if reason, skip := suiteSkip[tc.id]; skip {
					t.Skipf("known failure: %s", reason)
				}

				inYaml := readSuiteYaml(t, tc.dir)
				_, err, panicked := suiteParse(inYaml)
				if panicked != nil {
					t.Fatalf("Parse panicked for %s (%s): %v", tc.id, tc.name, panicked)
				}

				// Note: not all "error" tests will throw — some invalid YAML
				// may be silently accepted by a lenient parser. We record but
				// don't fail on these, since Jsonic is intentionally lenient.
				// (Same policy as the TS runner.)
				if err == nil {
					accepted++
				}
			})
		}
		t.Logf("expected-error cases silently accepted (lenient parser): %d/%d",
			accepted, len(errorCases))
	})

	t.Run("suite-stats", func(t *testing.T) {
		t.Logf("yaml-test-suite stats:")
		t.Logf("  Total test cases: %d", len(allCases))
		t.Logf("  Valid parse (with in.json): %d", len(validCases))
		t.Logf("  Error cases: %d", len(errorCases))
		t.Logf("  Skipped (no in.json, no error): %d", len(skipCases))
	})
}
