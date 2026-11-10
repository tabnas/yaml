/* Copyright (c) 2021-2026 Richard Rodger, MIT License */

//! The source as the matcher and the text check read it, by source
//! position.
//!
//! In a parse of a whole text the lexer's source is that text. In a parse
//! from a reader a window at a time (`lex.window`, admin ADR-30), the
//! lexer holds a window of the source: the text from the oldest token the
//! parser holds to as far as it has been read, which the engine may move
//! on, dropping the text behind it, before each token it cuts. This
//! plugin keeps a copy of its own ([`Kept`]): the source from a few lines
//! before the cursor to as far as the window has read, brought up to the
//! window at every cut. The backward scans read the line a token starts on
//! and the two before it, and the copy keeps the text after the
//! [`KEPT_BREAKS`]-th last line break before the cursor, so they read what
//! the whole text holds whatever the window has dropped.
//!
//! The scans read the copy through [`Src`], by source position. A read at
//! or past the end of what has been read notes it ([`Src::touched`]): in a
//! parse from a reader the matcher then asks the engine to cut the token
//! again over a window twice as far ahead (`Lexer::advance_chars` past the
//! window's end, which is how the engine's own matchers ask), and the cut
//! is made again from the state it began with ([`Kept::save`],
//! [`Kept::restore`]). So every decision stands on the text the whole
//! source gives. No scan reads the source on itself: a read past the
//! window's end by the text check, which runs after the engine's number
//! and comment matchers, would otherwise end the window where the source
//! does after one of them had stopped short at its end, and the engine
//! would take the cut as it stood.
//!
//! Two questions the scans ask of the text far behind the cursor are
//! answered from what the copy noted as it went by ([`Lines`]): where the
//! line a position is on starts, from the line breaks passed, and whether
//! the last character before the cursor that is neither blank nor in a
//! comment is a quote, which makes a bare `:` a key separator in flow
//! syntax however many lines back the quote is.

use std::cell::Cell;
use std::sync::Arc;

use tabnas::{Context, Lexer, Value};

use crate::state;

/// How many line breaks [`Kept`] keeps the text after. A backward scan
/// reads the line a token starts on and at most the two before it, each
/// ended by one or two breaks (`\r\n`).
pub(crate) const KEPT_BREAKS: usize = 8;

/// What the source noted as it went by, up to [`Lines::end`].
#[derive(Clone, Debug, Default)]
pub(crate) struct Lines {
    /// The source position noted up to.
    pub(crate) end: usize,
    /// The source positions of the line breaks (`\n`, `\r`) before `end`,
    /// the last few: never fewer than [`KEPT_BREAKS`] once there were that
    /// many.
    pub(crate) breaks: Vec<usize>,
    /// The flow colon's question: the last byte before `end` that is
    /// neither blank nor in a comment (`sig`, -1 for none). A comment is a
    /// `#` that starts its line or follows a blank, outside a quoted
    /// scalar opened on that line; `quote` is the quote open, `skip` that
    /// the next byte is escaped or the second of a doubled single quote,
    /// `comment` that the rest of the line is a comment, `prev` the byte
    /// before, -1 at a line's start.
    pub(crate) sig: i32,
    pub(crate) quote: i32,
    pub(crate) skip: bool,
    pub(crate) comment: bool,
    pub(crate) prev: i32,
}

impl Lines {
    fn new() -> Self {
        Lines {
            sig: -1,
            prev: -1,
            ..Lines::default()
        }
    }

    /// Note the byte `c` at `pos`, whose next byte is `next` (-1 for
    /// none). Mirrors `histSync` in `src/yaml.ts`.
    fn note(&mut self, pos: usize, c: i32, next: i32, keep_breaks: bool) {
        if c == i32::from(b'\n') || c == i32::from(b'\r') {
            if keep_breaks {
                self.breaks.push(pos);
            }
            // Line breaks are lines' ends in every scan here, and neither
            // is blank or in a comment to the flow colon.
            self.quote = 0;
            self.skip = false;
            self.comment = false;
            self.prev = -1;
            return;
        }
        if self.comment {
            return;
        }
        let blank = c == i32::from(b' ') || c == i32::from(b'\t');
        if self.skip {
            self.skip = false;
        } else if self.quote != 0 {
            if self.quote == i32::from(b'"') && c == i32::from(b'\\') {
                self.skip = true;
            } else if c == self.quote {
                if self.quote == i32::from(b'\'') && next == i32::from(b'\'') {
                    self.skip = true;
                } else {
                    self.quote = 0;
                }
            }
        } else if c == i32::from(b'"') || c == i32::from(b'\'') {
            self.quote = c;
        } else if c == i32::from(b'#')
            && (self.prev == -1 || self.prev == i32::from(b' ') || self.prev == i32::from(b'\t'))
        {
            self.comment = true;
            return;
        }
        if !blank {
            self.sig = c;
        }
        self.prev = c;
    }
}

