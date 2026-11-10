// Copyright (c) 2026 Richard Rodger and other contributors, MIT License

package tabnasyaml

// rule_history_test.go — every document parses alike with the engine's
// rule history bounded and without.
//
// A streaming host bounds the engine's rule history (`rule.history`):
// tabnas/transduce's ParserEventSource, in a run that prunes, keeps three
// predecessors (its StreamRuleHistory, which is what the Rust engine keeps
// by default), so that a parse of a long document does not keep every rule
// it made. Under the bound a rule's links are snapshots, and the snapshot
// of a pusher's child is the child as it ended: the links to the rules that
// replaced it are cut. The plugin reads a child's value from the child
// alone (childNode), as TypeScript reads `rule.child.node`, so it reads
// the same value either way; these tests hold it to that. Every fixture
// row, every case of the YAML Test Suite and every input of the YAML Test
// Schema parses to the same value, or fails the same way, with the history
// bounded as without, and, read by a subscriber to the rule passes as a
// streaming host reads them, makes the same passes.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	tabnas "github.com/tabnas/parser/go"
)

// boundedHistory is the rule history a streaming host bounds the engine's
// at: tabnas/transduce's StreamRuleHistory.
const boundedHistory = 3

// historyParser is MakeJsonic with the engine's rule history bounded at
// boundedHistory, or left as the engine leaves it, unbounded.
func historyParser(t *testing.T, bounded bool) *tabnas.Tabnas {
	t.Helper()
	j := MakeJsonic()
	if bounded {
		history := boundedHistory
		j.SetOptions(tabnas.Options{Rule: &tabnas.RuleOptions{History: &history}})
	}
	want := 0
	if bounded {
		want = boundedHistory
	}
	if got := j.Config().RuleHistory; got != want {
		t.Fatalf("the rule history is %d, want %d", got, want)
	}
	return j
}

// historyDoc is a document of the corpora: where it is, and its text.
type historyDoc struct{ at, text string }

// historyDocuments is every fixture row, every case of the YAML Test Suite
// and every input of the YAML Test Schema.
func historyDocuments(t *testing.T) []historyDoc {
	t.Helper()
	var docs []historyDoc
	fixtureRows(t, func(at, input string) {
		docs = append(docs, historyDoc{at, input})
	})
	rows := len(docs)
	cases := gatherSuiteCases(t)
	if len(cases) != 402 {
		t.Fatalf("the suite has %d cases, want 402", len(cases))
	}
	for _, c := range cases {
		docs = append(docs, historyDoc{"suite " + c.id, readSuiteYaml(t, c.dir)})
	}
	raw, err := os.ReadFile(schemaCorpus)
	if err != nil {
		t.Fatal(err)
	}
	var corpus map[string][3]string
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus) != 245 {
		t.Fatalf("the schema corpus has %d inputs, want 245", len(corpus))
	}
	inputs := make([]string, 0, len(corpus))
	for input := range corpus {
		inputs = append(inputs, input)
	}
	sort.Strings(inputs)
	for _, input := range inputs {
		docs = append(docs, historyDoc{"schema " + strconv.Quote(input), input})
	}
	if rows < 600 {
		t.Fatalf("only %d fixture rows", rows)
	}
	return docs
}

// scalarNode reports whether a rule's node is a value a pass carries: not a
// container, and not undefined.
func scalarNode(v any) bool {
	if tabnas.IsUndefined(v) {
		return false
	}
	switch v.(type) {
	case []any, *tabnas.OrderedMap, tabnas.OrderedMap, map[string]any, *MetaResult:
		return false
	}
	return true
}

