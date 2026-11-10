package tabnasyaml

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	tabnas "github.com/tabnas/parser/go"
)

// yamlSource is the source as the matcher and the text check read it, by
// source position.
//
// In a parse of a whole text lex.Src is that text. In a parse from a
// reader a window at a time (lex.window, admin ADR-30), lex.Src is the
// window: the source from the oldest token the parser holds to as far as
// it has been read, and while a token is cut the cursor counts from the
// window's start. Every read here goes through at, which reads the source
// on (Lex.Fill) past the window's end, so a scan sees what the whole text
// holds, and reads hist, this plugin's own copy of the lines before the
// cursor, behind the window's start: the backward scans read the line a
// token starts on and the two before it, which the window may have
// dropped. Positions kept from one token to the next (the flow scan, the
// line breaks) are the source's.
//
// The engine names no position for the window's start while a token is
// cut, so this plugin keeps one: a lex subscriber, which runs after each
// token the lexer gives, where the cursor counts from the source's start,
// notes where it stands (after), and at the next cut the window starts
// that far before the cursor (base).
type yamlSource struct {
	lex  *tabnas.Lex
	base int // The source position of lex.Src[0] while a token is cut.
	// lex.Src as last read: the window's text, or, once a fill has grown
	// it, the start of that text, which a read past it reads again (a
	// window only grows while a token is cut).
	src   string
	after int // The cursor's source position after the last token given.
	given int // How many tokens the lexer has given this parse.

	// Whether the lexer reads a window of a source read from a reader. A
	// whole text needs neither hist nor the cuts' state kept, since the
	// engine cuts a token of it once.
	windowed bool

	// The source position sync has brought hist, breaks and the flow
	// colon's question up to.
	end int
	// The source behind the window, from a reader: positions [histLow,
	// end).
	hist    []byte
	histLow int
	// The source positions of the line breaks sync has passed, the last
	// few kept: hist keeps the text after the histBreaks-th last of them.
	breaks []int

	// The flow colon's question, kept as the text goes by: the last byte
	// before the cursor that is neither blank nor in a comment (jSig, -1
	// for none). A comment is a `#` that starts its line or follows a
	// blank, outside a quoted scalar opened on that line; jQuote is the
	// quote open, jSkip that the next byte is escaped or the second of a
	// doubled single quote, jComment that the rest of the line is a
	// comment, jPrev the byte before, -1 at a line's start.
	jSig, jQuote, jPrev int
	jSkip, jComment     bool

	// The cut the matcher last began: its source position, and how many
	// tokens the lexer had given by then. The engine cuts a token again
	// over a wider window when a read reached the window's end, from the
	// same place, before it gives another: such a cut starts from the
	// state the first began with.
	cutPos, cutGiven int
	// What a cut changed of the anchors, newest last, so that a cut made
	// again can take it back.
	undo []undoEntry
	// The state a cut began with, this type's own, and the plugin's,
	// through the hooks the plugin sets.
	snap        sourceSnap
	saveHook    func()
	restoreHook func()
}

// undoEntry is one change a cut made to the anchors: an anchor declared
// (decl set), or one recorded (anchors set), with what it replaced.
type undoEntry struct {
	decl    map[string]bool
	anchors map[string]any
	name    string
	had     bool
	old     any
}

// sourceSnap is the part of a yamlSource a cut begins with.
type sourceSnap struct {
	end, histLen, breaks int
	jSig, jQuote, jPrev  int
	jSkip, jComment      bool
}

// histBreaks is how many line breaks hist keeps the text after. A
// backward scan reads the line a token starts on and at most the two
// before it, each ended by one or two breaks (`\r\n`).
const histBreaks = 8