/// The source from a position on, read by byte offset: index 0 is source
/// position `org`. Every read goes through [`Src::at`] or reads no further
/// than it has, and one at or past the end of what has been read notes
/// it in `touched`.
#[derive(Clone, Copy)]
pub(crate) struct Src<'a> {
    /// The source from position `low` on, as far as it has been read.
    text: &'a str,
    low: usize,
    org: usize,
    touched: &'a Cell<bool>,
    lines: &'a Lines,
}

impl<'a> Src<'a> {
    pub(crate) fn new(
        text: &'a str,
        low: usize,
        touched: &'a Cell<bool>,
        lines: &'a Lines,
    ) -> Self {
        Src {
            text,
            low,
            org: 0,
            touched,
            lines,
        }
    }

    /// The source from index `index` on.
    #[inline]
    pub(crate) fn from(self, index: usize) -> Src<'a> {
        Src {
            org: self.org + index,
            ..self
        }
    }

    /// Where source position `pos` is in `text`, or None past its end.
    /// Before its start is a fault of this plugin's: the scans never read
    /// behind the lines [`Kept`] keeps, and a read that would fails the
    /// parse loudly rather than read a different text.
    #[inline]
    fn place(self, pos: usize) -> Option<usize> {
        let x = self.behind(pos);
        if x < self.text.len() {
            Some(x)
        } else {
            self.touched.set(true);
            None
        }
    }

    #[inline]
    fn behind(self, pos: usize) -> usize {
        match pos.checked_sub(self.low) {
            Some(x) => x,
            None => panic!(
                "yaml: source position {pos} lies behind the text kept ({} on)",
                self.low
            ),
        }
    }

    #[inline]
    fn offset(self, index: usize) -> Option<usize> {
        self.place(self.org + index)
    }

    /// The byte at source position `pos`, or `-1` past the end.
    #[inline]
    fn byte(self, pos: usize) -> i32 {
        match self.place(pos) {
            Some(x) => i32::from(self.text.as_bytes()[x]),
            None => -1,
        }
    }

    /// The byte at `index`, or `-1` past the end. The canonical matcher
    /// tests `fwd[i] === undefined` constantly; a signed sentinel keeps
    /// those tests readable and, unlike `0`, cannot collide with a NUL in
    /// the source.
    #[inline]
    pub(crate) fn at(self, index: usize) -> i32 {
        match self.offset(index) {
            Some(x) => i32::from(self.text.as_bytes()[x]),
            None => -1,
        }
    }

    /// Whether `index` lies in the source.
    #[inline]
    pub(crate) fn has(self, index: usize) -> bool {
        self.offset(index).is_some()
    }

    /// Whether the byte at `index` is `byte`.
    #[inline]
    pub(crate) fn is(self, index: usize, byte: u8) -> bool {
        self.at(index) == i32::from(byte)
    }

    /// Whether `index` is the end of the source or the start of a
    /// character, as `str::is_char_boundary` answers.
    pub(crate) fn is_char_boundary(self, index: usize) -> bool {
        let c = self.at(index);
        c < 0 || (c & 0xC0) != 0x80
    }

    /// The end of what has been read, as an index, or `index` if that is
    /// nearer: `index.min(len)` as the whole text gives it, the read past
    /// the end noted.
    pub(crate) fn clamp(self, index: usize) -> usize {
        let len = (self.low + self.text.len()).saturating_sub(self.org);
        if index > len {
            self.touched.set(true);
            len
        } else {
            index
        }
    }

    /// The end of what has been read, as an index, for a clamp of an index
    /// the scans have read up to: it asks nothing of the source past it.
    pub(crate) fn read_end(self) -> usize {
        (self.low + self.text.len()).saturating_sub(self.org)
    }

    /// The text from index `from` to `to`, which the scans have read:
    /// `&fwd[from..to]`. Past the end of what has been read it is cut
    /// there, and the read noted.
    pub(crate) fn slice(self, from: usize, to: usize) -> &'a str {
        let to = self.clamp(to);
        let from = from.min(to);
        &self.text[self.behind(self.org + from)..self.behind(self.org + to)]
    }

    /// The text from index 0 to `to`, or to the end of the source, cut
    /// back to the start of a character it would split: for the scans that
    /// read a prefix of a string and stop at its end.
    pub(crate) fn up_to(self, to: usize) -> &'a str {
        let mut to = self.clamp(to);
        while to > 0 && !self.is_char_boundary(to) {
            to -= 1;
        }
        self.slice(0, to)
    }

    /// The character at `index`, or None past the end.
    pub(crate) fn char_at(self, index: usize) -> Option<char> {
        let x = self.offset(index)?;
        self.text[x..].chars().next()
    }

    /// Whether the source at `index` holds the ASCII `word`, compared byte
    /// by byte so a multi-byte character cannot split a slice.
    pub(crate) fn starts_with(self, index: usize, word: &str) -> bool {
        word.bytes()
            .enumerate()
            .all(|(offset, byte)| self.at(index + offset) == i32::from(byte))
    }

    /// The first index at or after `index` that holds `byte`, or None.
    pub(crate) fn find(self, index: usize, byte: u8) -> Option<usize> {
        let from = self.behind(self.org + index).min(self.text.len());
        match self.text.as_bytes()[from..].iter().position(|&b| b == byte) {
            Some(k) => Some(index + k),
            None => {
                self.touched.set(true);
                None
            }
        }
    }

    /// The start of the line that holds source position `pos`: the
    /// source's start or the position after a line break, as a scan back
    /// from `pos` finds it. The line breaks noted answer it at once, where
    /// a scan back over a long line reads each of its bytes; past them, a
    /// scan back to them, and before the oldest of them kept, the scan.
    /// Mirrors `lineStartOf` in `src/yaml.ts`.
    pub(crate) fn line_start(self, pos: usize) -> usize {
        let mut at = pos;
        let end = self.lines.end;
        if at > end {
            for k in (end + 1..=at).rev() {
                let c = self.byte(k - 1);
                if c == i32::from(b'\n') || c == i32::from(b'\r') {
                    return k;
                }
            }
            at = end;
        }
        let breaks = &self.lines.breaks;
        let k = breaks.partition_point(|&b| b < at);
        if k > 0 {
            return breaks[k - 1] + 1;
        }
        if breaks.is_empty() {
            // No break before the position noted up to: a list trimmed
            // keeps KEPT_BREAKS of them.
            return 0;
        }
        while at > 0 {
            let c = self.byte(at - 1);
            if c == i32::from(b'\n') || c == i32::from(b'\r') {
                break;
            }
            at -= 1;
        }
        at
    }

    /// Whether the last byte before source position `pos` that is neither
    /// blank nor in a comment is a quote: the flow colon's question,
    /// however far back the quote is. The bytes from where the lines were
    /// noted up to `pos` are noted on a copy.
    pub(crate) fn quote_before(self, pos: usize) -> bool {
        let mut lines = Lines {
            breaks: Vec::new(),
            ..self.lines.clone()
        };
        for at in self.lines.end..pos {
            lines.note(at, self.byte(at), self.byte(at + 1), false);
        }
        lines.sig == i32::from(b'"') || lines.sig == i32::from(b'\'')
    }
}

