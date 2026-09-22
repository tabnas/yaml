/* Copyright (c) 2021-2026 Richard Rodger, MIT License */

//! The YAML lexer matcher, the port of the `yamlMatcher` closure in
//! `ts/src/yaml.ts`.
//!
//! It runs below the engine's first built-in band, so it sees the source
//! before the fixed-token, string, comment, number and text matchers do,
//! and it owns everything YAML spells differently from relaxed JSON:
//! indentation (`#IN`), block sequence items (`#EL`), document frames
//! (`#DS` / `#DE` / `#DR`), anchors, aliases, tags, explicit keys and
//! both quoted scalar forms.
//!
//! # Two cursors
//!
//! The canonical matcher mutates the lexer's own `Point` directly, and
//! not always to the position natural advancement would give: after a
//! standalone anchor or tag line it assigns `pnt.cI = spaces` rather than
//! `spaces + 1`, and before a document marker it assigns `0`. Those are
//! quirks, but they are load-bearing ones, because `@val-set-el-in`
//! computes a block sequence's indent as `#EL`'s column minus one. A port
//! that advanced naturally would accept `&a\n- 1\n- 2`, which the
//! canonical parser rejects.
//!
//! This engine's `Lexer` exposes advancement, not assignment, so this
//! module carries a VIRTUAL cursor beside the real one: the row and
//! column the canonical matcher would hold, kept in the context bag
//! ([`crate::state::V_ROW`], [`V_COL`](crate::state::V_COL),
//! [`V_POS`](crate::state::V_POS)). Every token this plugin emits is
//! built at the virtual point; the real cursor is moved by the same
//! number of characters, so the byte offsets never part. When another
//! matcher has consumed a token in between, the virtual point is carried
//! over that span the way the engine carries its own, so the offset
//! survives rather than being forgotten.
//!
//! # Bytes and columns
//!
//! Scanning is by BYTE offset, as the Go port scans: every character
//! YAML gives syntactic meaning is ASCII, and UTF-8 never puts an ASCII
//! byte inside a multi-byte sequence, so a byte comparison is a character
//! comparison and a byte offset that stopped at one is a character
//! boundary. Columns, though, count CHARACTERS, so advancing converts the
//! consumed span with `chars().count()`. Conflating the two is the defect
//! `go/column_units_test.go` was written for.

use tabnas::{Context, Lexer, Point, Rule, Site, Token, Value};

use crate::state;
use crate::text;

/// The reference name the grammar's `options.lex.match` entry uses.
pub(crate) const MATCHER: &str = "@yaml-matcher";

/// A token this plugin builds, with the virtual point it was built at.
/// The canonical matcher captures `lex.pnt` at different moments in
/// different branches, sometimes before its own advance and sometimes
/// after, so the point travels with the token rather than being inferred.
pub(crate) struct Tok {
    pub(crate) name: &'static str,
    pub(crate) val: Value,
    pub(crate) src: String,
    pub(crate) si: usize,
    pub(crate) ri: usize,
    pub(crate) ci: usize,
}

impl Tok {
    pub(crate) fn new(
        name: &'static str,
        val: Value,
        src: impl Into<String>,
        at: (usize, usize, usize),
    ) -> Self {
        Tok {
            name,
            val,
            src: src.into(),
            si: at.0,
            ri: at.1,
            ci: at.2,
        }
    }
}

/// What one matcher call decided: where the cursor ends up, what the
/// virtual row and column become, and the token to return, if any.
pub(crate) struct Act {
    pub(crate) si: usize,
    pub(crate) ri: usize,
    pub(crate) ci: usize,
    pub(crate) token: Option<Tok>,
    /// The source ran out inside a construct this matcher had already
    /// begun consuming. See [`exhausted`].
    pub(crate) bad: bool,
}

impl Act {
    pub(crate) fn still(at: (usize, usize, usize)) -> Self {
        Act {
            si: at.0,
            ri: at.1,
            ci: at.2,
            token: None,
            bad: false,
        }
    }

    pub(crate) fn moved(at: (usize, usize, usize), token: Option<Tok>) -> Self {
        Act {
            si: at.0,
            ri: at.1,
            ci: at.2,
            token,
            bad: false,
        }
    }

    /// This matcher moved the cursor and has no token for where it
    /// landed, and the canonical engine would leave that position
    /// unclaimed. See [`claimable_after_move`] and the end-of-source
    /// guard in [`decide`].
    pub(crate) fn refuse(at: (usize, usize, usize)) -> Self {
        Act {
            si: at.0,
            ri: at.1,
            ci: at.2,
            token: None,
            bad: true,
        }
    }
}

/// The byte at `index`, or `-1` past the end. The canonical matcher tests
/// `fwd[i] === undefined` constantly; a signed sentinel keeps those tests
/// readable and, unlike `0`, cannot collide with a NUL in the source.
#[inline]
pub(crate) fn at(src: &str, index: usize) -> i32 {
    src.as_bytes()
        .get(index)
        .map_or(-1, |byte| i32::from(*byte))
}

#[inline]
pub(crate) fn is(src: &str, index: usize, byte: u8) -> bool {
    at(src, index) == i32::from(byte)
}

/// Space or tab.
#[inline]
pub(crate) fn blank(value: i32) -> bool {
    value == i32::from(b' ') || value == i32::from(b'\t')
}

/// Carriage return or line feed.
#[inline]
pub(crate) fn line_end(value: i32) -> bool {
    value == i32::from(b'\n') || value == i32::from(b'\r')
}

/// Space, tab, carriage return, line feed, or end of source.
#[inline]
pub(crate) fn blank_line_end_or_eof(value: i32) -> bool {
    blank(value) || line_end(value) || value < 0
}

/// `text.replace(/\s+$/, '')`, over JavaScript's own space class.
pub(crate) fn trim_end(text: &str) -> &str {
    text.trim_end_matches(crate::js_space)
}

/// The largest character boundary at or before `index`, clamping an
/// index past the end to the end.
///
/// Every offset this plugin computes comes from a scan over BYTES that
/// stands in for the canonical scan over UTF-16 code units. A scan that
/// runs off the end of an unterminated construct, or one that steps over
/// a fixed width of escape digits, can leave an offset past the string
/// or inside a multibyte character, and slicing at either would panic on
/// input a caller does not control.
#[inline]
pub(crate) fn floor_boundary(text: &str, index: usize) -> usize {
    let mut index = index.min(text.len());
    while !text.is_char_boundary(index) {
        index -= 1;
    }
    index
}

/// `text.substring(from, to)` as JavaScript spells it: each index is
/// clamped into the string, and a pair the wrong way round is SWAPPED
/// rather than treated as empty. Both are first moved back to a
/// character boundary, per [`floor_boundary`].
pub(crate) fn js_substring(text: &str, from: usize, to: usize) -> &str {
    let from = floor_boundary(text, from);
    let to = floor_boundary(text, to);
    &text[from.min(to)..from.max(to)]
}

/// `text.substring(from, to - 1)` where `to` is a byte offset standing in
/// for a UTF-16 code-unit index, so the step back is ONE CODE UNIT.
///
/// `substring` clamps after the subtraction and swaps a reversed pair,
/// and both are load bearing: a scan that overran the end by one leaves
/// `to - 1` exactly at the end, and an empty value leaves the pair the
/// wrong way round, where the canonical yields the opening quote.
///
/// One unit back from just past an astral character lands BETWEEN its two
/// surrogates, and the canonical keeps the high one. A Rust string has no
/// place for an unpaired surrogate, so this folds it to the replacement
/// character, exactly as [`push_code_point`] folds an escape naming one.
/// A character inside the Basic Multilingual Plane is one unit and one
/// scalar, so stepping back drops the whole of it in both runtimes.
pub(crate) fn js_substring_less_one_unit(text: &str, from: usize, to: usize) -> String {
    let unit = to.saturating_sub(1);
    let end = floor_boundary(text, unit);
    let split_astral = unit < text.len()
        && end < unit
        && text[end..]
            .chars()
            .next()
            .is_some_and(|character| character.len_utf8() == 4);
    let from = floor_boundary(text, from);
    let mut out = text[from.min(end)..from.max(end)].to_string();
    if split_astral && from <= end {
        out.push('\u{fffd}');
    }
    out
}

/// The fixed-width window an escape's digits live in, and where the
/// scan resumes after it.
pub(crate) struct Window<'a> {
    /// `text.substring(from, from + count)`, ending at the character
    /// boundary at or before the window's last code unit.
    pub(crate) digits: &'a str,
    /// The byte offset `count` code units past `from`, clamped to the
    /// end and floored to a character boundary.
    pub(crate) end: usize,
    /// The LOW surrogate of a character the window cut in half, which no
    /// byte offset can point at. See [`take_units`].
    pub(crate) split_low: Option<u32>,
}