// reset begins a parse: the lexer it reads, nothing kept of the last, and,
// from a reader, the subscriber that follows the cursor from token to
// token.
func (y *yamlSource) reset(lex *tabnas.Lex) {
	y.lex = lex
	y.base = 0
	y.src = lex.Src
	y.after = 0
	y.given = 0
	y.windowed = lex.Windowed()
	y.end = 0
	y.hist = y.hist[:0]
	y.histLow = 0
	y.breaks = y.breaks[:0]
	y.jSig, y.jQuote, y.jPrev = -1, 0, -1
	y.jSkip, y.jComment = false, false
	y.cutPos, y.cutGiven = -1, -1
	y.undo = y.undo[:0]
	if ctx := lex.Ctx; ctx != nil && y.windowed {
		// A subscriber of this parse's alone: the context's list is
		// copied, never appended to in place, so the instance's own list
		// and any other parse's are left as they were.
		subs := make([]tabnas.LexSub, 0, len(ctx.LexSubs)+1)
		subs = append(subs, ctx.LexSubs...)
		ctx.LexSubs = append(subs, func(_ *tabnas.Token, _ *tabnas.Rule, c *tabnas.Context) {
			if c.Lex == y.lex {
				y.after = c.Lex.Cursor().SI
				y.given++
			}
		})
	}
}

// view reads lex through its window at the position the subscriber noted,
// which is where the cursor stands at the start of a cut, and answers
// that position. A whole text is its own window.
func (y *yamlSource) view(lex *tabnas.Lex) int {
	y.lex = lex
	y.src = lex.Src
	if !y.windowed {
		y.base = 0
		return lex.Cursor().SI
	}
	y.base = y.after - lex.Cursor().SI
	return y.after
}

// begin begins the matcher's call: either a cut, bringing hist and the
// flow scan up to the cursor and keeping the state the cut begins with,
// or, where the engine makes the last cut again (the same place, and no
// token given since), the same cut from that state again.
func (y *yamlSource) begin(lex *tabnas.Lex, flow *flowScanState) {
	pos := y.view(lex)
	if y.windowed {
		if pos == y.cutPos && y.given == y.cutGiven {
			y.restore()
			return
		}
		y.cutPos, y.cutGiven = pos, y.given
	}
	y.sync(pos)
	flow.advance(y, pos)
	y.trim()
	if y.windowed {
		y.save()
	}
}

func (y *yamlSource) save() {
	y.undo = y.undo[:0]
	y.snap = sourceSnap{y.end, len(y.hist), len(y.breaks), y.jSig, y.jQuote, y.jPrev, y.jSkip, y.jComment}
	if y.saveHook != nil {
		y.saveHook()
	}
}

func (y *yamlSource) restore() {
	for i := len(y.undo) - 1; i >= 0; i-- {
		u := y.undo[i]
		if u.decl != nil {
			delete(u.decl, u.name)
		} else if u.had {
			u.anchors[u.name] = u.old
		} else {
			delete(u.anchors, u.name)
		}
	}
	y.undo = y.undo[:0]
	s := y.snap
	y.end = s.end
	y.hist = y.hist[:s.histLen]
	y.breaks = y.breaks[:s.breaks]
	y.jSig, y.jQuote, y.jPrev, y.jSkip, y.jComment = s.jSig, s.jQuote, s.jPrev, s.jSkip, s.jComment
	if y.restoreHook != nil {
		y.restoreHook()
	}
}

// declare notes the anchor name as one the lexer has passed.
func (y *yamlSource) declare(declared map[string]bool, name string) {
	if !declared[name] {
		if y.windowed {
			y.undo = append(y.undo, undoEntry{decl: declared, name: name})
		}
		declared[name] = true
	}
}

// record records an anchor the lexer records itself (an inline scalar's,
// a tagged value's).
func (y *yamlSource) record(anchors map[string]any, name string, val any) {
	if y.windowed {
		old, had := anchors[name]
		y.undo = append(y.undo, undoEntry{anchors: anchors, name: name, had: had, old: old})
	}
	anchors[name] = val
}