/// The bag's entries a cut changes, which a cut made again begins from as
/// the first began.
const CUT_STATE: [&str; 13] = [
    state::V_POS,
    state::V_ROW,
    state::V_COL,
    state::MOVED,
    state::INIT,
    state::PENDING_CL,
    state::FLOW_DEPTH,
    state::FLOW_POS,
    state::FLOW_UPTO,
    state::FLOW_SQ,
    state::FLOW_DQ,
    state::PENDING_TOKENS,
    state::PENDING_ANCHORS,
];

/// The bag's slot that holds [`Kept`] between calls.
const KEPT: &str = "yamlKept";

/// Where each part of [`Kept`] sits in the bag's slot: the scalars, the
/// copy of the source, the bag's entries a cut began with, and from
/// [`BREAKS`] on the line breaks noted.
const WINDOWED: usize = 0;
const LOW: usize = 1;
const END: usize = 2;
const SIG: usize = 3;
const QUOTE: usize = 4;
const SKIP: usize = 5;
const COMMENT: usize = 6;
const PREV: usize = 7;
const BUF: usize = 8;
const CUT_POS: usize = 9;
const CUT_TAKEN: usize = 10;
const CUT_ROW: usize = 11;
const CUT_COL: usize = 12;
const SNAP: usize = 13;
const BREAKS: usize = 14;