/// `text.substring(from, from + count)` where `count` is a number of
/// UTF-16 CODE UNITS, which is what the canonical window is counted in.
///
/// Counting CHARACTERS instead reads the same window only while every
/// character in it is one code unit. An astral character is TWO units
/// and one Rust scalar, so a window holding one takes too much text and
/// leaves the cursor too far along; a window whose LAST unit falls
/// inside one cuts the character in half.
///
/// A cut is not an error in the canonical either: it keeps the HIGH
/// surrogate, which is no hexadecimal digit and so ends `parseInt`'s
/// prefix exactly where stopping short of the character does, and leaves
/// the LOW surrogate for the next round of the scan, where it is
/// appended as a character of its own. `split_low` hands that unit back,
/// because a byte offset cannot point between two surrogates.
///
/// Fewer than `count` units left at `from` is not an error, exactly as a
/// `substring` past the end is not.
pub(crate) fn take_units(text: &str, from: usize, count: usize) -> Window<'_> {
    let from = floor_boundary(text, from);
    let tail = &text[from..];
    let mut units = 0usize;
    let mut offset = 0usize;
    for (index, character) in tail.char_indices() {
        let width = character.len_utf16();
        if units + width > count {
            let mut pair = [0u16; 2];
            let encoded = character.encode_utf16(&mut pair);
            let split_low = (width == 2 && units < count).then(|| u32::from(encoded[1]));
            return Window {
                digits: &tail[..index],
                end: from + index,
                split_low,
            };
        }
        units += width;
        offset = index + character.len_utf8();
        if units == count {
            break;
        }
    }
    Window {
        digits: &tail[..offset],
        end: from + offset,
        split_low: None,
    }
}

/// Advance a `(si, ri, ci)` triple over `count` bytes of `src` starting
/// at `si`, charging one column per CHARACTER: the canonical
/// `pnt.sI += n; pnt.cI += n` over UTF-16 units, which count characters
/// there and would count bytes here.
pub(crate) fn advance_cols(
    src: &str,
    at: (usize, usize, usize),
    count: usize,
) -> (usize, usize, usize) {
    let mut end = (at.0 + count).min(src.len());
    // Every scan here stops on an ASCII byte, so `end` is normally a
    // character boundary already. A count taken from a token's decoded
    // text rather than from its source span may not be, and slicing one
    // would panic.
    while end < src.len() && !src.is_char_boundary(end) {
        end += 1;
    }
    let columns = src[at.0..end].chars().count();
    (end, at.1, at.2 + columns)
}

/// Carry a `(row, column)` pair forward over `src[from..to]` the way the
/// engine's own cursor moves: a line feed starts a new row at column one,
/// anything else charges one column.
fn carry(src: &str, from: usize, to: usize, row: usize, column: usize) -> (usize, usize) {
    let (mut row, mut column) = (row, column);
    let from = from.min(src.len());
    let to = to.min(src.len()).max(from);
    for character in src[from..to].chars() {
        if character == '\n' {
            row += 1;
            column = 1;
        } else {
            column += 1;
        }
    }
    (row, column)
}

/// The virtual point for this call: what was stored, carried over
/// anything another matcher consumed since, or the real cursor when
/// nothing has been stored yet.
pub(crate) fn virtual_point_for(lexer: &Lexer<'_>, context: &mut Context) -> (usize, usize) {
    let point = lexer.point();
    let here = point.site.si;
    let Some(stored) = state::num(context, state::V_POS) else {
        return (point.site.ri, point.site.ci);
    };
    let stored = stored.max(0.0) as usize;
    if stored > here {
        return (point.site.ri, point.site.ci);
    }
    let row = state::usize_at(context, state::V_ROW);
    let column = state::usize_at(context, state::V_COL);
    carry(lexer.source(), stored, here, row, column)
}

fn store_point(context: &mut Context, act: &Act) {
    state::set_usize(context, state::V_POS, act.si);
    state::set_usize(context, state::V_ROW, act.ri);
    state::set_usize(context, state::V_COL, act.ci);
}

/// Apply a decision: build the token at its own virtual point, move the
/// real cursor by the same number of characters, and record where the
/// virtual cursor now stands.
pub(crate) fn apply(lexer: &mut Lexer<'_>, context: &mut Context, act: Act) -> Option<Token> {
    let point = lexer.point();
    let start = point.site.si;
    let start_pos = point.site.pos;
    let length = point.len;

    let token = {
        let source = lexer.source();
        act.token.as_ref().map(|token| {
            let offset = token.si.clamp(start, source.len());
            let pos = start_pos + source[start..offset].chars().count();
            // A tin of -1 asks the engine to resolve the identity from the
            // name, which it does for every custom matcher result. Every
            // name used here is registered before the grammar is installed.
            Token::new(
                token.name,
                -1,
                token.val.clone(),
                token.src.as_str(),
                Point {
                    len: length,
                    site: Site {
                        si: offset,
                        pos,
                        ri: token.ri,
                        ci: token.ci,
                    },
                },
            )
        })
    };

    let characters = {
        let source = lexer.source();
        let end = act.si.clamp(start, source.len());
        source[start..end].chars().count()
    };
    if characters > 0 {
        lexer.advance_chars(characters);
    }
    store_point(context, &act);
    if act.bad {
        // The refusal is reported where this plugin's cursor stands, not
        // where the engine's does, for the reason every other token here
        // carries the plugin's point.
        let mut refusal = lexer.bad("unexpected");
        refusal.site.ri = act.ri;
        refusal.site.ci = act.ci;
        return Some(refusal);
    }
    token
}

/// The matcher itself, in the shape [`tabnas::Tabnas::imperative_lex_match_ref`]
/// takes. The decision is made against an immutable view of the source so
/// the cursor moves exactly once, at the end.
pub(crate) fn yaml_matcher(
    lexer: &mut Lexer<'_>,
    _rule: &mut Rule,
    context: &mut Context,
) -> Option<Token> {
    let (row, column) = virtual_point_for(lexer, context);
    let start = lexer.point().site.si;
    let act = {
        let source = lexer.source();
        decide(source, (start, row, column), context)
    };
    // A call that moved the cursor and produced nothing leaves the rest
    // of the matcher chain reading a position it was not dispatched for.
    // The text check compensates; see `crate::text::check`.
    let moved = act.token.is_none() && !act.bad && act.si != start;
    state::set_flag(context, state::MOVED, moved);
    apply(lexer, context, act)
}

/// Remove every comment line from `src`, the port of the canonical
/// `src.replace(/^[ \t]*#[^\n]*(\n|$)/gm, '')`. A line whose first
/// non-blank character is `#` goes, terminator included.
fn strip_comment_lines(src: &str) -> String {
    let mut out = String::with_capacity(src.len());
    let mut rest = src;
    while !rest.is_empty() {
        let end = rest.find('\n').map_or(rest.len(), |index| index + 1);
        let (line, tail) = rest.split_at(end);
        if !line.trim_start_matches([' ', '\t']).starts_with('#') {
            out.push_str(line);
        }
        rest = tail;
    }
    out
}