// pos is the cursor's source position.
func (y *yamlSource) pos() int {
	return y.base + y.lex.Cursor().SI
}

// at is the byte at source position a, or -1 past the source's end and
// before its start.
func (y *yamlSource) at(a int) int {
	if x := uint(a - y.base); x < uint(len(y.src)) {
		return int(y.src[x])
	}
	return y.atSlow(a)
}

func (y *yamlSource) atSlow(a int) int {
	x := a - y.base
	if x >= 0 {
		if x >= len(y.lex.Src) {
			y.lex.Fill(x - y.lex.Cursor().SI + 1)
		}
		y.src = y.lex.Src
		if x < len(y.src) {
			return int(y.src[x])
		}
		return -1
	}
	if a < 0 {
		return -1
	}
	if i := a - y.histLow; 0 <= i && i < len(y.hist) {
		return int(y.hist[i])
	}
	// The scans never read behind the lines hist keeps, so a read that
	// would is a fault of this plugin's, and fails the parse loudly
	// (the engine reports a matcher's panic as `internal`) rather than
	// read a different text.
	panic(fmt.Sprintf("yaml: source position %d lies behind the text kept (%d to %d)",
		a, y.histLow, y.histLow+len(y.hist)))
}

// b is the byte at source position a, 0 outside the source, which every
// read here tests for first, as `byteAt` does.
func (y *yamlSource) b(a int) byte {
	if x := uint(a - y.base); x < uint(len(y.src)) {
		return y.src[x]
	}
	return y.bSlow(a)
}

// bSlow is b past the window's text as last read. It is kept out of line,
// so that b and fwdText.b are inlined where they are read.
//
//go:noinline
func (y *yamlSource) bSlow(a int) byte {
	if c := y.atSlow(a); c >= 0 {
		return byte(c)
	}
	return 0
}

// has reports whether source position a lies in the source.
func (y *yamlSource) has(a int) bool {
	if uint(a-y.base) < uint(len(y.src)) {
		return true
	}
	return y.atSlow(a) >= 0
}

// sub is the source from position a to b, the source read on to b first.
// Every caller asks for text it has read, or for a prefix the source
// ends inside, which ends where the source does.
func (y *yamlSource) sub(a, b int) string {
	if b > a {
		y.at(b - 1)
	}
	x, z := a-y.base, b-y.base
	if z > len(y.lex.Src) {
		z = len(y.lex.Src)
	}
	if x >= 0 {
		if x > z {
			x = z
		}
		return y.lex.Src[x:z]
	}
	// Behind the window: from hist, then the window.
	var out strings.Builder
	for k := a; k < b && k < y.base; k++ {
		out.WriteByte(y.b(k))
	}
	if z > 0 {
		out.WriteString(y.lex.Src[:z])
	}
	return out.String()
}

// bomAtStart reports whether the source opens with a byte order mark.
func (y *yamlSource) bomAtStart() bool {
	return y.at(0) == 0xEF && y.at(1) == 0xBB && y.at(2) == 0xBF
}

// fwd is the source from the cursor on.
func (y *yamlSource) fwd() fwdText {
	return y.fwdAt(y.pos())
}

// fwdAt is the source from position a on.
func (y *yamlSource) fwdAt(a int) fwdText {
	f := fwdText{y: y, at: a}
	if x := a - y.base; 0 <= x && x <= len(y.src) {
		f.s = y.src[x:]
	}
	return f
}