/// What the plugin keeps of the source from one call to the next, in the
/// context's bag: the lines noted, and from a reader its copy of the
/// source and the state the cut it last began began with. A call takes it
/// out of the bag and puts it back, the slot's own array reused, so that
/// keeping it costs no allocation a call.
pub(crate) struct Kept {
    /// The bag's slot, taken out for the call.
    items: Arc<Vec<Value>>,
    windowed: bool,
    pub(crate) lines: Lines,
    /// From a reader: the source from position `low` to as far as the
    /// window has read.
    buf: String,
    low: usize,
    /// The cut last begun: its source position, and how many tokens the
    /// parser had taken by then. The engine cuts a token again over a
    /// wider window when a read reached the window's end, from the same
    /// place, before the parser takes another: such a cut begins from the
    /// state the first began with. With it, the virtual row and column it
    /// began at ([`crate::lex::virtual_point_for`]).
    cut_pos: usize,
    cut_taken: usize,
    cut_row: usize,
    cut_col: usize,
}

impl Kept {
    /// What this parse keeps, or, at its first call, nothing yet: a parse
    /// whose lexer is `windowed` reads a window of its source.
    pub(crate) fn load(context: &mut Context, windowed: bool) -> Kept {
        let taken = context
            .u
            .get_mut(KEPT)
            .map(|slot| std::mem::replace(slot, Value::Undefined));
        let Some(Value::Array(mut items)) = taken else {
            return Kept {
                items: Arc::new(vec![Value::Undefined; BREAKS]),
                windowed,
                lines: Lines::new(),
                buf: String::new(),
                low: 0,
                cut_pos: usize::MAX,
                cut_taken: usize::MAX,
                cut_row: 0,
                cut_col: 0,
            };
        };
        let fields = Arc::make_mut(&mut items);
        let number = |value: &Value| match value {
            Value::Number(n) => *n as usize,
            _ => 0,
        };
        let signed = |value: &Value| match value {
            Value::Number(n) => *n as i32,
            _ => -1,
        };
        let flag = |value: &Value| matches!(value, Value::Bool(true));
        let buf = match std::mem::replace(&mut fields[BUF], Value::Undefined) {
            Value::String(text) => text,
            _ => String::new(),
        };
        Kept {
            windowed: flag(&fields[WINDOWED]),
            lines: Lines {
                end: number(&fields[END]),
                breaks: fields[BREAKS..].iter().map(number).collect(),
                sig: signed(&fields[SIG]),
                quote: signed(&fields[QUOTE]),
                skip: flag(&fields[SKIP]),
                comment: flag(&fields[COMMENT]),
                prev: signed(&fields[PREV]),
            },
            buf,
            low: number(&fields[LOW]),
            cut_pos: number(&fields[CUT_POS]),
            cut_taken: number(&fields[CUT_TAKEN]),
            cut_row: number(&fields[CUT_ROW]),
            cut_col: number(&fields[CUT_COL]),
            items,
        }
    }

    /// Put what this parse keeps back in the bag for the next call.
    pub(crate) fn store(mut self, context: &mut Context) {
        let n = |value: usize| Value::Number(value as f64);
        let fields = Arc::make_mut(&mut self.items);
        fields[WINDOWED] = Value::Bool(self.windowed);
        fields[LOW] = n(self.low);
        fields[END] = n(self.lines.end);
        fields[SIG] = Value::Number(f64::from(self.lines.sig));
        fields[QUOTE] = Value::Number(f64::from(self.lines.quote));
        fields[SKIP] = Value::Bool(self.lines.skip);
        fields[COMMENT] = Value::Bool(self.lines.comment);
        fields[PREV] = Value::Number(f64::from(self.lines.prev));
        fields[BUF] = Value::String(self.buf);
        fields[CUT_POS] = n(self.cut_pos);
        fields[CUT_TAKEN] = n(self.cut_taken);
        fields[CUT_ROW] = n(self.cut_row);
        fields[CUT_COL] = n(self.cut_col);
        fields.truncate(BREAKS);
        fields.extend(self.lines.breaks.iter().map(|&b| n(b)));
        let value = Value::Array(self.items);
        match context.u.get_mut(KEPT) {
            Some(slot) => *slot = value,
            None => {
                context.u.insert(KEPT.to_string(), value);
            }
        }
    }

    pub(crate) fn windowed(&self) -> bool {
        self.windowed
    }