/// The body of the canonical `yamlMatcher`, as a decision over an
/// immutable source.
#[allow(clippy::too_many_lines)]
fn decide(src: &str, start: (usize, usize, usize), context: &mut Context) -> Act {
    // First call of a parse: plant the state the accessors expect, and
    // answer a source with no content at all with one null value, so the
    // parser yields null rather than refusing the document.
    if !state::flag(context, state::INIT) {
        state::initialise(context);
        // `src.trim() === '' || stripped === ''` in the canonical port,
        // where `trim` strips JavaScript's whitespace rather than Rust's.
        if src.trim_matches(crate::js_space).is_empty()
            || strip_comment_lines(src)
                .trim_matches(crate::js_space)
                .is_empty()
        {
            return Act::moved(
                (src.len(), start.1, start.2),
                Some(Tok::new("#VL", Value::Null, "", start)),
            );
        }
    }

    // Tokens the explicit-key handler queued, in order.
    if state::list_len(context, state::PENDING_TOKENS) > 0 {
        if let Some(queued) = state::list_shift(context, state::PENDING_TOKENS) {
            return Act::moved(start, Some(decode_token(&queued, start)));
        }
    }

    let first = at(src, start.0);
    let mut cursor = start;
    loop {
        // Consuming the last of the source without producing a token
        // leaves the engine's matcher chain with nothing to claim and no
        // character to reject. End of source IS the next token.
        if cursor.0 >= src.len() {
            return Act::refuse(cursor);
        }
        let fwd = &src[cursor.0..];

        // A line of only tabs and spaces is blank in YAML, but the engine
        // refuses a bare tab, so skip it here.
        if blank(at(fwd, 0)) {
            let line_end_index = fwd.find('\n');
            let line = line_end_index.map_or(fwd, |index| &fwd[..index]);
            if line.contains('\t')
                && !line.is_empty()
                && line.bytes().all(|byte| blank(i32::from(byte)))
            {
                let skip = line_end_index.map_or(line.len(), |index| index + 1);
                cursor = (cursor.0 + skip, cursor.1 + 1, 0);
                continue;
            }
        }

        // The `#CL` the explicit-key handler owes, before any tag branch,
        // so the colon precedes the value it introduces.
        if state::flag(context, state::PENDING_CL) {
            state::set_flag(context, state::PENDING_CL, false);
            return Act::moved(
                cursor,
                Some(Tok::new("#CL", Value::Number(1.0), ": ", cursor)),
            );
        }

        // Alias: *name.
        if is(fwd, 0, b'*') {
            return alias(src, fwd, cursor, context);
        }

        // Anchor: &name. The marker is consumed and the anchor recorded;
        // the value that follows is lexed on the next pass of this loop.
        if is(fwd, 0, b'&') {
            match anchor(src, fwd, cursor, context) {
                Ok(next) => {
                    cursor = next;
                    continue;
                }
                Err(act) => return *act,
            }
        }

        // Directive line at column 0: %YAML, %TAG, %FOO.
        if is(fwd, 0, b'%') && at_line_start(src, cursor.0) {
            let mut pos = 0;
            while pos < fwd.len() && !line_end(at(fwd, pos)) {
                pos += 1;
            }
            let text = fwd[..pos].to_string();
            let next = advance_cols(src, cursor, pos);
            return Act::moved(
                next,
                Some(Tok::new("#DR", Value::String(text.clone()), text, next)),
            );
        }

        // Non-specific tag (`! value`) and local tag (`!name value`).
        if is(fwd, 0, b'!') && !is(fwd, 1, b'!') && at(fwd, 1) >= 0 {
            if is(fwd, 1, b' ') {
                let mut val_end = 2;
                while val_end < fwd.len() && !line_end(at(fwd, val_end)) {
                    val_end += 1;
                }
                let raw = trim_end(&fwd[2..val_end]).to_string();
                let source = fwd[..val_end].to_string();
                let next = advance_cols(src, cursor, val_end);
                return Act::moved(
                    next,
                    Some(Tok::new("#TX", Value::String(raw), source, cursor)),
                );
            }
            cursor = local_tag(src, fwd, cursor);
            continue;
        }

        // Structural tags (!!seq, !!map, !!omap, ...): consumed, with the
        // structure that follows left to the next pass.
        if is(fwd, 0, b'!') && is(fwd, 1, b'!') && is_struct_tag(fwd) {
            match struct_tag(src, fwd, cursor) {
                Ok(next) => {
                    cursor = next;
                    continue;
                }
                Err(act) => return *act,
            }
        }

        // Typed tags (!!str, !!int, !!float, !!bool, !!null, and any
        // other `!!name`), which convert the value that follows.
        if is(fwd, 0, b'!') && is(fwd, 1, b'!') {
            match text::type_tag(src, fwd, cursor, context) {
                Ok(next) => {
                    if next.0 == cursor.0 {
                        // No progress: refuse rather than spin.
                        return Act::still(cursor);
                    }
                    cursor = next;
                    continue;
                }
                Err(act) => return *act,
            }
        }

        // `?` inside a flow collection is the explicit-key indicator and
        // becomes a token the pair and elem rules consume.
        if is(fwd, 0, b'?') && blank(at(fwd, 1)) && state::flow_depth_at(context, src, cursor.0) > 0
        {
            let next = advance_cols(src, cursor, 1);
            return Act::moved(next, Some(Tok::new("#QM", Value::Undefined, "?", cursor)));
        }

        // `?` in block context: the heavyweight explicit-key handler.
        if is(fwd, 0, b'?') && (blank(at(fwd, 1)) || line_end(at(fwd, 1)) || at(fwd, 1) < 0) {
            return explicit_key(src, fwd, cursor, context);
        }

        // Document frame markers at column 0.
        if at_line_start(src, cursor.0) && is_doc_marker(fwd, 0) {
            return doc_marker(src, fwd, cursor);
        }

        // Quoted scalars: YAML's escaping and folding, not the engine's.
        if is(fwd, 0, b'"') {
            return double_quoted(src, fwd, cursor);
        }
        if is(fwd, 0, b'\'') {
            return single_quoted(src, fwd, cursor);
        }

        // A value that starts with a digit but is not a number.
        if at(fwd, 0) >= 0 && (at(fwd, 0) as u8).is_ascii_digit() {
            if let Some(act) = numeric_plain(src, fwd, cursor, context) {
                return act;
            }
        }

        // Block sequence entry: `- `.
        if is(fwd, 0, b'-') && blank_line_end_or_eof(at(fwd, 1)) {
            let mut next = advance_cols(src, cursor, 1);
            if blank(at(fwd, 1)) {
                next = advance_cols(src, next, 1);
            }
            return Act::moved(next, Some(Tok::new("#EL", Value::Undefined, "- ", cursor)));
        }

        // Key separator. A colon with no space after it separates only
        // when a quoted scalar precedes it, the JSON-compatible flow form.
        if is(fwd, 0, b':') {
            let flow_colon = !blank_line_end_or_eof(at(fwd, 1)) && quoted_before(src, cursor.0);
            if blank_line_end_or_eof(at(fwd, 1)) || flow_colon {
                let token = Tok::new("#CL", Value::Number(1.0), ": ", cursor);
                let next = if blank(at(fwd, 1)) {
                    (cursor.0 + 1, cursor.1, cursor.2 + 2)
                } else if line_end(at(fwd, 1)) {
                    // The newline is left for the indent handler.
                    (cursor.0 + 1, cursor.1, cursor.2)
                } else {
                    (cursor.0 + 1, cursor.1, cursor.2 + 1)
                };
                return Act::moved(next, Some(token));
            }
        }

        // Flow punctuation. The engine's fixed-token matcher would take
        // these, but it builds its token at the engine's own cursor, and
        // this plugin's cursor is one column ahead of it from a `: `
        // onwards (see the module note). Emitting them here keeps every
        // token of a YAML parse on the one cursor, so a diagnostic
        // reports the column the canonical parser reports.
        //
        // Only where the fixed matcher would itself have had a turn: the
        // cursor has not moved during this call, so the character the
        // call dispatched on is this one.
        if cursor.0 == start.0 && flow_punctuation(at(fwd, 0)).is_some() {
            let name = flow_punctuation(at(fwd, 0)).expect("just tested");
            let text = &fwd[..1];
            let token = Tok::new(name, Value::String(text.to_string()), text, cursor);
            return Act::moved(advance_cols(src, cursor, 1), Some(token));
        }

        // Newlines. Inside a flow collection they are whitespace; in block
        // context they carry the indentation the grammar runs on.
        if line_end(at(fwd, 0)) {
            if state::flow_depth_at(context, src, cursor.0) > 0 {
                match flow_newline(src, fwd, cursor) {
                    Ok(next) => {
                        cursor = next;
                        continue;
                    }
                    Err(act) => return *act,
                }
            }
            match block_newline(src, fwd, cursor, context) {
                Ok(next) => {
                    cursor = next;
                    continue;
                }
                Err(act) => return *act,
            }
        }

        // Nothing here is this matcher's. When the cursor has moved
        // during this call, the engine's remaining matchers are the ones
        // chosen for the character the call started on, so a character
        // outside that set is unclaimed and the parse is refused.
        if cursor.0 != start.0 && !claimable_after_move(first, at(src, cursor.0)) {
            return Act::refuse(cursor);
        }
        return Act::still(cursor);
    }
}

/// Which built-in matcher family can start on `byte`, under the option
/// overlay this plugin installs. `None` means the families that run on
/// any input (number, value, text) and the plugin's own matchers.
///
/// The canonical engine dispatches on the FIRST character of a lexer
/// call: only the matchers that could produce a token starting there are
/// run at all. A plugin matcher that moves the cursor and then produces
/// nothing therefore leaves the new position to a matcher set chosen for
/// the old one, and a character outside that set is simply unclaimed.
/// This is not incidental: the canonical matcher emits a flow
/// collection's opener itself after an anchor or a tag for exactly this
/// reason, and the one place it does not compensate, a `{` or `[` at
/// column 0 after a blank or comment line, is refused.
fn dispatch_family(byte: i32) -> Option<u8> {
    Some(match byte {
        value if value == i32::from(b'{') => 0,
        value if value == i32::from(b'}') => 0,
        value if value == i32::from(b'[') => 0,
        value if value == i32::from(b']') => 0,
        value if value == i32::from(b',') => 0,
        value if value == i32::from(b'\n') => 1,
        value if value == i32::from(b'\r') => 1,
        value if value == i32::from(b'#') => 2,
        _ => return None,
    })
}

/// Whether a matcher able to claim `here` would have run in a call that
/// began at `first`.
///
/// Only the fixed, line and comment families are listed. The string
/// family is in no set at all, because this plugin empties
/// `string.chars` and lexes both quoted forms itself. Whitespace is not
/// listed either: this plugin's `text.check` claims a plain scalar that
/// begins with a space, and the text family runs whatever the call
/// dispatched on.
fn claimable_after_move(first: i32, here: i32) -> bool {
    match dispatch_family(here) {
        None => true,
        family => dispatch_family(first) == family,
    }
}