// passText is a pass as a streaming host relays it (tabnas/transduce's
// RulePass): the rule, its depth and instance number, the alternate the
// pass took, the tokens it consumed with their values, its counters, and,
// at a close, its value when that is a scalar.
func passText(rule *tabnas.Rule, ctx *tabnas.Context, done tabnas.RuleDone) string {
	open := done.State == tabnas.OPEN
	recorded := rule.C[:rule.CN]
	state := "close"
	if open {
		recorded = rule.O[:rule.ON]
		state = "open"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s d%d i%d", state, rule.Name, rule.D, rule.I)
	keep := len(recorded)
	if done.Alt != nil {
		g := append([]string(nil), done.Alt.G...)
		sort.Strings(g)
		fmt.Fprintf(&b, " alt{p:%s r:%s b:%d g:%s}", done.Alt.P, done.Alt.R, done.Alt.B, strings.Join(g, ","))
		keep -= done.Alt.B
	}
	for _, tok := range recorded[:max(keep, 0)] {
		fmt.Fprintf(&b, " %s%q=%s", tok.Name, tok.Src, canonical(tok.ResolveVal(rule, ctx)))
	}
	names := make([]string, 0, len(rule.N))
	for name := range rule.N {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, " %s=%d", name, rule.N[name])
	}
	if !open && scalarNode(rule.Node) {
		b.WriteString(" value " + canonical(rule.Node))
	}
	return b.String()
}

// passParser is a parser that records the passes of each parse, as a
// streaming host subscribes to them.
type passParser struct {
	j      *tabnas.Tabnas
	passes []string
}

func newPassParser(t *testing.T, bounded bool) *passParser {
	p := &passParser{j: historyParser(t, bounded)}
	p.j.SubRuleDone(func(rule *tabnas.Rule, ctx *tabnas.Context, done tabnas.RuleDone) {
		if !done.Probe {
			p.passes = append(p.passes, passText(rule, ctx, done))
		}
	})
	return p
}

// historyParse is what a parse shows (shown), and a panic as one.
func historyParse(j *tabnas.Tabnas, text string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprintf("panic %v", r)
		}
	}()
	return shown(j.Parse(text))
}

// parse is what a parse of text shows, and its passes.
func (p *passParser) parse(text string) (string, []string) {
	p.passes = nil
	return historyParse(p.j, text), p.passes
}

// historyDiffers says where text parses otherwise with the history bounded:
// "" alike, and otherwise how, by the value or the ending, with no
// subscriber and with one, or by the first pass that differs.
func historyDiffers(whole, bound *tabnas.Tabnas, wholePasses, boundPasses *passParser, text string) string {
	if a, b := historyParse(whole, text), historyParse(bound, text); a != b {
		return fmt.Sprintf("%s unbounded, %s bounded", clip(a), clip(b))
	}
	a, passesA := wholePasses.parse(text)
	b, passesB := boundPasses.parse(text)
	if a != b {
		return fmt.Sprintf("read by a subscriber, %s unbounded, %s bounded", clip(a), clip(b))
	}
	for i := 0; i < len(passesA) || i < len(passesB); i++ {
		at := func(passes []string) string {
			if i < len(passes) {
				return passes[i]
			}
			return "nothing"
		}
		if at(passesA) != at(passesB) {
			return fmt.Sprintf("pass %d of %d: %s unbounded, %s bounded", i, len(passesA), clip(at(passesA)), clip(at(passesB)))
		}
	}
	return ""
}

// TestEveryDocumentParsesAlikeWithTheRuleHistoryBounded: every document of
// the three corpora parses to the same value, or fails the same way, with
// the rule history bounded as without, and makes the same passes.
func TestEveryDocumentParsesAlikeWithTheRuleHistoryBounded(t *testing.T) {
	docs := historyDocuments(t)
	whole, bound := historyParser(t, false), historyParser(t, true)
	wholePasses, boundPasses := newPassParser(t, false), newPassParser(t, true)
	var differ []string
	for i, doc := range docs {
		if why := historyDiffers(whole, bound, wholePasses, boundPasses, doc.text); why != "" {
			differ = append(differ, doc.at+": "+why)
		}
		if (i+1)%250 == 0 || i+1 == len(docs) {
			t.Logf("history: %d of %d (%d%%) documents, %d differing", i+1, len(docs), (i+1)*100/len(docs), len(differ))
		}
	}
	if len(differ) > 0 {
		t.Errorf("%d of %d documents parse otherwise with the rule history bounded at %d:\n  %s",
			len(differ), len(docs), boundedHistory, strings.Join(differ, "\n  "))
	}
}