    /// The source as the scans read it: the `whole` text the lexer holds,
    /// or from a reader this plugin's copy of the window.
    pub(crate) fn src<'a>(&'a self, whole: &'a str, touched: &'a Cell<bool>) -> Src<'a> {
        if self.windowed {
            Src::new(&self.buf, self.low, touched, &self.lines)
        } else {
            Src::new(whole, 0, touched, &self.lines)
        }
    }

    /// Bring the copy up to the window: the text the window has read past
    /// its end is added to it.
    pub(crate) fn extend(&mut self, lexer: &Lexer<'_>) {
        if !self.windowed {
            return;
        }
        let window = lexer.source();
        let cursor = lexer.point().site.si;
        let low = cursor - (window.len() - lexer.remaining().len());
        let end = low + window.len();
        let kept = self.low + self.buf.len();
        if self.buf.is_empty() {
            self.low = low;
            self.buf.push_str(window);
            return;
        }
        if end > kept {
            assert!(
                low <= kept,
                "yaml: source position {kept} was dropped before it was kept"
            );
            self.buf.push_str(&window[kept - low..]);
        }
    }

    /// Note the source up to the cursor at `to`: its line breaks and the
    /// flow colon's question.
    pub(crate) fn note(&mut self, to: usize, lexer: &Lexer<'_>) {
        let from = self.lines.end;
        if to <= from {
            return;
        }
        let (text, low) = if self.windowed {
            (self.buf.as_str(), self.low)
        } else {
            (lexer.source(), 0)
        };
        let bytes = text.as_bytes();
        let at = |pos: usize| -> i32 {
            match pos.checked_sub(low) {
                Some(x) if x < bytes.len() => i32::from(bytes[x]),
                _ => -1,
            }
        };
        let mut lines = std::mem::take(&mut self.lines);
        for pos in from..to {
            lines.note(pos, at(pos), at(pos + 1), true);
        }
        lines.end = to;
        self.lines = lines;
    }

    /// Drop what is kept before the [`KEPT_BREAKS`]-th last line break,
    /// and before `limit`, the source position a cut made again reads
    /// from, once it is as long as what is kept, so that the copy costs a
    /// constant per byte.
    pub(crate) fn trim(&mut self, limit: usize) {
        let breaks = &mut self.lines.breaks;
        if breaks.len() < KEPT_BREAKS {
            return;
        }
        if 2 * KEPT_BREAKS < breaks.len() {
            breaks.drain(..breaks.len() - KEPT_BREAKS);
        }
        if !self.windowed {
            return;
        }
        let keep = (breaks[breaks.len() - KEPT_BREAKS] + 1).min(limit);
        let dead = keep.saturating_sub(self.low);
        if dead == 0 || dead < self.buf.len() - dead {
            return;
        }
        self.buf.drain(..dead);
        self.low = keep;
    }

    /// Whether the cut at `pos`, after `taken` tokens, is the last cut
    /// made again; if not, it is the cut now begun.
    pub(crate) fn again(&mut self, pos: usize, taken: usize) -> bool {
        if !self.windowed {
            return false;
        }
        if pos == self.cut_pos && taken == self.cut_taken {
            return true;
        }
        self.cut_pos = pos;
        self.cut_taken = taken;
        false
    }

    /// Keep the state a cut begins with, its virtual point `at` among it,
    /// and begin the record of what it changes of the anchors.
    pub(crate) fn save(&mut self, context: &mut Context, at: (usize, usize)) {
        if !self.windowed {
            return;
        }
        (self.cut_row, self.cut_col) = at;
        let snap = CUT_STATE
            .iter()
            .map(|key| context.u.get(*key).cloned().unwrap_or(Value::Undefined))
            .collect();
        Arc::make_mut(&mut self.items)[SNAP] = Value::array(snap);
        context
            .u
            .insert(state::UNDO.to_string(), Value::array(Vec::new()));
    }

    /// Begin a cut made again from the state the first began with, and
    /// answer the virtual point it began at.
    pub(crate) fn restore(&mut self, context: &mut Context) -> (usize, usize) {
        state::undo(context);
        if let Value::Array(snap) = &self.items[SNAP] {
            for (key, value) in CUT_STATE.iter().zip(snap.iter()) {
                if matches!(value, Value::Undefined) {
                    context.u.shift_remove(*key);
                } else {
                    context.u.insert((*key).to_string(), value.clone());
                }
            }
        }
        (self.cut_row, self.cut_col)
    }
}

/// How many tokens the parser has taken from the lexer, ignored ones
/// aside: those it has consumed, and the lookahead it holds.
pub(crate) fn taken(context: &Context) -> usize {
    context.v_abs + context.t.len()
}