/// The five single-character fixed tokens jsonic keeps, by name. The
/// colon is not among them: this plugin removes it, because YAML's key
/// separator is `: `, which the matcher emits itself.
fn flow_punctuation(byte: i32) -> Option<&'static str> {
    Some(match byte {
        value if value == i32::from(b'[') => "#OS",
        value if value == i32::from(b']') => "#CS",
        value if value == i32::from(b'{') => "#OB",
        value if value == i32::from(b'}') => "#CB",
        value if value == i32::from(b',') => "#CA",
        _ => return None,
    })
}

fn at_line_start(src: &str, offset: usize) -> bool {
    offset == 0 || line_end(at(src, offset - 1))
}

/// Three of `-` or three of `.` at `index`, with nothing said about what
/// follows.
fn doc_marker_run(src: &str, index: usize) -> bool {
    // Compared byte by byte: a slice would panic on a source whose next
    // character is multi-byte, and `---` and `...` are ASCII anyway.
    let head = at(src, index);
    if head != i32::from(b'-') && head != i32::from(b'.') {
        return false;
    }
    at(src, index + 1) == head && at(src, index + 2) == head
}

/// `---` or `...` followed by a blank, a line end or end of source.
pub(crate) fn is_doc_marker(src: &str, index: usize) -> bool {
    doc_marker_run(src, index) && blank_line_end_or_eof(at(src, index + 3))
}

/// `---` or `...` followed by a SPACE, a line end or end of source, with
/// a tab excluded.
///
/// The canonical spells this test out at four sites and the four are not
/// the same: three take a tab after the marker and the one that stops a
/// BLOCK SCALAR does not, so a `---<TAB>` line stays part of the scalar
/// there and ends the document everywhere else.
pub(crate) fn is_doc_marker_no_tab(src: &str, index: usize) -> bool {
    let after = at(src, index + 3);
    doc_marker_run(src, index) && (after == i32::from(b' ') || line_end(after) || after < 0)
}

/// Whether the source at `index` starts with the ASCII `word`, compared
/// byte by byte so a multi-byte character cannot split a slice.
fn starts_with_at(src: &str, index: usize, word: &str) -> bool {
    word.bytes()
        .enumerate()
        .all(|(offset, byte)| at(src, index + offset) == i32::from(byte))
}

/// `^!!(seq|map|omap|set|pairs|binary|ordered|python/\S*)\b`, the
/// structural tags whose value is the structure that follows.
fn is_struct_tag(fwd: &str) -> bool {
    for name in ["seq", "map", "omap", "set", "pairs", "binary", "ordered"] {
        if starts_with_at(fwd, 2, name) {
            // The canonical pattern ends in `\b`, so a longer word does
            // not match.
            let after = at(fwd, 2 + name.len());
            let word =
                after >= 0 && (after as u8).is_ascii_alphanumeric() || after == i32::from(b'_');
            if !word {
                return true;
            }
        }
    }
    if !starts_with_at(fwd, 2, "python/") {
        return false;
    }
    // `python/` carries the same trailing `\b`, and `/` is not a word
    // character, so the boundary holds only where the non-blank run after
    // the slash carries one: `\S*` backtracks to the first of them. The
    // word characters are JavaScript's ASCII `\w`, and the run ends at
    // JavaScript's `\s`. `!!python/ [1]` therefore keeps its `[1]`.
    fwd["!!python/".len()..]
        .chars()
        .take_while(|character| !crate::js_space(*character))
        .any(|character| character.is_ascii_alphanumeric() || character == '_')
}

/// Encode a token for the queue in the context bag, and read it back.
pub(crate) fn encode_token(token: &Tok) -> Value {
    let mut map = indexmap::IndexMap::new();
    map.insert("n".to_string(), Value::String(token.name.to_string()));
    map.insert("v".to_string(), token.val.clone());
    map.insert("s".to_string(), Value::String(token.src.clone()));
    map.insert("i".to_string(), Value::Number(token.si as f64));
    map.insert("r".to_string(), Value::Number(token.ri as f64));
    map.insert("c".to_string(), Value::Number(token.ci as f64));
    Value::object(map)
}

fn decode_token(value: &Value, fallback: (usize, usize, usize)) -> Tok {
    let field = |key: &str| -> Option<Value> {
        match value {
            Value::Object(map) => map.get(key).cloned(),
            _ => None,
        }
    };
    let number = |key: &str, default: usize| -> usize {
        match field(key) {
            Some(Value::Number(number)) => number.max(0.0) as usize,
            _ => default,
        }
    };
    let name: &'static str = match field("n") {
        Some(Value::String(name)) => match name.as_str() {
            "#CL" => "#CL",
            "#IN" => "#IN",
            "#VL" => "#VL",
            "#TX" => "#TX",
            _ => "#TX",
        },
        _ => "#TX",
    };
    Tok {
        name,
        val: field("v").unwrap_or(Value::Undefined),
        src: match field("s") {
            Some(Value::String(text)) => text,
            _ => String::new(),
        },
        si: number("i", fallback.0),
        ri: number("r", fallback.1),
        ci: number("c", fallback.2),
    }
}

// ---------------------------------------------------------------------
// Aliases and anchors
// ---------------------------------------------------------------------

/// The characters an anchor or alias name stops at.
fn name_end(fwd: &str, alias: bool) -> usize {
    let mut index = 1;
    while index < fwd.len() {
        let byte = at(fwd, index);
        if blank(byte)
            || line_end(byte)
            || byte == i32::from(b',')
            || byte == i32::from(b'{')
            || byte == i32::from(b'}')
            || byte == i32::from(b'[')
            || byte == i32::from(b']')
        {
            break;
        }
        // A colon ends a name only when it separates a key from a value;
        // elsewhere it is a legal name character.
        if alias && byte == i32::from(b':') && blank(at(fwd, index + 1)) {
            break;
        }
        index += 1;
    }
    index
}

fn alias(src: &str, fwd: &str, cursor: (usize, usize, usize), context: &mut Context) -> Act {
    let end = name_end(fwd, true);
    let name = fwd[1..end].to_string();
    let source = fwd[..end].to_string();

    // An alias used as a key resolves to its anchor's text right here.
    let mut after = end;
    while blank(at(fwd, after)) {
        after += 1;
    }
    let is_key = is(fwd, after, b':') && blank_line_end_or_eof(at(fwd, after + 1));
    let recorded = state::map_get(context, state::ANCHORS, &name);
    let next = advance_cols(src, cursor, end);

    if is_key {
        if let Some(value) = recorded.clone() {
            if !matches!(value, Value::Undefined) {
                return Act::moved(
                    next,
                    Some(Tok::new(
                        "#TX",
                        Value::String(crate::string_of(&value)),
                        source,
                        cursor,
                    )),
                );
            }
        }
    }

    match recorded {
        Some(value) if !matches!(value, Value::Undefined) => {
            let (name_of, value) = match &value {
                Value::String(_) => ("#TX", value.clone()),
                Value::Number(_) => ("#NR", value.clone()),
                Value::Object(_) | Value::Array(_) | Value::MapRef(_) | Value::ListRef(_) => {
                    ("#VL", crate::deep_copy(&value))
                }
                _ => ("#VL", value.clone()),
            };
            Act::moved(next, Some(Tok::new(name_of, value, source, cursor)))
        }
        _ => {
            let mut marker = indexmap::IndexMap::new();
            marker.insert("__yamlAlias".to_string(), Value::String(name));
            Act::moved(
                next,
                Some(Tok::new("#VL", Value::object(marker), source, cursor)),
            )
        }
    }
}

/// Consume an anchor marker, recording the anchor and whatever inline
/// scalar value can be read off the rest of the line. Returns the cursor
/// to continue from, or a token when the anchored value is a flow
/// collection whose opener this has to emit itself.
fn anchor(
    src: &str,
    fwd: &str,
    cursor: (usize, usize, usize),
    context: &mut Context,
) -> Result<(usize, usize, usize), Box<Act>> {
    let end = name_end(fwd, false);
    let name = fwd[1..end].to_string();
    let mut skip = end;
    if blank(at(fwd, skip)) {
        skip += 1;
    }

    // Only whitespace before the marker on its line makes it standalone.
    let mut standalone = true;
    let mut indent = 0usize;
    let mut back = cursor.0;
    while back > 0 && !line_end(at(src, back - 1)) {
        back -= 1;
        if !blank(at(src, back)) {
            standalone = false;
            break;
        }
        indent += 1;
    }

    let mut next = advance_cols(src, cursor, skip);

    // An inline comment after the anchor is not the anchored value.
    {
        let mut index = next.0;
        while blank(at(src, index)) {
            index += 1;
        }
        if is(src, index, b'#') {
            while index < src.len() && !line_end(at(src, index)) {
                index += 1;
            }
            next = advance_cols(src, next, index - next.0);
        }
    }

    let inline = !(standalone && (line_end(at(src, next.0)) || next.0 >= src.len()));
    if inline {
        let peek = &src[next.0.min(src.len())..];
        let head = at(peek, 0);
        if head >= 0
            && head != i32::from(b'[')
            && head != i32::from(b'{')
            && head != i32::from(b'>')
            && head != i32::from(b'|')
            && !line_end(head)
        {
            if let Some(scalar) = inline_scalar(peek) {
                state::map_set(context, state::ANCHORS, name.clone(), Value::String(scalar));
            }
        }
    }

    let mut pending = indexmap::IndexMap::new();
    pending.insert("n".to_string(), Value::String(name));
    pending.insert("i".to_string(), Value::Bool(inline));
    state::list_push(context, state::PENDING_ANCHORS, Value::object(pending));

    // An anchored flow collection: emit the opener here, because the
    // cursor has already passed the marker and the fixed-token matcher
    // would read the bracket as a bad character.
    let opener = at(src, next.0);
    if opener == i32::from(b'[') || opener == i32::from(b'{') {
        let (name_of, text) = if opener == i32::from(b'[') {
            ("#OS", "[")
        } else {
            ("#OB", "{")
        };
        let token = Tok::new(name_of, Value::String(text.to_string()), text, next);
        return Err(Box::new(Act::moved(
            advance_cols(src, next, 1),
            Some(token),
        )));
    }

    // A standalone anchor line: consume the newline and the next line's
    // indent so no spurious `#IN` separates the anchor from its value.
    if standalone && line_end(at(src, next.0)) {
        let mut newline = next.0;
        if is(src, newline, b'\r') {
            newline += 1;
        }
        if is(src, newline, b'\n') {
            newline += 1;
        }
        let mut spaces = 0;
        while is(src, newline + spaces, b' ') {
            spaces += 1;
        }
        let following = at(src, newline + spaces);
        if following >= 0 && !line_end(following) && spaces >= indent {
            next = (newline + spaces, next.1 + 1, spaces);
        }
    }

    Ok(next)
}

