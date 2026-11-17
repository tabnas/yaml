// Copyright (c) 2026 Richard Rodger and other contributors, MIT License

package tabnasyaml

// Three properties of the reader, each held over every text this
// repository has: every row of the shared fixtures, every case of the YAML
// Test Suite and every input of the YAML Test Schema. Mirrors
// ts/test/reading.test.ts.
//
//  1. A line break is `\n`, `\r\n`, or `\r` by itself (YAML 1.2.2, 5.4),
//     and which one a text uses changes nothing it reads as. Each text is
//     read as written, with every `\n` made `\r\n`, and with every `\n`
//     made `\r`, and the three must agree: in the value, or in the error's
//     code.
//  2. A token stands where its offset says: its row is one more than the
//     line breaks before it, and its column is how many characters it
//     stands past its line's start, from 1. It is held for every token a
//     lex subscriber sees, and for the diagnostic of every text that is
//     refused. A row and a column kept up token by token drift, and once
//     did: a column too far after every `: `, no row for a line break
//     inside a quoted scalar, column 0 after a block scalar.
//  3. The core schema option changes how a plain scalar resolves and
//     nothing else: under it a text reads as it does without, except that a
//     scalar the plugin's own reading gives a boolean or a number may be
//     the string the core schema gives it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	jsonic "github.com/tabnas/jsonic/go"
	tabnas "github.com/tabnas/parser/go"
	support "github.com/tabnas/support/go"
)

// readingText is one text to read, where it came from, and the plugin
// options it is read with.
type readingText struct {
	at   string
	text string
	opts map[string]any
}

// readingTexts is every text: the fixture rows with their options, then
// the suite's cases, sub-cases (`AB12/00`) included, then the schema's
// inputs.
func readingTexts(t *testing.T) []readingText {
	t.Helper()
	var out []readingText
	dir, err := support.FindSpecDir("")
	if err != nil {
		t.Fatal(err)
	}
	files, err := support.LoadSpecDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(files, func(i, k int) bool { return files[i].Name < files[k].Name })
	for _, f := range files {
		for _, row := range f.Rows {
			opts := map[string]any{}
			if raw := strings.TrimSpace(row.Named("opts")); raw != "" {
				if err := json.Unmarshal([]byte(raw), &opts); err != nil {
					t.Fatalf("%s: opts %q: %v", row.Where(), raw, err)
				}
			}
			out = append(out, readingText{
				at:   fmt.Sprintf("%s:%d", f.Name, row.Line),
				text: row.UnescNamed("input"),
				opts: opts,
			})
		}
	}

	suite := filepath.Join("..", "test", "yaml-test-suite")
	entries, err := os.ReadDir(suite)
	if err != nil {
		t.Fatal(err)
	}
	numbered := regexp.MustCompile(`^\d+$`)
	for _, e := range entries {
		caseDir := filepath.Join(suite, e.Name())
		if raw, err := os.ReadFile(filepath.Join(caseDir, "in.yaml")); err == nil {
			out = append(out, readingText{at: "suite " + e.Name(), text: string(raw), opts: map[string]any{}})
			continue
		}
		subs, err := os.ReadDir(caseDir)
		if err != nil {
			continue
		}
		for _, s := range subs {
			if !numbered.MatchString(s.Name()) {
				continue
			}
			if raw, err := os.ReadFile(filepath.Join(caseDir, s.Name(), "in.yaml")); err == nil {
				out = append(out, readingText{at: "suite " + e.Name() + "/" + s.Name(), text: string(raw), opts: map[string]any{}})
			}
		}
	}

	raw, err := os.ReadFile(schemaCorpus)
	if err != nil {
		t.Fatal(err)
	}
	var corpus map[string][3]string
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	inputs := make([]string, 0, len(corpus))
	for k := range corpus {
		inputs = append(inputs, k)
	}
	sort.Strings(inputs)
	for _, input := range inputs {
		q, _ := json.Marshal(input)
		out = append(out, readingText{at: "schema " + string(q), text: input, opts: map[string]any{}})
	}
	return out
}

// readingParsers keeps one parser for each set of options: building one
// reads the grammar's text, and a parser keeps nothing from one parse to
// the next.
type readingParsers map[string]*jsonic.Jsonic

func (ps readingParsers) with(t *testing.T, opts map[string]any) *jsonic.Jsonic {
	t.Helper()
	key, _ := json.Marshal(opts)
	if j, ok := ps[string(key)]; ok {
		return j
	}
	j := jsonic.Make()
	if err := j.Use(Yaml, opts); err != nil {
		t.Fatalf("use %s: %v", key, err)
	}
	ps[string(key)] = j
	return j
}