// sync copies the source up to position to, which the window holds, onto
// the end of hist, noting each line break and the flow colon's byte as
// it goes.
func (y *yamlSource) sync(to int) {
	from := y.end
	if to <= from {
		return
	}
	if from < y.base {
		panic(fmt.Sprintf("yaml: source position %d was dropped before it was kept", from))
	}
	seg := y.sub(from, to)
	if y.windowed {
		y.hist = append(y.hist, seg...)
	}
	y.end = to
	for k := 0; k < len(seg); k++ {
		a, c := from+k, int(seg[k])
		if c == '\n' || c == '\r' {
			y.breaks = append(y.breaks, a)
			// Line breaks are lines' ends in every scan here, and neither
			// is blank or in a comment to the flow colon.
			y.jQuote, y.jSkip, y.jComment, y.jPrev = 0, false, false, -1
			continue
		}
		if y.jComment {
			continue
		}
		blank := c == ' ' || c == '\t'
		if y.jSkip {
			y.jSkip = false
		} else if y.jQuote != 0 {
			if y.jQuote == '"' && c == '\\' {
				y.jSkip = true
			} else if c == y.jQuote {
				if y.jQuote == '\'' && y.at(a+1) == '\'' {
					y.jSkip = true
				} else {
					y.jQuote = 0
				}
			}
		} else if c == '"' || c == '\'' {
			y.jQuote = c
		} else if c == '#' && (y.jPrev == -1 || y.jPrev == ' ' || y.jPrev == '\t') {
			y.jComment = true
			continue
		}
		if !blank {
			y.jSig = c
		}
		y.jPrev = c
	}
}

// trim drops the text hist keeps before the histBreaks-th last line break,
// once it is as long as what is kept, so that the copy costs a constant
// per byte.
func (y *yamlSource) trim() {
	if len(y.breaks) < histBreaks {
		return
	}
	if 2*histBreaks < len(y.breaks) {
		y.breaks = append(y.breaks[:0], y.breaks[len(y.breaks)-histBreaks:]...)
	}
	if !y.windowed {
		return
	}
	keep := y.breaks[len(y.breaks)-histBreaks] + 1
	dead := keep - y.histLow
	if dead <= 0 || dead < len(y.hist)-dead {
		return
	}
	y.hist = append(y.hist[:0], y.hist[dead:]...)
	y.histLow = keep
}

// lineStart is the source position of the start of the line that holds
// source position a: the source's start, or the position after a line
// break (`\n` or `\r`), as a scan back from a would find it. The breaks
// sync has passed answer it at once, which a scan back over a long line
// costs as many reads as the line has bytes for; past them, a scan back
// to them, and before the oldest of them kept, the scan.
func (y *yamlSource) lineStart(a int) int {
	if a > y.end {
		for k := a; k > y.end; k-- {
			if c := y.b(k - 1); c == '\n' || c == '\r' {
				return k
			}
		}
		a = y.end
	}
	bs := y.breaks
	k := sort.SearchInts(bs, a)
	if k > 0 {
		return bs[k-1] + 1
	}
	if len(bs) == 0 {
		// No break before the position sync has reached: a list trimmed
		// keeps histBreaks of them.
		return 0
	}
	for a > 0 && y.b(a-1) != '\n' && y.b(a-1) != '\r' {
		a--
	}
	return a
}

// quoteBefore reports whether the last byte before source position a that
// is neither blank nor in a comment is a quote: the flow colon's question.
func (y *yamlSource) quoteBefore(a int) bool {
	y.sync(a)
	return y.jSig == '"' || y.jSig == '\''
}

// blank reports whether the source holds nothing but white space after
// the comment lines that open it, as `jsTrim(src) == "" ||
// jsTrim(stripCommentLines(src)) == ""` asks of the whole text. It reads
// from the source's start and stops at the first character that is
// neither, so only the leading space and comments are read.
func (y *yamlSource) blank() bool {
	a := 0
	for {
		q := a
		for y.at(q) == ' ' || y.at(q) == '\t' {
			q++
		}
		if y.at(q) != '#' {
			break
		}
		for c := y.at(q); c >= 0 && c != '\n' && c != '\r'; c = y.at(q) {
			q++
		}
		if y.at(q) == '\r' {
			q++
		}
		if y.at(q) == '\n' {
			q++
		}
		a = q
	}
	for y.has(a) {
		r, size := utf8.DecodeRuneInString(y.sub(a, a+utf8.UTFMax))
		if !isJSSpace(r) {
			return false
		}
		a += size
	}
	return true
}