/// The scalar an inline anchor names, read off the rest of the line.
fn inline_scalar(peek: &str) -> Option<String> {
    match at(peek, 0) {
        value if value == i32::from(b'"') => {
            let mut index = 1;
            while index < peek.len() && !is(peek, index, b'"') {
                if is(peek, index, b'\\') {
                    index += 1;
                }
                index += 1;
            }
            let raw = &peek[1..index.min(peek.len())];
            Some(
                raw.replace("\\n", "\n")
                    .replace("\\t", "\t")
                    .replace("\\\\", "\\")
                    .replace("\\\"", "\""),
            )
        }
        value if value == i32::from(b'\'') => {
            let mut index = 1;
            while index < peek.len() && !is(peek, index, b'\'') {
                if is(peek, index, b'\'') && is(peek, index + 1, b'\'') {
                    index += 1;
                }
                index += 1;
            }
            let raw = &peek[1..index.min(peek.len())];
            Some(raw.replace("''", "'"))
        }
        _ => {
            let mut index = 0;
            while index < peek.len() {
                let byte = at(peek, index);
                if line_end(byte)
                    || byte == i32::from(b',')
                    || byte == i32::from(b'}')
                    || byte == i32::from(b']')
                {
                    break;
                }
                if byte == i32::from(b':') && blank_line_end_or_eof(at(peek, index + 1)) {
                    break;
                }
                if byte == i32::from(b' ') && is(peek, index + 1, b'#') {
                    break;
                }
                index += 1;
            }
            // `peek.substring(0, ei).trim()`, again JavaScript's
            // whitespace rather than Rust's.
            let raw = peek[..index].trim_matches(crate::js_space);
            if raw.is_empty() {
                None
            } else {
                Some(raw.to_string())
            }
        }
    }
}

// ---------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------

fn local_tag(src: &str, fwd: &str, cursor: (usize, usize, usize)) -> (usize, usize, usize) {
    let mut tag_end = 1;
    // A tab does not end a tag name, only a space or a line break does.
    while tag_end < fwd.len() && !is(fwd, tag_end, b' ') && !line_end(at(fwd, tag_end)) {
        tag_end += 1;
    }
    if is(fwd, tag_end, b' ') {
        tag_end += 1;
    }
    let mut next = advance_cols(src, cursor, tag_end);

    if next.0 < src.len() && line_end(at(src, next.0)) {
        // Standalone on its line: take the newline and the next indent.
        let mut standalone = true;
        let mut back = cursor.0;
        while back > 0 && !line_end(at(src, back - 1)) {
            back -= 1;
            if !blank(at(src, back)) {
                standalone = false;
                break;
            }
        }
        if standalone {
            let mut newline = next.0;
            if is(src, newline, b'\r') {
                newline += 1;
            }
            if is(src, newline, b'\n') {
                newline += 1;
            }
            let mut spaces = 0;
            while is(src, newline + spaces, b' ') {
                spaces += 1;
            }
            next = (newline + spaces, next.1 + 1, spaces);
        }
    }
    next
}

fn struct_tag(
    src: &str,
    fwd: &str,
    cursor: (usize, usize, usize),
) -> Result<(usize, usize, usize), Box<Act>> {
    let mut skip = 2;
    while skip < fwd.len() && !is(fwd, skip, b' ') && !is(fwd, skip, b'\n') {
        skip += 1;
    }
    while is(fwd, skip, b' ') {
        skip += 1;
    }

    let mut standalone = true;
    let mut indent = 0usize;
    let mut back = cursor.0;
    while back > 0 && !line_end(at(src, back - 1)) {
        back -= 1;
        if !blank(at(src, back)) {
            standalone = false;
            break;
        }
        indent += 1;
    }

    if standalone && line_end(at(fwd, skip)) {
        let mut newline = skip;
        if is(fwd, newline, b'\r') {
            newline += 1;
        }
        if is(fwd, newline, b'\n') {
            newline += 1;
        }
        let mut spaces = 0;
        while is(fwd, newline + spaces, b' ') {
            spaces += 1;
        }
        if spaces >= indent {
            let next = (cursor.0 + newline + spaces, cursor.1 + 1, spaces);
            let opener = at(src, next.0);
            if opener == i32::from(b'[') || opener == i32::from(b'{') {
                let (name_of, text) = if opener == i32::from(b'[') {
                    ("#OS", "[")
                } else {
                    ("#OB", "{")
                };
                let token = Tok::new(name_of, Value::String(text.to_string()), text, next);
                return Err(Box::new(Act::moved(
                    advance_cols(src, next, 1),
                    Some(token),
                )));
            }
            return Ok(next);
        }
    }

    let mut next = advance_cols(src, cursor, skip);

    // An inline comment after the tag is not the tagged value.
    {
        let mut index = next.0;
        while blank(at(src, index)) {
            index += 1;
        }
        if is(src, index, b'#') {
            while index < src.len() && !line_end(at(src, index)) {
                index += 1;
            }
            next = advance_cols(src, next, index - next.0);
        }
    }

    let opener = at(src, next.0);
    if opener == i32::from(b'[') || opener == i32::from(b'{') {
        let (name_of, text) = if opener == i32::from(b'[') {
            ("#OS", "[")
        } else {
            ("#OB", "{")
        };
        let token = Tok::new(name_of, Value::String(text.to_string()), text, next);
        return Err(Box::new(Act::moved(
            advance_cols(src, next, 1),
            Some(token),
        )));
    }
    Ok(next)
}

// ---------------------------------------------------------------------
// Document frames
// ---------------------------------------------------------------------

fn doc_marker(src: &str, fwd: &str, cursor: (usize, usize, usize)) -> Act {
    let ended = is(fwd, 0, b'.');
    let mut pos = 3;
    while blank(at(fwd, pos)) {
        pos += 1;
    }
    let inline = pos < fwd.len() && !line_end(at(fwd, pos)) && !is(fwd, pos, b'#');
    let next = if inline {
        advance_cols(src, cursor, pos)
    } else {
        let mut end = pos;
        while end < fwd.len() && !line_end(at(fwd, end)) {
            end += 1;
        }
        let mut rows = 0;
        if is(fwd, end, b'\r') {
            end += 1;
        }
        if is(fwd, end, b'\n') {
            end += 1;
            rows = 1;
        }
        (cursor.0 + end, cursor.1 + rows, 1)
    };
    let name = if ended { "#DE" } else { "#DS" };
    Act::moved(
        next,
        Some(Tok::new(name, Value::Undefined, &fwd[..3], next)),
    )
}

// ---------------------------------------------------------------------
// Quoted scalars
// ---------------------------------------------------------------------

/// Write out a high surrogate that never found its partner. A Rust
/// string holds Unicode scalars, and a lone surrogate is not one, so it
/// folds to the replacement character the way the engine's own string
/// lexer folds one. The canonical port keeps the code unit, which is the
/// entry `../DIVERGENCE.md` records.
fn flush_surrogate(pending: &mut Option<u32>, out: &mut String) {
    if pending.take().is_some() {
        out.push('\u{fffd}');
    }
}

