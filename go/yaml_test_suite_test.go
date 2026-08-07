/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

// Official YAML Test Suite conformance — the Go half of
// ts/test/yaml-test-suite.test.ts. Same corpus, same case gathering, same
// three groups, same strict comparison, so the two runtimes are scored on
// exactly the same dial.
//
// Corpus: https://github.com/yaml/yaml-test-suite `data` branch, pinned at
// commit 6ad3d2c62885d82fc349026c136ef560838fdf3d. It is NOT vendored — it is
// fetched by ../scripts/fetch-yaml-test-suite.sh.
//
//	valid-parse          in.json present: must parse AND deep-equal that value.
//	expected-errors      `error` present: must be REJECTED. This half used to
//	                     be exercised and then assert nothing at all, so all 94
//	                     cases "passed" unconditionally.
//	valid-parse-novalue  Neither file: the suite publishes no expected value,
//	                     so the strongest honest check is that the (valid)
//	                     document parses. A documented partial check.
//
// The comparison is STRICT. The previous version used a deepLooseEqual that
// equated the number 1 with the string "1", and compared only the FIRST
// document of a multi-document stream. Both hid real conformance failures.

package tabnasyaml

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	jsonic "github.com/tabnas/jsonic/go"
)

var suiteDir = filepath.Join("..", "test", "yaml-test-suite")

var fetchScript = filepath.Join("..", "scripts", "fetch-yaml-test-suite.sh")

// requireSuite guarantees the corpus is present. This suite must NEVER skip:
// a conformance test that quietly does not run turns a green tick into a lie.
// Try the pinned fetch once, then fail loudly with instructions.
func requireSuite(t *testing.T) {
	t.Helper()
	probe := filepath.Join(suiteDir, "229Q", "in.yaml")
	if fileExists(probe) {
		return
	}

	cmd := exec.Command("bash", fetchScript)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	if !fileExists(probe) {
		t.Fatalf("yaml-test-suite corpus is MISSING at %s.\n"+
			"It is deliberately not committed (third-party corpus). Fetch it with:\n"+
			"    bash scripts/fetch-yaml-test-suite.sh\n"+
			"This suite refuses to skip: without the corpus there is no "+
			"conformance number, and a green run would be meaningless.", suiteDir)
	}
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

// gatherSuiteCases mirrors gatherTests() in the TS runner: all case
// directories, including numbered sub-tests (AB12/00, AB12/01, ...).
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

		// Skip metadata directories ("name", "tags") that hold no cases.
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

// parseJSONStream splits in.json into ALL of its documents. The previous
// runner kept only the first, which scored every multi-document case on a
// fraction of its expected output.
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
			if i+1 < len(rest) && !unicode.IsSpace(rune(rest[i+1])) {
				continue
			}

			var v any
			dec := json.NewDecoder(strings.NewReader(rest[:i+1]))
			dec.UseNumber()
			if err := dec.Decode(&v); err == nil {
				// Reject a partial decode (e.g. "12" out of "12x" is already
				// excluded by the whitespace rule, but be explicit).
				if _, err2 := dec.Token(); err2 != nil {
					cut = i + 1
				}
			}
			if cut >= 0 {
				break
			}
		}

		if cut < 0 {
			head := rest
			if len(head) > 80 {
				head = head[:80]
			}
			return nil, fmt.Errorf("unparseable in.json remainder: %q", head)
		}

		var v any
		dec := json.NewDecoder(strings.NewReader(rest[:cut]))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		docs = append(docs, v)
		rest = strings.TrimSpace(rest[cut:])
	}

	return docs, nil
}