// readingCanon is a value as text that tells apart everything a parse can
// give.
func readingCanon(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case float64:
		if x == 0 && math.Signbit(x) {
			return "-0"
		}
		return jsNumberString(x)
	case bool:
		return strconv.FormatBool(x)
	case string:
		return strconv.Quote(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = readingCanon(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *tabnas.OrderedMap:
		parts := make([]string, len(x.Keys))
		for i, k := range x.Keys {
			parts[i] = strconv.Quote(k) + ":" + readingCanon(x.Vals[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = strconv.Quote(k) + ":" + readingCanon(x[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	if tabnas.IsUndefined(v) {
		return "undefined"
	}
	if m, ok := v.(*MetaResult); ok {
		b, _ := json.Marshal(m.Meta)
		return "meta " + string(b) + " " + readingCanon(m.Content)
	}
	return fmt.Sprintf("%T(%#v)", v, v)
}

// errCode is the code a parse was refused with, or "" for none.
func errCode(err error) string {
	if err == nil {
		return ""
	}
	var te *tabnas.TabnasError
	if errors.As(err, &te) {
		return te.Code
	}
	return "not a TabnasError: " + err.Error()
}

// readingSeen is what a parse shows: its value, or its error's code.
func readingSeen(j *jsonic.Jsonic, text string) string {
	v, err := j.Parse(text)
	if err != nil {
		return "error " + errCode(err)
	}
	return "value " + readingCanon(v)
}

func readingShort(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func readingStart(s string) string {
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

// placeOf is the row and column of byte offset at in text: a line break is
// `\n`, a `\r` no `\n` follows, or `\r\n`, a column counts characters, and
// a byte order mark at the start takes no column. Between the halves of a
// `\r\n` the line has ended.
func placeOf(text string, at int) (row, col int) {
	row = 1
	ls := 0
	if strings.HasPrefix(text, bomText) {
		ls = len(bomText)
	}
	for i := 0; i < at && i < len(text); i++ {
		c := text[i]
		if c == '\n' || c == '\r' {
			if !(c == '\n' && i > 0 && text[i-1] == '\r') {
				row++
			}
			ls = i + 1
		}
	}
	if at < ls {
		return row, 1
	}
	if at > len(text) {
		at = len(text)
	}
	return row, utf8.RuneCountInString(text[ls:at]) + 1
}

func TestReading(t *testing.T) {
	all := readingTexts(t)
	parsers := readingParsers{}

	t.Run("census", func(t *testing.T) {
		// The texts are all there: a property held over a truncated set is
		// held over less than it says.
		if len(all) <= 1400 {
			t.Errorf("%d texts", len(all))
		}
	})

	for _, lb := range []struct{ name, brk string }{{"crlf", "\r\n"}, {"cr", "\r"}} {
		t.Run("line-breaks-"+lb.name, func(t *testing.T) {
			compared, own := 0, 0
			var failures []string
			for _, one := range all {
				// A text with a carriage return of its own is about one
				// line break or another already.
				if strings.Contains(one.text, "\r") {
					own++
					continue
				}
				j := parsers.with(t, one.opts)
				written := readingSeen(j, one.text)
				rewritten := readingSeen(j, strings.ReplaceAll(one.text, "\n", lb.brk))
				compared++
				if written != rewritten {
					failures = append(failures, fmt.Sprintf("%s: %q\n  as written: %s\n  with %s: %s",
						one.at, readingStart(one.text), readingShort(written), lb.name, readingShort(rewritten)))
				}
			}
			t.Logf("%d compared, %d left out", compared, own)
			if compared <= 1300 {
				t.Errorf("%d compared, %d left out", compared, own)
			}
			for i, f := range failures {
				if i == 10 {
					break
				}
				t.Error(f)
			}
			if len(failures) > 0 {
				t.Errorf("%d texts differ", len(failures))
			}
		})
	}

	t.Run("a-token-stands-where-its-offset-says", func(t *testing.T) {
		// Every token the lexer gives, seen by a lex subscriber: its row
		// and column are its offset's.
		tokens := 0
		var failures []string
		var at, text string
		watched := map[string]*jsonic.Jsonic{}
		for _, one := range all {
			key, _ := json.Marshal(one.opts)
			j, ok := watched[string(key)]
			if !ok {
				j = jsonic.Make()
				if err := j.Use(Yaml, one.opts); err != nil {
					t.Fatalf("use %s: %v", key, err)
				}
				j.Sub(func(tkn *tabnas.Token, _ *tabnas.Rule, _ *tabnas.Context) {
					tokens++
					row, col := placeOf(text, tkn.SI)
					if (row != tkn.RI || col != tkn.CI) && len(failures) < 10 {
						failures = append(failures, fmt.Sprintf("%s: %s %q at %d has %d:%d, its offset gives %d:%d in %q",
							at, tkn.Name, readingStart(tkn.Src), tkn.SI, tkn.RI, tkn.CI, row, col, readingStart(text)))
					}
				}, nil)
				watched[string(key)] = j
			}
			at, text = one.at, one.text
			// Its tokens are placed all the same when a text is refused.
			_, _ = j.Parse(text)
		}
		t.Logf("%d tokens", tokens)
		if tokens <= 15000 {
			t.Errorf("%d tokens", tokens)
		}
		for _, f := range failures {
			t.Error(f)
		}
	})

	t.Run("an-error-stands-where-its-offset-says", func(t *testing.T) {
		// The diagnostic a refused text gives: its row and column are its
		// offset's, in every text that is refused.
		errs := 0
		var failures []string
		for _, one := range all {
			_, err := parsers.with(t, one.opts).Parse(one.text)
			var te *tabnas.TabnasError
			if err == nil || !errors.As(err, &te) {
				continue
			}
			errs++
			row, col := placeOf(one.text, te.Pos)
			if (row != te.Row || col != te.Col) && len(failures) < 10 {
				failures = append(failures, fmt.Sprintf("%s: %s at %d has %d:%d, its offset gives %d:%d in %q",
					one.at, te.Code, te.Pos, te.Row, te.Col, row, col, readingStart(one.text)))
			}
		}
		t.Logf("%d errors", errs)
		if errs <= 80 {
			t.Errorf("%d errors", errs)
		}
		for _, f := range failures {
			t.Error(f)
		}
	})

	t.Run("the-core-schema-changes-plain-scalars-alone", func(t *testing.T) {
		compared, changed := 0, 0
		var failures []string
		for _, one := range all {
			if one.opts["schema"] == "core" {
				continue
			}
			under := map[string]any{"schema": "core"}
			for k, v := range one.opts {
				if k != "schema" {
					under[k] = v
				}
			}
			plain, plainErr := parsers.with(t, one.opts).Parse(one.text)
			core, coreErr := parsers.with(t, under).Parse(one.text)
			compared++
			if plainErr != nil || coreErr != nil {
				a, b := "a value", "a value"
				if plainErr != nil {
					a = "error " + errCode(plainErr)
				}
				if coreErr != nil {
					b = "error " + errCode(coreErr)
				}
				if a != b {
					failures = append(failures, fmt.Sprintf("%s: %q gives %s, and under the core schema %s",
						one.at, readingStart(one.text), a, b))
				}
				continue
			}
			if readingCanon(plain) != readingCanon(core) {
				changed++
			}
			if where, parted := coreParts(plain, core, ""); parted {
				failures = append(failures, fmt.Sprintf("%s: %q parts at %s", one.at, readingStart(one.text), where))
			}
		}
		if compared <= 1300 {
			t.Errorf("%d compared", compared)
		}
		// The option does something: the fixtures hold the YAML 1.1 words
		// and jsonic's numbers, which it reads as strings.
		t.Logf("%d compared, %d changed under the core schema", compared, changed)
		if changed <= 20 {
			t.Errorf("%d changed", changed)
		}
		for i, f := range failures {
			if i == 10 {
				break
			}
			t.Error(f)
		}
		if len(failures) > 0 {
			t.Errorf("%d texts part elsewhere", len(failures))
		}
	})
}

// coreParts says where under parts from plain, if it does: under is plain
// except where a boolean or a number of plain is a string in under.
func coreParts(plain, under any, path string) (string, bool) {
	if m, ok := plain.(*MetaResult); ok {
		u, ok := under.(*MetaResult)
		if !ok || readingCanon(&MetaResult{Meta: m.Meta}) != readingCanon(&MetaResult{Meta: u.Meta}) {
			return path + " (its meta)", true
		}
		return coreParts(m.Content, u.Content, path)
	}
	pl, plOK := plain.([]any)
	ul, ulOK := under.([]any)
	if plOK || ulOK {
		if !plOK || !ulOK || len(pl) != len(ul) {
			return path + " (a sequence)", true
		}
		for i := range pl {
			if where, parted := coreParts(pl[i], ul[i], fmt.Sprintf("%s/%d", path, i)); parted {
				return where, true
			}
		}
		return "", false
	}
	if pm, ok := plain.(*tabnas.OrderedMap); ok {
		um, ok := under.(*tabnas.OrderedMap)
		if !ok {
			return path + " (a mapping)", true
		}
		pk := append([]string(nil), pm.Keys...)
		uk := append([]string(nil), um.Keys...)
		sort.Strings(pk)
		sort.Strings(uk)
		if strings.Join(pk, "\x00") != strings.Join(uk, "\x00") {
			return path + " (its keys)", true
		}
		for _, k := range pm.Keys {
			if where, parted := coreParts(pm.Vals[k], um.Vals[k], path+"/"+k); parted {
				return where, true
			}
		}
		return "", false
	}
	if readingCanon(plain) == readingCanon(under) {
		return "", false
	}
	switch plain.(type) {
	case bool, float64:
		if _, ok := under.(string); ok {
			return "", false
		}
	}
	return fmt.Sprintf("%s (%s and %s)", path, readingCanon(plain), readingCanon(under)), true
}