/// Append one escaped code point. `String.fromCharCode` appends a UTF-16
/// CODE UNIT, so the canonical port's `\uD83D\uDE00` is one astral
/// character rather than two escapes; a high surrogate is therefore held
/// back until the next escape either completes the pair or does not.
fn push_code_point(code: u32, pending: &mut Option<u32>, out: &mut String) {
    if (0xd800..=0xdbff).contains(&code) {
        flush_surrogate(pending, out);
        *pending = Some(code);
    } else if (0xdc00..=0xdfff).contains(&code) {
        match pending.take() {
            Some(high) => out.push(
                char::from_u32(0x10000 + ((high - 0xd800) << 10) + (code - 0xdc00))
                    .unwrap_or('\u{fffd}'),
            ),
            None => out.push('\u{fffd}'),
        }
    } else {
        flush_surrogate(pending, out);
        out.push(char::from_u32(code).unwrap_or('\u{fffd}'));
    }
}

fn double_quoted(src: &str, fwd: &str, cursor: (usize, usize, usize)) -> Act {
    let mut index = 1;
    let mut value = String::new();
    // A high surrogate escape waiting for the low one that completes it.
    let mut pending = None;
    // Characters up to here came from escapes and are not trimmable.
    let mut escaped_upto = 0usize;
    while index < fwd.len() && !is(fwd, index, b'"') {
        if is(fwd, index, b'\\') {
            index += 1;
            let escape = at(fwd, index);
            let simple = |text: &str| Some(text.to_string());
            let decoded: Option<String> = match escape {
                e if e == i32::from(b'n') => simple("\n"),
                e if e == i32::from(b't') => simple("\t"),
                e if e == i32::from(b'r') => simple("\r"),
                e if e == i32::from(b'"') => simple("\""),
                e if e == i32::from(b'\\') => simple("\\"),
                e if e == i32::from(b'/') => simple("/"),
                e if e == i32::from(b'b') => simple("\u{8}"),
                e if e == i32::from(b'f') => simple("\u{c}"),
                e if e == i32::from(b'a') => simple("\u{7}"),
                e if e == i32::from(b'e') => simple("\u{1b}"),
                e if e == i32::from(b'v') => simple("\u{b}"),
                e if e == i32::from(b'0') => simple("\0"),
                e if e == i32::from(b'\t') => simple("\t"),
                e if e == i32::from(b' ') => simple(" "),
                e if e == i32::from(b'_') => simple("\u{a0}"),
                e if e == i32::from(b'N') => simple("\u{85}"),
                e if e == i32::from(b'L') => simple("\u{2028}"),
                e if e == i32::from(b'P') => simple("\u{2029}"),
                _ => None,
            };
            if let Some(text) = decoded {
                flush_surrogate(&mut pending, &mut value);
                value.push_str(&text);
                index += 1;
                escaped_upto = value.len();
                continue;
            }
            if escape == i32::from(b'x') || escape == i32::from(b'u') || escape == i32::from(b'U') {
                let width = if escape == i32::from(b'x') {
                    2
                } else if escape == i32::from(b'u') {
                    4
                } else {
                    8
                };
                // `fwd.substring(i + 1, i + 1 + width)`: a FIXED WIDTH
                // window, not a run of hexadecimal digits. It routinely
                // holds the closing quote or the rest of the line, and
                // the canonical handler reads a number out of it with
                // `parseInt`, which takes the longest prefix it can.
                //
                // The width counts UTF-16 CODE UNITS, so an astral
                // character inside the window fills two of them. The
                // cursor after it is `i + 1 + width` UNITS along, and
                // nothing else: it is not the length of the text the
                // window happened to cover.
                let from = index + 1;
                let window = take_units(fwd, from, width);
                let number = crate::parse_int_16(window.digits);
                if escape == i32::from(b'U') {
                    // `String.fromCodePoint` THROWS a RangeError on
                    // anything that is not a code point, and nothing in
                    // the canonical matcher catches it, so the token is
                    // never produced and the document is refused. That
                    // covers a value past U+10FFFF, a negative one, and
                    // a window with no hexadecimal digit in it at all.
                    match crate::from_code_point(number) {
                        Some(code) => push_code_point(code, &mut pending, &mut value),
                        None => return Act::refuse(cursor),
                    }
                } else {
                    // `String.fromCharCode` takes `ToUint16` of its
                    // argument instead, so it never throws and an
                    // unreadable window is a NUL.
                    push_code_point(crate::to_uint16(number), &mut pending, &mut value);
                }
                index = window.end;
                escaped_upto = value.len();
                if let Some(low) = window.split_low {
                    // The canonical cursor lands one unit into the
                    // character the window cut, on its LOW surrogate,
                    // and the scan appends that unit as a character of
                    // its own. Put through the same pairing an escape
                    // goes through, it completes a high surrogate the
                    // escape left pending, exactly as it does there.
                    push_code_point(low, &mut pending, &mut value);
                    index += fwd[index..].chars().next().map_or(0, char::len_utf8);
                }
                continue;
            }
            if line_end(escape) {
                // An escaped line break joins the lines with nothing.
                if escape == i32::from(b'\r') && is(fwd, index + 1, b'\n') {
                    index += 1;
                }
                index += 1;
                while blank(at(fwd, index)) {
                    index += 1;
                }
                continue;
            }
            if escape < 0 {
                // A backslash as the last character of the source: there
                // is nothing to escape, so the scan ends with the value it
                // has. The canonical port once appended the nine letters
                // of `undefined` here, text the input never held, and was
                // repaired under ADR-13 rather than copied.
                flush_surrogate(&mut pending, &mut value);
                index += 1;
                continue;
            }
            let character = fwd[index..].chars().next().unwrap_or('\\');
            flush_surrogate(&mut pending, &mut value);
            value.push(character);
            index += character.len_utf8();
            continue;
        }
        if line_end(at(fwd, index)) {
            // Flow folding: trim what was written literally, then fold.
            flush_surrogate(&mut pending, &mut value);
            let mut trim_to = value.len();
            while trim_to > escaped_upto
                && matches!(value.as_bytes().get(trim_to - 1), Some(b' ' | b'\t'))
            {
                trim_to -= 1;
            }
            value.truncate(trim_to);
            let mut empty_lines = 0;
            while index < fwd.len() && line_end(at(fwd, index)) {
                if is(fwd, index, b'\r') {
                    index += 1;
                }
                if is(fwd, index, b'\n') {
                    index += 1;
                }
                empty_lines += 1;
                while blank(at(fwd, index)) {
                    index += 1;
                }
            }
            if empty_lines > 1 {
                for _ in 1..empty_lines {
                    value.push('\n');
                }
            } else {
                value.push(' ');
            }
            continue;
        }
        let character = fwd[index..].chars().next().unwrap_or('"');
        flush_surrogate(&mut pending, &mut value);
        value.push(character);
        index += character.len_utf8();
    }
    flush_surrogate(&mut pending, &mut value);
    if is(fwd, index, b'"') {
        index += 1;
    }
    let source = fwd[..index.min(fwd.len())].to_string();
    let next = advance_cols(src, cursor, index);
    Act::moved(
        next,
        Some(Tok::new("#ST", Value::String(value), source, cursor)),
    )
}

fn single_quoted(src: &str, fwd: &str, cursor: (usize, usize, usize)) -> Act {
    let mut index = 1;
    let mut value = String::new();
    while index < fwd.len() {
        if is(fwd, index, b'\'') {
            if is(fwd, index + 1, b'\'') {
                value.push('\'');
                index += 2;
                continue;
            }
            index += 1;
            break;
        }
        if line_end(at(fwd, index)) {
            while matches!(value.as_bytes().last(), Some(b' ' | b'\t')) {
                value.pop();
            }
            let mut empty_lines = 0;
            while index < fwd.len() && line_end(at(fwd, index)) {
                if is(fwd, index, b'\r') {
                    index += 1;
                }
                if is(fwd, index, b'\n') {
                    index += 1;
                }
                empty_lines += 1;
                while blank(at(fwd, index)) {
                    index += 1;
                }
            }
            if empty_lines > 1 {
                for _ in 1..empty_lines {
                    value.push('\n');
                }
            } else {
                value.push(' ');
            }
            continue;
        }
        let character = fwd[index..].chars().next().unwrap_or('\'');
        value.push(character);
        index += character.len_utf8();
    }
    let source = fwd[..index.min(fwd.len())].to_string();
    let next = advance_cols(src, cursor, index);
    Act::moved(
        next,
        Some(Tok::new("#ST", Value::String(value), source, cursor)),
    )
}

/// Whether the nearest significant character before `offset`, skipping
/// whitespace and line comments, closes a quoted scalar. That is what
/// makes a bare `:` a key separator in JSON-compatible flow syntax.
fn quoted_before(src: &str, offset: usize) -> bool {
    let mut previous = offset as isize - 1;
    while previous >= 0 {
        let byte = at(src, previous as usize);
        if blank(byte) || line_end(byte) {
            previous -= 1;
            continue;
        }
        let mut line_start = previous as usize;
        while line_start > 0 && !line_end(at(src, line_start - 1)) {
            line_start -= 1;
        }
        // A `#` inside a quoted scalar is literal, not a comment start.
        let mut hash_at: isize = -1;
        let mut quote: i32 = 0;
        let mut index = line_start;
        while index <= previous as usize {
            let character = at(src, index);
            if quote != 0 {
                if quote == i32::from(b'"') && character == i32::from(b'\\') {
                    index += 2;
                    continue;
                }
                if character == quote {
                    if quote == i32::from(b'\'') && is(src, index + 1, b'\'') {
                        index += 2;
                        continue;
                    }
                    quote = 0;
                }
                index += 1;
                continue;
            }
            if character == i32::from(b'"') || character == i32::from(b'\'') {
                quote = character;
                index += 1;
                continue;
            }
            if character == i32::from(b'#') && (index == line_start || blank(at(src, index - 1))) {
                hash_at = index as isize;
                break;
            }
            index += 1;
        }
        if hash_at >= 0 {
            previous = hash_at - 1;
            continue;
        }
        break;
    }
    previous >= 0 && (is(src, previous as usize, b'"') || is(src, previous as usize, b'\''))
}

