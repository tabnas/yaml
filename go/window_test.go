// Copyright (c) 2026 Richard Rodger and other contributors, MIT License

package tabnasyaml

// window_test.go — a parse from a reader (Tabnas.ParseReader, admin ADR-30
// and ADR-33).
//
// MakeJsonic builds the parser whole and declares lex.window on it, so that
// a reader is read a window at a time. The plugin's matcher and its text
// check read the source through the window (source.go), read on where a
// scan reaches its end, and keep the lines before the cursor their backward
// scans read. These tests hold it to that: MakeJsonic declares the window
// and parses every fixture row as the parser without it does; every row,
// every case of the YAML Test Suite and every input of the YAML Test Schema
// parses the same from a reader, in pieces of 1, 3, 64 and 4096 bytes, as
// from its text; and tokens longer than the window, lines whose start the
// window drops before the scans read back to it, a flow colon far past the
// quote it follows, and a long document parse the same too.
//
// Go's window sizes are fixed at 64 KiB (the engine's small-window hook is
// unexported), and before each token the window reaches 64 KiB past the
// cursor, so a token meets the window's edge only where it is longer than
// that, and the window drops a line's start only once a block of the line
// lies behind the cursor: the long tokens and the long document carry such
// tokens and lines of every kind the plugin reads.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	jsonic "github.com/tabnas/jsonic/go"
	tabnas "github.com/tabnas/parser/go"
	support "github.com/tabnas/support/go"
)

// readerMargin is how far past the cursor the window reaches before each
// token is cut: a token longer than this meets the window's edge.
const readerMargin = 64 << 10

// pieces is a reader that hands its bytes over at most size at a time.
type pieces struct {
	text []byte
	at   int
	size int
}

func (p *pieces) Read(buf []byte) (int, error) {
	if p.at == len(p.text) {
		return 0, io.EOF
	}
	n := min(p.size, len(buf), len(p.text)-p.at)
	copy(buf, p.text[p.at:p.at+n])
	p.at += n
	return n, nil
}