// canon maps a value (parsed YAML result or decoded JSON expectation) onto a
// single comparable shape: plain map[string]any / []any, float64 numbers, and
// marker strings for YAML's non-finite numbers, which JSON cannot spell.
// NOTHING here coerces between types — a string "1" stays a string and will
// not match the number 1.
func canon(v any) any {
	switch c := v.(type) {
	case nil:
		return nil
	case json.Number:
		f, err := c.Float64()
		if err != nil {
			return c.String()
		}
		return canonFloat(f)
	case float64:
		return canonFloat(c)
	case float32:
		return canonFloat(float64(c))
	case int:
		return float64(c)
	case int8:
		return float64(c)
	case int16:
		return float64(c)
	case int32:
		return float64(c)
	case int64:
		return float64(c)
	case uint:
		return float64(c)
	case uint8:
		return float64(c)
	case uint16:
		return float64(c)
	case uint32:
		return float64(c)
	case uint64:
		return float64(c)
	case string:
		return c
	case bool:
		return c
	case []any:
		out := make([]any, len(c))
		for i, e := range c {
			out[i] = canon(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(c))
		for k, e := range c {
			out[k] = canon(e)
		}
		return out
	case *jsonic.OrderedMap:
		if c == nil {
			return nil
		}
		return canon(c.Vals)
	case jsonic.OrderedMap:
		return canon(c.Vals)
	}

	// Fall back through reflection for any other slice/map shape the engine
	// may hand back, so an unexpected type is never silently "equal".
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = canon(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		for _, k := range rv.MapKeys() {
			out[fmt.Sprint(k.Interface())] = canon(rv.MapIndex(k).Interface())
		}
		return out
	case reflect.Ptr:
		if rv.IsNil() {
			return nil
		}
		return canon(rv.Elem().Interface())
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

func readSuiteYaml(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "in.yaml"))
	if err != nil {
		t.Fatalf("cannot read in.yaml in %s: %v", dir, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// suiteParse runs Parse, converting a panic into an error so a single bad
// case cannot abort the whole run. A panic is a rejection, not a pass: the
// caller sees err != nil, exactly as the TS runner's try/catch does.
func suiteParse(src string) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return Parse(src)
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// TestYamlTestSuite is the Go half of the official-suite conformance run.
func TestYamlTestSuite(t *testing.T) {
	requireSuite(t)

	allCases := gatherSuiteCases(t)
	if len(allCases) < 300 {
		t.Fatalf("yaml-test-suite: only %d cases discovered — the corpus is "+
			"truncated or the layout changed; refusing to report a bogus number.",
			len(allCases))
	}

	var validCases, errorCases, novalueCases []suiteCase
	for _, c := range allCases {
		switch {
		case c.hasError:
			errorCases = append(errorCases, c)
		case c.hasJSON:
			validCases = append(validCases, c)
		default:
			novalueCases = append(novalueCases, c)
		}
	}

	// Valid documents must parse AND produce the correct value.
	t.Run("valid-parse", func(t *testing.T) {
		for _, tc := range validCases {
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				inYaml := readSuiteYaml(t, tc.dir)
				raw, err := os.ReadFile(filepath.Join(tc.dir, "in.json"))
				if err != nil {
					t.Fatalf("cannot read in.json: %v", err)
				}
				docs, err := parseJSONStream(string(raw))
				if err != nil {
					t.Fatalf("%s: %v", tc.id, err)
				}

				// The plugin yields the bare value for a single-document
				// stream and a slice of values for a multi-document one.
				var expected any = docs
				if len(docs) == 1 {
					expected = docs[0]
				}

				actual, perr := suiteParse(inYaml)
				if perr != nil {
					t.Fatalf("%s (%s): valid document REJECTED: %v\n  input:    %q\n  expected: %s",
						tc.id, tc.name, perr, inYaml, show(expected))
				}

				got, want := canon(actual), canon(expected)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s (%s): wrong value\n  input:    %q\n  expected: %s\n  actual:   %s",
						tc.id, tc.name, inYaml, show(want), show(got))
				}
			})
		}
	})

	// Invalid documents must be REJECTED.
	t.Run("expected-errors", func(t *testing.T) {
		for _, tc := range errorCases {
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				inYaml := readSuiteYaml(t, tc.dir)
				actual, err := suiteParse(inYaml)
				if err == nil {
					t.Errorf("%s (%s): invalid document ACCEPTED (must be rejected)\n  input:  %q\n  parsed: %s",
						tc.id, tc.name, inYaml, show(canon(actual)))
				}
			})
		}
	})

	// No expected value is published for these, so the value cannot be
	// checked. They are still valid YAML, so the parse itself must succeed —
	// a real assertion, just a partial one. A documented coverage gap.
	t.Run("valid-parse-novalue", func(t *testing.T) {
		for _, tc := range novalueCases {
			t.Run(tc.id+": "+tc.name, func(t *testing.T) {
				inYaml := readSuiteYaml(t, tc.dir)
				if _, err := suiteParse(inYaml); err != nil {
					t.Errorf("%s (%s): valid document REJECTED: %v\n  input: %q",
						tc.id, tc.name, err, inYaml)
				}
			})
		}
	})

	// Not a pass/fail conformance number — a guard that the corpus scored
	// against is the pinned one. If these move, the fetch pin moved with them.
	t.Run("suite-census", func(t *testing.T) {
		for _, c := range []struct {
			what string
			got  int
			want int
		}{
			{"total cases", len(allCases), 402},
			{"value-checked cases", len(validCases), 279},
			{"must-fail cases", len(errorCases), 94},
			{"parse-only cases", len(novalueCases), 29},
		} {
			if c.got != c.want {
				t.Errorf("%s: got %d, want %d", c.what, c.got, c.want)
			}
		}
	})
}