// ---------------------------------------------------------------------
// Digits that are not numbers
// ---------------------------------------------------------------------

/// A value that starts with a digit but carries an embedded colon
/// (`20:03:20`), a comma in block context (`1,2,3`) or trailing words
/// (`64 characters, hexadecimal.`) is a plain scalar, and the engine's
/// number matcher would otherwise take only the digits.
fn numeric_plain(
    src: &str,
    fwd: &str,
    cursor: (usize, usize, usize),
    context: &mut Context,
) -> Option<Act> {
    let in_flow = state::flow_depth_at(context, src, cursor.0) > 0;
    let mut embedded_colon = false;
    let mut trailing_text = false;
    let mut block_comma = false;
    let mut index = 1;
    while index < fwd.len() && !line_end(at(fwd, index)) {
        if is(fwd, index, b':') && !blank_line_end_or_eof(at(fwd, index + 1)) {
            embedded_colon = true;
            break;
        }
        // A comma is NOT a separator in block context. Flow indicators
        // only indicate inside a flow collection, so `example: 1,2,3` is
        // the plain scalar `1,2,3` rather than a number, a separator and
        // two more numbers.
        //
        // The scan CONTINUES rather than breaking, so a scalar that also
        // has a space with text after it (`12, hexadecimal`) still reaches
        // the trailing-text branch, which handles the spaces and the
        // multiline continuation this scan cannot.
        if is(fwd, index, b',') {
            if in_flow {
                break;
            }
            block_comma = true;
            index += 1;
            continue;
        }
        if blank(at(fwd, index)) {
            let mut after = index;
            while blank(at(fwd, after)) {
                after += 1;
            }
            let following = at(fwd, after);
            if after < fwd.len()
                && !line_end(following)
                && following != i32::from(b'#')
                && following != i32::from(b':')
            {
                trailing_text = true;
            }
            break;
        }
        index += 1;
    }

    // TRAILING TEXT FIRST. A scalar can be both (`12, hexadecimal`), and
    // the token scan below stops at the first space, which would truncate
    // it to `12,`. The plain-scalar handler takes the whole scalar,
    // continuation lines included.
    if trailing_text {
        // The canonical matcher sets a flag here and lets the text check
        // pick the value up once the number matcher has been skipped.
        // Running the plain-scalar handler directly reaches the same
        // token, and cannot leave a flag set for the next parse.
        return Some(text::plain_scalar(src, fwd, cursor, context));
    }

    if embedded_colon || block_comma {
        let mut end = 0;
        while end < fwd.len() && !blank_line_end_or_eof(at(fwd, end)) {
            end += 1;
        }
        let text = fwd[..end].to_string();
        let next = advance_cols(src, cursor, end);
        return Some(Act::moved(
            next,
            Some(Tok::new("#TX", Value::String(text.clone()), text, cursor)),
        ));
    }
    None
}

// ---------------------------------------------------------------------
// Newlines
// ---------------------------------------------------------------------

/// Inside a flow collection a newline is whitespace. The cursor has to
/// land on a valid column, and any flow punctuation that follows is
/// emitted here: the fixed-token matcher never gets a clean look at the
/// advanced position and would read `]` as a bad character.
fn flow_newline(
    src: &str,
    fwd: &str,
    cursor: (usize, usize, usize),
) -> Result<(usize, usize, usize), Box<Act>> {
    let mut pos = 0;
    let mut rows = 0;
    let mut last_newline: isize = -1;
    while pos < fwd.len() && (line_end(at(fwd, pos)) || blank(at(fwd, pos))) {
        if is(fwd, pos, b'\n') {
            rows += 1;
            last_newline = pos as isize;
        }
        pos += 1;
    }
    if is(fwd, pos, b'#') {
        while pos < fwd.len() && !line_end(at(fwd, pos)) {
            pos += 1;
        }
    }
    let column = if last_newline >= 0 {
        fwd[last_newline as usize..pos].chars().count()
    } else {
        cursor.2 + fwd[..pos].chars().count()
    };
    let next = (cursor.0 + pos, cursor.1 + rows, column);

    let following = at(fwd, pos);
    let punctuation = if following == i32::from(b'[') {
        Some(("#OS", "["))
    } else if following == i32::from(b'{') {
        Some(("#OB", "{"))
    } else if following == i32::from(b']') {
        Some(("#CS", "]"))
    } else if following == i32::from(b'}') {
        Some(("#CB", "}"))
    } else if following == i32::from(b',') {
        Some(("#CA", ","))
    } else {
        None
    };
    if let Some((name, text)) = punctuation {
        let token = Tok::new(name, Value::String(text.to_string()), text, next);
        return Err(Box::new(Act::moved(
            advance_cols(src, next, 1),
            Some(token),
        )));
    }
    Ok(next)
}

/// In block context a newline carries the indentation the grammar runs
/// on. Blank, comment-only, tab-only and anchor-only lines are consumed
/// so the indent reported is that of the next line with content.
fn block_newline(
    _src: &str,
    fwd: &str,
    cursor: (usize, usize, usize),
    context: &mut Context,
) -> Result<(usize, usize, usize), Box<Act>> {
    let mut pos = 0;
    let mut spaces = 0;
    let mut rows = 0;
    while pos < fwd.len() {
        if is(fwd, pos, b'\r') && is(fwd, pos + 1, b'\n') {
            pos += 2;
            rows += 1;
        } else if is(fwd, pos, b'\n') {
            pos += 1;
            rows += 1;
        } else {
            break;
        }
        spaces = 0;
        while is(fwd, pos, b' ') {
            pos += 1;
            spaces += 1;
        }
        if is(fwd, pos, b'#') {
            while pos < fwd.len() && !line_end(at(fwd, pos)) {
                pos += 1;
            }
            continue;
        }
        if is(fwd, pos, b'\t') {
            let mut tab = pos;
            while blank(at(fwd, tab)) {
                tab += 1;
            }
            if tab >= fwd.len() || line_end(at(fwd, tab)) {
                pos = tab;
                continue;
            }
        }
        if is(fwd, pos, b'&') {
            let mut end = pos + 1;
            while end < fwd.len() && !blank_line_end_or_eof(at(fwd, end)) {
                end += 1;
            }
            let mut after = end;
            while blank(at(fwd, after)) {
                after += 1;
            }
            if after >= fwd.len() || line_end(at(fwd, after)) || is(fwd, after, b'#') {
                let mut pending = indexmap::IndexMap::new();
                pending.insert(
                    "n".to_string(),
                    Value::String(fwd[pos + 1..end].to_string()),
                );
                pending.insert("i".to_string(), Value::Bool(false));
                state::list_push(context, state::PENDING_ANCHORS, Value::object(pending));
                while after < fwd.len() && !line_end(at(fwd, after)) {
                    after += 1;
                }
                pos = after;
                continue;
            }
        }
    }

    // Trailing newlines: the source is exhausted.
    if pos >= fwd.len() {
        let next = (cursor.0 + pos, cursor.1 + rows, spaces + 1);
        return Err(Box::new(Act::moved(
            next,
            Some(Tok::new("#ZZ", Value::Undefined, "", next)),
        )));
    }

    // A document frame or a directive on the next line takes no indent.
    if spaces == 0 && (is_doc_marker(fwd, pos) || is(fwd, pos, b'%')) {
        return Ok((cursor.0 + pos, cursor.1 + rows, 0));
    }

    // Neither does a flow collection or a quoted scalar at column 0:
    // there is no block for an indent to describe.
    if spaces == 0
        && (is(fwd, pos, b'{') || is(fwd, pos, b'[') || is(fwd, pos, b'"') || is(fwd, pos, b'\''))
    {
        return Ok((cursor.0 + pos, cursor.1 + rows, 0));
    }

    let token = Tok::new("#IN", Value::Number(spaces as f64), &fwd[..pos], cursor);
    Err(Box::new(Act::moved(
        (cursor.0 + pos, cursor.1 + rows, spaces + 1),
        Some(token),
    )))
}

// ---------------------------------------------------------------------
// Explicit keys
// ---------------------------------------------------------------------