// canonical is a value as text that tells apart everything a parse can
// give: the order of a mapping's keys, a number's sign, NaN (which
// reflect.DeepEqual calls unequal to itself) and the infinities.
func canonical(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case *tabnas.OrderedMap:
		parts := make([]string, 0, len(x.Keys))
		for _, k := range x.Keys {
			parts = append(parts, strconv.Quote(k)+":"+canonical(x.Vals[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, strconv.Quote(k)+":"+canonical(x[k]))
		}
		return "map{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, canonical(e))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *MetaResult:
		meta, _ := json.Marshal(x.Meta)
		return "meta(" + string(meta) + " " + canonical(x.Content) + ")"
	case float64:
		if math.IsNaN(x) {
			return "NaN"
		}
		if x == 0 && math.Signbit(x) {
			return "-0"
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return strconv.Quote(x)
	}
	return fmt.Sprintf("%T(%#v)", v, v)
}

// shown is what a parse shows: its value, or its error's code, position,
// row, column and token text. An error's excerpt is left out: a parse from
// a reader shows a long line only as far back as the window reaches
// (tabnas/parser DIVERGENCE.md), and that is the one difference allowed.
func shown(v any, err error) string {
	if err == nil {
		return "value " + canonical(v)
	}
	var te *tabnas.TabnasError
	if errors.As(err, &te) {
		return fmt.Sprintf("error %s at %d %d:%d %q", te.Code, te.Pos, te.Row, te.Col, te.Src)
	}
	return "not a TabnasError: " + err.Error()
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// sameFromReader parses text whole and from a reader in pieces of each
// size, and reports where the two differ. It returns the number of reader
// parses compared.
func sameFromReader(t *testing.T, j *tabnas.Tabnas, at, text string, sizes []int) int {
	t.Helper()
	whole := shown(j.Parse(text))
	for _, size := range sizes {
		read := shown(j.ParseReader(&pieces{text: []byte(text), size: size}))
		if read != whole {
			t.Errorf("%s: %q in pieces of %d:\n  whole:  %s\n  reader: %s",
				at, clip(text), size, clip(whole), clip(read))
		}
	}
	return len(sizes)
}

// readerSizes are the pieces a document is handed over in: down to a byte
// for a short one, and from 64 bytes for one over 8 KiB, whose every
// one-byte read the Go window answers with a copy of itself
// (tabnas/parser#321).
func readerSizes(text string) []int {
	if len(text) > 8<<10 {
		return []int{64, 4096, readerMargin + 3}
	}
	return []int{1, 3, 64, 4096}
}

// fixtureRows calls each with every row of the shared fixtures: where it
// is, and its input.
func fixtureRows(t *testing.T, each func(at, input string)) {
	t.Helper()
	dir, err := support.FindSpecDir("")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures in %s: %v", dir, err)
	}
	for _, path := range paths {
		spec, err := support.LoadSpec(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range spec.Rows {
			each(fmt.Sprintf("%s:%d", filepath.Base(path), row.Line), row.UnescNamed("input"))
		}
	}
}

// withoutWindow is the parser MakeJsonic built before it declared the
// window, and still builds but for that.
func withoutWindow(opts ...YamlOptions) *tabnas.Tabnas {
	j := MakeJsonic(opts...)
	no := false
	j.SetOptions(tabnas.Options{Lex: &tabnas.LexOptions{Window: &no}})
	return j
}

// TestMakeJsonicDeclaresTheWindow: MakeJsonic declares lex.window, and the
// plugin installed on jsonic's base does not.
func TestMakeJsonicDeclaresTheWindow(t *testing.T) {
	if !MakeJsonic().Config().Window {
		t.Error("MakeJsonic does not declare lex.window")
	}
	if withoutWindow().Config().Window {
		t.Error("the window is still declared with it turned off")
	}
	// jsonic's base declares the window itself (tabnas/jsonic#121), so the
	// plugin goes on that base with the window turned off.
	no := false
	plain := jsonic.Make(tabnas.Options{Lex: &tabnas.LexOptions{Window: &no}})
	if err := plain.Use(Yaml, nil); err != nil {
		t.Fatal(err)
	}
	if plain.Config().Window {
		t.Error("installing the plugin declares lex.window")
	}
}

// TestMakeJsonicParsesAsBefore: every fixture row parses through
// MakeJsonic as through the parser without the window, and so does a
// stream read with the meta option.
func TestMakeJsonicParsesAsBefore(t *testing.T) {
	made, before := MakeJsonic(), withoutWindow()
	n := 0
	fixtureRows(t, func(at, input string) {
		a, b := shown(made.Parse(input)), shown(before.Parse(input))
		if a != b {
			t.Errorf("%s:\n  MakeJsonic: %s\n  before:     %s", at, a, b)
		}
		n++
	})
	if n < 600 {
		t.Errorf("only %d rows", n)
	}
	meta := "a: 1\n---\nb: 2\n"
	a := shown(MakeJsonic(YamlOptions{Meta: true}).Parse(meta))
	b := shown(withoutWindow(YamlOptions{Meta: true}).Parse(meta))
	if a != b || !strings.HasPrefix(a, "value meta(") {
		t.Errorf("meta:\n  MakeJsonic: %s\n  before:     %s", a, b)
	}
}

// TestFixturesFromAReader: every fixture row parses the same from a reader
// as from its text, under MakeJsonic.
func TestFixturesFromAReader(t *testing.T) {
	compared := 0
	fixtureRows(t, func(at, input string) {
		compared += sameFromReader(t, MakeJsonic(), at, input, readerSizes(input))
	})
	t.Logf("reader fixtures: %d reader parses (100%%)", compared)
	if compared < 2500 {
		t.Errorf("only %d reader parses", compared)
	}
}

// TestYamlTestSuiteFromAReader: every case of the YAML Test Suite parses
// the same from a reader as from its text.
func TestYamlTestSuiteFromAReader(t *testing.T) {
	cases := gatherSuiteCases(t)
	if len(cases) != 402 {
		t.Fatalf("the suite has %d cases, want 402", len(cases))
	}
	j := MakeJsonic()
	compared := 0
	for n, c := range cases {
		text := readSuiteYaml(t, c.dir)
		compared += sameFromReader(t, j, "suite "+c.id, text, readerSizes(text))
		if (n+1)%100 == 0 || n+1 == len(cases) {
			t.Logf("reader suite: %d of %d cases (%d%%), %d reader parses",
				n+1, len(cases), (n+1)*100/len(cases), compared)
		}
	}
}

// TestYamlTestSchemaFromAReader: every input of the YAML Test Schema
// parses the same from a reader as from its text.
func TestYamlTestSchemaFromAReader(t *testing.T) {
	raw, err := os.ReadFile(schemaCorpus)
	if err != nil {
		t.Fatal(err)
	}
	var corpus map[string][3]string
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus) != 245 {
		t.Fatalf("the corpus has %d inputs, want 245", len(corpus))
	}
	inputs := make([]string, 0, len(corpus))
	for input := range corpus {
		inputs = append(inputs, input)
	}
	sort.Strings(inputs)
	j := MakeJsonic()
	compared := 0
	for _, input := range inputs {
		compared += sameFromReader(t, j, "schema "+strconv.Quote(input), input, readerSizes(input))
	}
	t.Logf("reader schema: %d inputs, %d reader parses (100%%)", len(inputs), compared)
}

// longTokens are documents whose tokens, or the lines the scans read back
// over, are longer than the window: each kind of token the plugin cuts
// itself, long, and some cut short by the source's end.
func longTokens() []string {
	long := readerMargin + 2048
	lines := func(n int, line string) string { return strings.Repeat(line, n) }
	return []string{
		// Plain scalars: one line, folded over lines, in a flow sequence.
		"a: " + strings.Repeat("p", long) + "\nb: 1\n",
		"a: x\n" + lines(long/8, "  more t\n") + "b: 2\n",
		"- " + strings.Repeat("w ", long/2) + "end\n- next\n",
		"[" + strings.Repeat("item, ", long/6) + "last]\n",
		// Quoted scalars, with escapes and folded lines.
		"a: \"" + strings.Repeat("d", long) + "\\u00e9\\x41\\U0001F600\\n\"\nb: 1\n",
		"a: \"" + strings.Repeat("é", long/2) + "\"\n",
		"a: '" + strings.Repeat("s''", long/3) + "'\nb: 1\n",
		"a: \"line\n" + lines(long/6, "  word\n") + "  end\"\n",
		// Block scalars, literal and folded, kept and stripped, explicit.
		"a: |\n" + lines(long/8, "  literal\n") + "b: 1\n",
		"a: >-\n" + lines(long/8, "  folded\n") + "\n  para\nb: 1\n",
		"a: |+\n" + lines(long/8, "  kept\n") + "\n\n",
		"- a: |2\n" + lines(long/8, "     deep\n") + "- b\n",
		// Comments, blank lines and anchors between lines.
		"# " + strings.Repeat("c", long) + "\na: 1\n",
		"a: 1\n" + lines(long/4, "# c\n") + "b: 2\n",
		"a: 1\n" + strings.Repeat("\n", long) + "b: 2\n",
		"a: 1\n" + lines(long/4, " \t \n") + "b: 2\n",
		// Anchors, aliases and tags with long names and values.
		"&" + strings.Repeat("n", long) + " a: 1\nb: *" + strings.Repeat("n", long) + "\n",
		"a: &x " + strings.Repeat("v", long) + "\nb: *x\n",
		"a: !!str " + strings.Repeat("s", long) + "\nb: !!int \"12\"\n",
		"a: !" + strings.Repeat("t", long) + " value\n",
		// Explicit keys, directives, documents.
		"? " + strings.Repeat("k", long) + "\n: v\n",
		"? |\n" + lines(long/8, "  keyline\n") + ": v\n",
		"%YAML 1.2\n%TAG !e! tag:" + strings.Repeat("e", long) + ":\n---\na: 1\n...\n---\nb: 2\n",
		// Lines whose start the scans read back to from far along them.
		"k" + strings.Repeat("e", long) + "y: |\n  text\n",
		"- - " + strings.Repeat("- ", long/2) + "a: |1\n   x\n",
		"[" + strings.Repeat("x, ", long/3) + "y]: z\n",
		"{" + strings.Repeat("\"k\": v, ", long/8) + "\"q\":r}\n",
		// A flow colon after a quote far behind it.
		"{\"a\"" + strings.Repeat("\n", long) + ":b}\n",
		"{\"a\" # c\n" + lines(long/8, "  # more\n") + ":b}\n",
		// Numbers longer than the window, which the engine's number
		// matcher reads and the text check would read on.
		"a: 1" + strings.Repeat("_1", long/2) + "\nb: 2\n",
		"a: ." + strings.Repeat("5", long) + "x\n",
		// Cut short by the source's end.
		"a: \"" + strings.Repeat("u", long),
		"a: '" + lines(long/2, "u\n"),
		"? " + strings.Repeat("k", long),
		"a: *" + strings.Repeat("m", long),
	}
}

// TestLongTokensFromAReader: tokens longer than the window, and lines the
// scans read back over from past a block of them, parse the same from a
// reader as from their text.
func TestLongTokensFromAReader(t *testing.T) {
	j := MakeJsonic()
	docs := longTokens()
	for n, doc := range docs {
		sameFromReader(t, j, fmt.Sprintf("long token %d", n), doc, []int{4096, readerMargin + 3})
		t.Logf("reader long tokens: %d of %d (%d%%)", n+1, len(docs), (n+1)*100/len(docs))
	}
}

// longDocument is a document of records, some 650 KB, every kind of token
// the plugin reads among them, so that the window's edge and the text it
// drops behind the cursor fall across each, a block (64 KiB) at a time.
func longDocument(tail string) string {
	var b strings.Builder
	b.WriteString("# records\ntitle: records\nbase: &base\n  kind: record\n  tags: [a, b]\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "\nrecord%d:\n  <<: *base\n  id: %d\n  name: \"ñ%d\\u00e9\" # note %d\n"+
			"  path: 'C:\\dir\\%d'\n  when: 2001-12-14t21:59:43.%d-05:00\n"+
			"  list:\n    - one\n    - &i%d two %d\n    - { k: %d, v: [1, 2] }\n"+
			"  note: |\n    line %d\n    more\n  folded: >-\n    a\n    b %d\n"+
			"  ? explicit %d\n  : value\n  again: *i%d\n  plain: word word\n    word\n",
			i, i, i, i, i, i, i, i, i, i, i, i, i)
	}
	b.WriteString(tail)
	return b.String()
}

// TestLongDocumentFromAReader: a long document parses the same from a
// reader as from its text, in pieces of several sizes, and so does one
// with an error at its end, which the reader places where the whole text
// does.
func TestLongDocumentFromAReader(t *testing.T) {
	j := MakeJsonic()
	for n, tail := range []string{"\nend: true\n", "\nend: *missing\n"} {
		doc := longDocument(tail)
		if len(doc) < 512<<10 {
			t.Fatalf("the document is %d bytes", len(doc))
		}
		whole := shown(j.Parse(doc))
		if strings.HasPrefix(whole, "value ") != (n == 0) {
			t.Fatalf("document %d: %s", n, clip(whole))
		}
		sameFromReader(t, j, fmt.Sprintf("long document %d", n), doc, []int{64, 4096, readerMargin + 3})
		t.Logf("reader long document: %d of 2 (%d%%)", n+1, (n+1)*50)
	}
}

// counted is a pieces reader that counts the bytes it has handed over.
type counted struct {
	pieces
	read int
}

func (c *counted) Read(buf []byte) (int, error) {
	n, err := c.pieces.Read(buf)
	c.read += n
	return n, err
}

// TestReaderIsReadNoFurtherThanTheWindowNeeds: an alias to no anchor near
// the start of a document of some 4 MB fails, from a reader, there,
// having read a window or so past it and not the document, which a scan
// that read to the source's end would have read.
func TestReaderIsReadNoFurtherThanTheWindowNeeds(t *testing.T) {
	doc := "a: 1\nb: *missing\n" + strings.Repeat("c: [1, 'two', \"3\"] # c\n", 160000)
	if len(doc) < 3<<20 {
		t.Fatalf("the document is %d bytes", len(doc))
	}
	j := MakeJsonic()
	whole := shown(j.Parse(doc))
	if !strings.Contains(whole, "yaml_undefined_alias") {
		t.Fatalf("whole: %s", clip(whole))
	}
	r := &counted{pieces: pieces{text: []byte(doc), size: 4096}}
	if got := shown(j.ParseReader(r)); got != whole {
		t.Fatalf("reader: %s\nwhole: %s", clip(got), clip(whole))
	}
	if r.read >= 512<<10 {
		t.Fatalf("%d bytes read", r.read)
	}
}