// fwdText is the source from a position on, index 0 being that position,
// read as the matcher's scans read their `fwd`.
type fwdText struct {
	// The window's text from that position, as far as it reached when
	// this was made: a read past it reads the source (yamlSource.atSlow).
	s  string
	y  *yamlSource
	at int
}

// b is the byte at index i, 0 past the source's end.
func (f fwdText) b(i int) byte {
	if uint(i) < uint(len(f.s)) {
		return f.s[i]
	}
	return f.y.bSlow(f.at + i)
}

// has reports whether index i lies in the source.
func (f fwdText) has(i int) bool {
	if uint(i) < uint(len(f.s)) {
		return true
	}
	return f.y.atSlow(f.at+i) >= 0
}

// sub is the text from index a to b.
func (f fwdText) sub(a, b int) string {
	return f.y.sub(f.at+a, f.at+b)
}

// upTo is the text from index 0 to n, or to the source's end, for the
// helpers that read a string and clamp at its end.
func (f fwdText) upTo(n int) string {
	f.y.at(f.at + n)
	end := f.y.base + len(f.y.lex.Src)
	if f.at+n < end {
		end = f.at + n
	}
	return f.y.sub(f.at, end)
}

// from is the text from index i on.
func (f fwdText) from(i int) fwdText {
	return f.y.fwdAt(f.at + i)
}

// prefix reports whether the text at index i holds word.
func (f fwdText) prefix(i int, word string) bool {
	for k := 0; k < len(word); k++ {
		if f.y.at(f.at+i+k) != int(word[k]) {
			return false
		}
	}
	return true
}

// prefixAt reports whether the source at position a holds word.
func (y *yamlSource) prefixAt(a int, word string) bool {
	for k := 0; k < len(word); k++ {
		if y.at(a+k) != int(word[k]) {
			return false
		}
	}
	return true
}

// lineEnd is the first source position at or after a that holds a line
// break, `\n` or `\r`, or the source's end: found with IndexAny over the
// window, read on while none is in it.
func (y *yamlSource) lineEnd(a int) int {
	for {
		if a < y.base {
			if c := y.at(a); c < 0 || c == '\n' || c == '\r' {
				return a
			}
			a++
			continue
		}
		src := y.lex.Src
		if x := a - y.base; x <= len(src) {
			if k := strings.IndexAny(src[x:], "\n\r"); k >= 0 {
				return a + k
			}
		}
		n := len(src)
		y.lex.Fill(n - y.lex.Cursor().SI + 1)
		if len(y.lex.Src) == n {
			return y.base + n
		}
		if a < y.base+n {
			a = y.base + n
		}
	}
}

// outside reads lex between cuts, where its cursor counts from the
// source's start and its window ends where the source has been read to.
func (y *yamlSource) outside(lex *tabnas.Lex) {
	y.lex = lex
	y.base = lex.Cursor().Len - len(lex.Src)
	y.src = lex.Src
}

// rb is the byte at window position x (lex.Src[x] as the whole text gives
// it), 0 outside the source.
func (y *yamlSource) rb(x int) byte {
	return y.b(y.base + x)
}

// rhas reports whether window position x lies in the source.
func (y *yamlSource) rhas(x int) bool {
	return y.at(y.base+x) >= 0
}

// structTagReach is how much of fwd structTagPrefixRe reads: past the
// `!!`, the run of characters that are not white space to JavaScript, and
// the one that ends it, where its `\b` is decided.
func structTagReach(fwd fwdText) int {
	k := 2
	for fwd.has(k) {
		r, size := utf8.DecodeRuneInString(fwd.sub(k, k+utf8.UTFMax))
		k += size
		if isJSSpace(r) {
			break
		}
	}
	return k
}