/// `? key` in block context, with the several shapes the canonical
/// handler accepts: a key with no value, a key whose `:` is on the next
/// line, a multi-line plain key, a block-scalar key, and a value that is
/// itself a block mapping or sequence.
#[allow(clippy::too_many_lines)]
fn explicit_key(src: &str, fwd: &str, cursor: (usize, usize, usize), context: &mut Context) -> Act {
    let start = if blank(at(fwd, 1)) { 2 } else { 1 };
    let mut key_end = start;
    while key_end < fwd.len() && !line_end(at(fwd, key_end)) {
        if is(fwd, key_end, b' ') && is(fwd, key_end + 1, b'#') {
            break;
        }
        key_end += 1;
    }
    let mut key = trim_end(&fwd[start..key_end]).to_string();

    // A `!!type` prefix on the key is stripped and applied.
    if let Some(stripped) = strip_key_tag(&key) {
        key = stripped;
    }

    let mut consumed = key_end;
    while consumed < fwd.len() && !line_end(at(fwd, consumed)) {
        consumed += 1;
    }
    let mut before_newline = consumed;
    if is(fwd, consumed, b'\r') {
        consumed += 1;
    }
    if is(fwd, consumed, b'\n') {
        consumed += 1;
    }

    // The indent of the `?` itself bounds a continuation line.
    let mut question_indent = 0;
    {
        let mut line_start = cursor.0;
        while line_start > 0 && !line_end(at(src, line_start - 1)) {
            line_start -= 1;
        }
        while line_start < cursor.0 && is(src, line_start, b' ') {
            question_indent += 1;
            line_start += 1;
        }
    }

    let mut extra_rows = 0;

    if let Some((folded, chomp, explicit_indent)) = block_scalar_key(&key) {
        let mut lines: Vec<String> = Vec::new();
        let mut content_indent = 0usize;
        while consumed < fwd.len() {
            let mut line_indent = 0;
            while is(fwd, consumed + line_indent, b' ') {
                line_indent += 1;
            }
            let after = consumed + line_indent;
            if after >= fwd.len() || line_end(at(fwd, after)) {
                lines.push(String::new());
                consumed = after;
                if is(fwd, consumed, b'\r') {
                    consumed += 1;
                }
                if is(fwd, consumed, b'\n') {
                    consumed += 1;
                }
                extra_rows += 1;
                continue;
            }
            if content_indent == 0 {
                content_indent = if explicit_indent > 0 {
                    question_indent + explicit_indent
                } else {
                    line_indent
                };
            }
            if line_indent < content_indent {
                break;
            }
            let mut line_end_index = after;
            while line_end_index < fwd.len() && !line_end(at(fwd, line_end_index)) {
                line_end_index += 1;
            }
            lines.push(fwd[consumed + content_indent..line_end_index].to_string());
            consumed = line_end_index;
            if is(fwd, consumed, b'\r') {
                consumed += 1;
            }
            if is(fwd, consumed, b'\n') {
                consumed += 1;
            }
            extra_rows += 1;
        }
        if chomp != '+' {
            while lines.last().is_some_and(String::is_empty) {
                lines.pop();
            }
        }
        key = if folded {
            lines.join(" ") + "\n"
        } else {
            lines.join("\n") + "\n"
        };
        if chomp == '-' && key.ends_with('\n') {
            key.pop();
        }
    } else {
        // A plain key may run over several lines.
        while consumed < fwd.len() {
            let mut line_indent = 0;
            while is(fwd, consumed + line_indent, b' ') {
                line_indent += 1;
            }
            let mut after = consumed + line_indent;
            if after < fwd.len() && is(fwd, after, b'#') {
                while after < fwd.len() && !line_end(at(fwd, after)) {
                    after += 1;
                }
                before_newline = after;
                if is(fwd, after, b'\r') {
                    after += 1;
                }
                if is(fwd, after, b'\n') {
                    after += 1;
                }
                extra_rows += 1;
                consumed = after;
                continue;
            }
            if line_indent > question_indent
                && !is(fwd, after, b':')
                && !is(fwd, after, b'?')
                && !is(fwd, after, b'-')
            {
                let mut continuation_end = after;
                while continuation_end < fwd.len() && !line_end(at(fwd, continuation_end)) {
                    if is(fwd, continuation_end, b' ') && is(fwd, continuation_end + 1, b'#') {
                        break;
                    }
                    continuation_end += 1;
                }
                let text = trim_end(&fwd[after..continuation_end]);
                if !text.is_empty() {
                    key.push(' ');
                    key.push_str(text);
                }
                consumed = continuation_end;
                before_newline = consumed;
                if is(fwd, consumed, b'\r') {
                    consumed += 1;
                }
                if is(fwd, consumed, b'\n') {
                    consumed += 1;
                }
                extra_rows += 1;
                continue;
            }
            break;
        }
    }

    // Does a `:` follow on the next non-comment line?
    let mut has_value = false;
    let mut value_consumed = consumed;
    {
        let mut index = consumed;
        while is(fwd, index, b' ') {
            index += 1;
        }
        if is(fwd, index, b':') && blank_line_end_or_eof(at(fwd, index + 1)) {
            has_value = true;
            value_consumed = index + 1;
            if blank(at(fwd, value_consumed)) {
                value_consumed += 1;
            }
        }
    }

    let source = fwd[..if has_value { consumed } else { key_end }].to_string();

    if has_value {
        let indent = value_consumed - consumed;
        let next = (
            cursor.0 + value_consumed,
            cursor.1 + 1 + extra_rows,
            indent + 1,
        );
        let following = at(fwd, value_consumed);
        let has_inline = following >= 0 && !line_end(following);
        let mut needs_indent = false;
        if has_inline {
            let quoted_or_flow = following == i32::from(b'"')
                || following == i32::from(b'\'')
                || following == i32::from(b'[')
                || following == i32::from(b'{')
                || following == i32::from(b'!');
            if !quoted_or_flow {
                let mut line_end_index = value_consumed;
                while line_end_index < fwd.len() && !line_end(at(fwd, line_end_index)) {
                    line_end_index += 1;
                }
                for index in value_consumed..line_end_index {
                    if is(fwd, index, b':') {
                        let after = at(fwd, index + 1);
                        if blank_line_end_or_eof(after) || index + 1 == line_end_index {
                            needs_indent = true;
                            break;
                        }
                    }
                }
                if !needs_indent
                    && following == i32::from(b'-')
                    && blank(at(fwd, value_consumed + 1))
                {
                    needs_indent = true;
                }
            }
        }
        if needs_indent {
            // A block mapping or sequence inline after `: `: the colon and
            // an indent token together establish the block's context.
            state::list_push(
                context,
                state::PENDING_TOKENS,
                encode_token(&Tok::new("#CL", Value::Number(1.0), ": ", next)),
            );
            state::list_push(
                context,
                state::PENDING_TOKENS,
                encode_token(&Tok::new("#IN", Value::Number(indent as f64), "", next)),
            );
        } else {
            state::set_flag(context, state::PENDING_CL, true);
        }
        return Act::moved(
            next,
            Some(Tok::new("#TX", Value::String(key), source, next)),
        );
    }

    // No `:` follows: the key takes a null value, and the newline is left
    // for the indent handler so a following pair still opens its map.
    let next = advance_cols(src, cursor, before_newline);
    state::list_push(
        context,
        state::PENDING_TOKENS,
        encode_token(&Tok::new("#CL", Value::Number(1.0), ": ", next)),
    );
    state::list_push(
        context,
        state::PENDING_TOKENS,
        encode_token(&Tok::new("#VL", Value::Null, "", next)),
    );
    Act::moved(
        next,
        Some(Tok::new("#TX", Value::String(key), source, next)),
    )
}

/// `!!name rest` on an explicit key: the tag is stripped and `rest` kept.
/// The canonical pattern is `^!!(\w+)\s+(.*)$`, and JavaScript's `\w` is
/// ASCII, so `is_alphanumeric` would strip a tag the canonical port keeps
/// whole: `? !!\u{e9} value` has no tag name at all there.
fn strip_key_tag(key: &str) -> Option<String> {
    let rest = key.strip_prefix("!!")?;
    let name_len = rest
        .find(|character: char| !character.is_ascii_alphanumeric() && character != '_')
        .unwrap_or(rest.len());
    if name_len == 0 {
        return None;
    }
    let tail = &rest[name_len..];
    let trimmed = tail.trim_start_matches(crate::js_space);
    if trimmed.len() == tail.len() {
        return None;
    }
    Some(trimmed.to_string())
}

/// `|`, `>` and their chomping and indent indicators, used as a key.
fn block_scalar_key(key: &str) -> Option<(bool, char, usize)> {
    let mut chars = key.chars();
    let indicator = chars.next()?;
    if indicator != '|' && indicator != '>' {
        return None;
    }
    let mut chomp = ' ';
    let mut indent = 0usize;
    let mut next = chars.next();
    if matches!(next, Some('+' | '-')) {
        chomp = next.unwrap_or(' ');
        next = chars.next();
    }
    if let Some(digit) = next {
        if digit.is_ascii_digit() {
            indent = digit.to_digit(10).unwrap_or(0) as usize;
            next = chars.next();
        } else {
            return None;
        }
    }
    if next.is_some() {
        return None;
    }
    Some((indicator == '>', chomp, indent))
}
