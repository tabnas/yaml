/* Copyright (c) 2021-2026 Richard Rodger, MIT License */

//! Scalars: block scalars, plain scalars and typed tags.
//!
//! The canonical implementation puts the first two in `options.text.check`,
//! the hook the engine's text matcher runs before it reads a word. This
//! engine's check hook sees the lexer alone, and a YAML plain scalar needs
//! the per-parse flow-depth cache, so the same work is registered as a
//! second custom matcher in the band between the number matcher and the
//! text matcher: the position `text.check` occupies, with the context the
//! hook cannot supply.
//!
//! The typed-tag handler is called from [`crate::lex`] rather than from
//! here, because the canonical matcher reaches `!!str` and friends before
//! the text check ever sees them.

use tabnas::{Context, Lexer, Rule, Token, Value};

use crate::lex::{
    advance_cols, apply, at, blank, blank_line_end_or_eof, is, is_doc_marker, is_doc_marker_no_tab,
    js_substring, js_substring_less_one_unit, line_end, trim_end, Act, Tok,
};
use crate::state;

/// The reference name the grammar's `options.lex.match` entry uses.
pub(crate) const MATCHER: &str = "@yaml-text";

/// The matcher that stands in for `options.text.check`.
pub(crate) fn yaml_text(
    lexer: &mut Lexer<'_>,
    _rule: &mut Rule,
    context: &mut Context,
) -> Option<Token> {
    let (row, column) = crate::lex::virtual_point_for(lexer, context);
    let start = lexer.point().site.si;
    let act = {
        let source = lexer.source();
        check(source, (start, row, column), context)
    };
    let act = act?;
    apply(lexer, context, act)
}

/// The body of `options.text.check`: a block scalar, then the characters
/// other matchers own, then a plain scalar.
fn check(src: &str, cursor: (usize, usize, usize), context: &mut Context) -> Option<Act> {
    let fwd = &src[cursor.0.min(src.len())..];
    if fwd.is_empty() {
        return None;
    }
    let head = at(fwd, 0);

    if head == i32::from(b'|') || head == i32::from(b'>') {
        if let Some(act) = block_scalar(src, fwd, cursor) {
            return Some(act);
        }
        // A block scalar indicator followed by text on the same line
        // (`a: > x`) is invalid YAML. The canonical handler does not
        // refuse it: the branch simply falls through to plain-scalar
        // handling, which yields `{"a": "> x"}`.
        return Some(plain_scalar(src, fwd, cursor, context));
    }

    // Characters another matcher owns, and the anchor, alias and tag
    // markers the YAML matcher has already had its chance at.
    for owned in [
        b'{', b'}', b'[', b']', b',', b'#', b'\n', b'\r', b'"', b'\'', b'*', b'&', b'!',
    ] {
        if head == i32::from(owned) {
            return None;
        }
    }

    // A colon that separates a key from a value is not text.
    if head == i32::from(b':') && blank_line_end_or_eof(at(fwd, 1)) {
        return None;
    }

    // The number matcher runs between the YAML matcher and this check,
    // and the engine gates it on the character the lexer call started
    // on. When the YAML matcher moved the cursor past an anchor or a tag
    // and produced nothing, that character is the marker, so the number
    // matcher never sees the value, and a plain scalar would swallow the
    // next line as a continuation where the canonical parser stops at
    // the number. Stand in for it here, in the position it would have
    // run.
    if state::flag(context, state::MOVED) {
        if let Some(act) = number_run(src, fwd, cursor) {
            return Some(act);
        }
    }

    Some(plain_scalar(src, fwd, cursor, context))
}

/// The token the engine's number matcher would have produced: the run up
/// to the next delimiter, when the whole of it reads as a number.
fn number_run(src: &str, fwd: &str, cursor: (usize, usize, usize)) -> Option<Act> {
    let head = at(fwd, 0);
    let numeric = head == i32::from(b'-')
        || head == i32::from(b'+')
        || head == i32::from(b'.')
        || (head >= i32::from(b'0') && head <= i32::from(b'9'));
    if !numeric {
        return None;
    }
    let mut end = 0;
    while end < fwd.len() {
        let byte = at(fwd, end);
        if blank_line_end_or_eof(byte)
            || byte == i32::from(b',')
            || byte == i32::from(b'[')
            || byte == i32::from(b']')
            || byte == i32::from(b'{')
            || byte == i32::from(b'}')
            || byte == i32::from(b':')
            || byte == i32::from(b'#')
        {
            break;
        }
        end += 1;
    }
    let run = &fwd[..end];
    let number = crate::js_to_number(run)?;
    if !number.is_finite() {
        return None;
    }
    let next = advance_cols(src, cursor, end);
    Some(Act::moved(
        next,
        Some(Tok::new("#NR", Value::Number(number), run, cursor)),
    ))
}

// ---------------------------------------------------------------------
// Block scalars
// ---------------------------------------------------------------------

#[derive(Clone, Copy, PartialEq, Eq)]
enum Chomp {
    Clip,
    Strip,
    Keep,
}

/// A literal (`|`) or folded (`>`) block scalar. `None` means the
/// indicator is not one, and the caller falls through to a plain scalar.
#[allow(clippy::too_many_lines)]
fn block_scalar(src: &str, fwd: &str, cursor: (usize, usize, usize)) -> Option<Act> {
    let fold = is(fwd, 0, b'>');
    let mut chomp = Chomp::Clip;
    let mut explicit_indent = 0usize;
    let mut idx = 1;
    // The chomping and indentation indicators, in either order.
    for _ in 0..2 {
        let byte = at(fwd, idx);
        if byte == i32::from(b'+') {
            chomp = Chomp::Keep;
            idx += 1;
        } else if byte == i32::from(b'-') {
            chomp = Chomp::Strip;
            idx += 1;
        } else if (i32::from(b'1')..=i32::from(b'9')).contains(&byte) {
            explicit_indent = (byte - i32::from(b'0')) as usize;
            idx += 1;
        }
    }

    while is(fwd, idx, b' ') {
        idx += 1;
    }
    if is(fwd, idx, b'#') {
        while idx < fwd.len() && !line_end(at(fwd, idx)) {
            idx += 1;
        }
    }
    if !line_end(at(fwd, idx)) && idx < fwd.len() {
        // Text on the indicator's own line: not a block scalar.
        return None;
    }

    if is(fwd, idx, b'\r') {
        idx += 1;
    }
    if is(fwd, idx, b'\n') {
        idx += 1;
    }

    // The block's indent: the first content line's, unless given.
    let mut block_indent = 0usize;
    if explicit_indent == 0 {
        let mut probe = idx;
        while probe < fwd.len() {
            let mut spaces = 0;
            while is(fwd, probe + spaces, b' ') {
                spaces += 1;
            }
            let after = probe + spaces;
            if after >= fwd.len() || line_end(at(fwd, after)) {
                probe = after;
                if is(fwd, probe, b'\r') {
                    probe += 1;
                }
                if is(fwd, probe, b'\n') {
                    probe += 1;
                }
                continue;
            }
            block_indent = spaces;
            break;
        }
    }

    // The indent of the line the indicator sits on. Content must be more
    // indented than that, or the scalar is empty. A document's root node
    // has parent indent -1, so column 0 content under a column 0
    // indicator, or one after `---`, is not empty.
    let mut containing_indent = 0usize;
    let is_doc_start;
    let is_doc_root;
    {
        let mut index = cursor.0;
        while index > 0 && !line_end(at(src, index - 1)) {
            index -= 1;
        }
        let line_start = index;
        while index < cursor.0 && is(src, index, b' ') {
            containing_indent += 1;
            index += 1;
        }
        is_doc_start = is(src, line_start, b'-')
            && is(src, line_start + 1, b'-')
            && is(src, line_start + 2, b'-');
        is_doc_root = containing_indent == 0 && index == cursor.0;
    }

    if explicit_indent > 0 {
        let mut index = cursor.0;
        while index > 0 && !line_end(at(src, index - 1)) {
            index -= 1;
        }
        let mut key_column = containing_indent;
        let mut has_colon = false;
        for probe in index + containing_indent..cursor.0 {
            if is(src, probe, b':') && blank(at(src, probe + 1)) {
                has_colon = true;
                break;
            }
        }
        if has_colon {
            // `key: |2`: each `- ` before the key adds to the indent.
            let mut scan = index + containing_indent;
            while scan < cursor.0 && is(src, scan, b'-') && blank(at(src, scan + 1)) {
                key_column += 2;
                scan += 2;
                while scan < cursor.0 && is(src, scan, b' ') {
                    key_column += 1;
                    scan += 1;
                }
            }
            block_indent = key_column + explicit_indent;
        } else {
            // The indicator is alone on its line: the parent mapping key
            // is on the line before, and its indent is the base.
            let mut parent_indent = 0usize;
            let mut search = index as isize - 1;
            if search > 0 {
                if is(src, search as usize, b'\n') {
                    search -= 1;
                }
                if search >= 0 && is(src, search as usize, b'\r') {
                    search -= 1;
                }
                let previous_end = (search + 1).max(0) as usize;
                let mut previous_start = search.max(0) as usize;
                while previous_start > 0 && !line_end(at(src, previous_start - 1)) {
                    previous_start -= 1;
                }
                for probe in previous_start..previous_end {
                    if is(src, probe, b':')
                        && (blank(at(src, probe + 1))
                            || line_end(at(src, probe + 1))
                            || probe + 1 >= previous_end)
                    {
                        parent_indent = 0;
                        let mut leading = previous_start;
                        while leading < previous_end && is(src, leading, b' ') {
                            parent_indent += 1;
                            leading += 1;
                        }
                        break;
                    }
                }
            }
            block_indent = parent_indent + explicit_indent;
            containing_indent = parent_indent;
        }
    }

    if block_indent <= containing_indent && !is_doc_start && !is_doc_root && idx < fwd.len() {
        // The content is not indented enough: the scalar is empty. With
        // keep chomping the trailing blank lines still count.
        let mut value = String::new();
        let mut end = idx;
        if chomp == Chomp::Keep {
            let mut blanks = 0;
            while end < fwd.len() {
                if is(fwd, end, b'\n') {
                    blanks += 1;
                    end += 1;
                } else if is(fwd, end, b'\r') {
                    end += 1;
                    if is(fwd, end, b'\n') {
                        end += 1;
                    }
                    blanks += 1;
                } else {
                    break;
                }
            }
            value = "\n".repeat(blanks.max(1));
        }
        let source = fwd[..end].to_string();
        return Some(Act::moved(
            (cursor.0 + end, cursor.1 + 1, 0),
            Some(Tok::new("#TX", Value::String(value), source, cursor)),
        ));
    }

    // The content lines, with the block indent stripped.
    let mut lines: Vec<String> = Vec::new();
    let mut pos = idx;
    let mut rows = 1usize;
    let mut last_newline = idx;
    while pos < fwd.len() {
        let mut line_indent = 0;
        while is(fwd, pos + line_indent, b' ') {
            line_indent += 1;
        }
        let after = pos + line_indent;
        if after >= fwd.len() || line_end(at(fwd, after)) {
            // A blank line keeps whatever sits past the block indent.
            if line_indent > block_indent {
                lines.push(fwd[pos + block_indent..after].to_string());
            } else {
                lines.push(String::new());
            }
            last_newline = after;
            pos = after;
            if is(fwd, pos, b'\r') {
                pos += 1;
            }
            if is(fwd, pos, b'\n') {
                pos += 1;
            }
            rows += 1;
            continue;
        }
        if line_indent < block_indent {
            break;
        }
        // `fwd[pos+3] === '\n' || '\r' || ' ' || undefined`, spelled
        // out and WITHOUT a tab, unlike the marker test everywhere else.
        if line_indent == 0 && is_doc_marker_no_tab(fwd, pos) {
            break;
        }
        let line_start = pos + block_indent;
        let mut line_stop = line_start;
        while line_stop < fwd.len() && !line_end(at(fwd, line_stop)) {
            line_stop += 1;
        }
        lines.push(fwd[line_start..line_stop].to_string());
        last_newline = line_stop;
        pos = line_stop;
        if is(fwd, pos, b'\r') {
            pos += 1;
        }
        if is(fwd, pos, b'\n') {
            pos += 1;
        }
        rows += 1;
    }

    let mut value = if fold {
        fold_lines(&lines)
    } else {
        lines.join("\n")
    };

    if lines.is_empty() {
        value = String::new();
    } else {
        match chomp {
            Chomp::Strip => {
                while value.ends_with('\n') {
                    value.pop();
                }
            }
            Chomp::Clip => {
                while value.ends_with('\n') {
                    value.pop();
                }
                value.push('\n');
            }
            Chomp::Keep => value.push('\n'),
        }
    }

    // When the block ended because the indent dropped, and what follows
    // is not a document marker, leave the final newline for the indent
    // handler so the grammar still sees where the next line starts.
    let mut end_pos = pos;
    let mut end_rows = rows;
    if pos < fwd.len() && pos > last_newline {
        let mut next_indent = 0;
        let mut probe = pos;
        while is(fwd, probe, b' ') {
            next_indent += 1;
            probe += 1;
        }
        let marker = next_indent == 0
            && ((is(fwd, probe, b'-') && is(fwd, probe + 1, b'-') && is(fwd, probe + 2, b'-'))
                || (is(fwd, probe, b'.') && is(fwd, probe + 1, b'.') && is(fwd, probe + 2, b'.')));
        if !marker {
            end_pos = last_newline;
            end_rows = rows - 1;
        }
    }

    let source = fwd[..end_pos].to_string();
    Some(Act::moved(
        (cursor.0 + end_pos, cursor.1 + end_rows, 0),
        Some(Tok::new("#TX", Value::String(value), source, cursor)),
    ))
}

/// Folded-scalar line joining: a run of normal lines becomes one line
/// joined by spaces, blank lines become newlines, and a more-indented
/// line is kept verbatim with newlines around it.
fn fold_lines(lines: &[String]) -> String {
    let mut result = String::new();
    let mut previous_normal = false;
    let mut pending_empty = 0usize;
    for line in lines {
        let more_indented = line.starts_with(' ') || line.starts_with('\t');
        if line.is_empty() {
            pending_empty += 1;
        } else if more_indented {
            if previous_normal && !result.is_empty() {
                result.push('\n');
            }
            for _ in 0..pending_empty {
                result.push('\n');
            }
            pending_empty = 0;
            if !result.is_empty() && !result.ends_with('\n') {
                result.push('\n');
            }
            result.push_str(line);
            result.push('\n');
            previous_normal = false;
        } else {
            if pending_empty > 0 {
                if previous_normal && !result.is_empty() {
                    result.push('\n');
                    for _ in 1..pending_empty {
                        result.push('\n');
                    }
                } else {
                    for _ in 0..pending_empty {
                        result.push('\n');
                    }
                }
                pending_empty = 0;
            }
            if previous_normal && !result.is_empty() && !result.ends_with('\n') {
                result.push(' ');
            }
            result.push_str(line);
            previous_normal = true;
        }
    }
    for _ in 0..pending_empty {
        result.push('\n');
    }
    result
}

// ---------------------------------------------------------------------
// Plain scalars
// ---------------------------------------------------------------------

/// A YAML plain scalar, to the end of the line, with the continuation
/// lines a multi-line plain scalar folds in.
#[allow(clippy::too_many_lines)]
pub(crate) fn plain_scalar(
    src: &str,
    fwd: &str,
    cursor: (usize, usize, usize),
    context: &mut Context,
) -> Act {
    let in_flow = state::flow_depth_at(context, src, cursor.0) > 0;

    let mut line_start = cursor.0;
    while line_start > 0 && !line_end(at(src, line_start - 1)) {
        line_start -= 1;
    }

    let mut current_line_indent = 0usize;
    {
        let mut index = line_start;
        while index < cursor.0 && is(src, index, b' ') {
            current_line_indent += 1;
            index += 1;
        }
    }

    // Preceded by `: ` on the same line, so this is a mapping value.
    let mut is_map_value = false;
    {
        let mut index = cursor.0 as isize - 1;
        while index >= line_start as isize && blank(at(src, index as usize)) {
            index -= 1;
        }
        if index >= line_start as isize && is(src, index as usize, b':') {
            is_map_value = true;
        }
    }

    let minimum_indent = if is_map_value {
        current_line_indent + 1
    } else {
        current_line_indent
    };

    let mut index = 0usize;
    let scan_line = |fwd: &str, index: &mut usize| -> String {
        let start = *index;
        let mut stop = *index;
        while stop < fwd.len() {
            let byte = at(fwd, stop);
            if line_end(byte) {
                break;
            }
            if byte == i32::from(b':') && blank_line_end_or_eof(at(fwd, stop + 1)) {
                break;
            }
            if blank(byte) && is(fwd, stop + 1, b'#') {
                break;
            }
            if in_flow && (byte == i32::from(b']') || byte == i32::from(b'}')) {
                break;
            }
            if byte == i32::from(b',') && in_flow {
                break;
            }
            stop += 1;
        }
        *index = stop;
        trim_end(&fwd[start..stop]).to_string()
    };

    let mut text = scan_line(fwd, &mut index);
    let mut total_consumed = index;
    let mut rows = 0usize;

    while index < fwd.len() && line_end(at(fwd, index)) {
        let mut blank_lines = 0usize;
        while index < fwd.len() && line_end(at(fwd, index)) {
            if is(fwd, index, b'\r') {
                index += 1;
            }
            if is(fwd, index, b'\n') {
                index += 1;
            }
            let mut leading = 0;
            while blank(at(fwd, index + leading)) {
                leading += 1;
            }
            if index + leading >= fwd.len() || line_end(at(fwd, index + leading)) {
                blank_lines += 1;
                index += leading;
                continue;
            }
            break;
        }
        let mut line_indent = 0usize;
        while blank(at(fwd, index)) {
            line_indent += 1;
            index += 1;
        }

        let doc_marker = line_indent == 0 && is_doc_marker(fwd, index);

        // A `- ` only starts a new sequence entry when its indent matches
        // an enclosing sequence's.
        let mut sequence_marker = false;
        if is(fwd, index, b'-') && blank_line_end_or_eof(at(fwd, index + 1)) {
            let mut sequence_indent: isize = -1;
            let mut probe = cursor.0 as isize - 1;
            while probe >= line_start as isize {
                if is(src, probe as usize, b'-') && blank(at(src, probe as usize + 1)) {
                    sequence_indent = probe - line_start as isize;
                    break;
                }
                probe -= 1;
            }
            sequence_marker = (sequence_indent >= 0 && line_indent as isize == sequence_indent)
                || (sequence_indent < 0 && line_indent <= current_line_indent);
        }

        let can_continue = if in_flow {
            index < fwd.len()
                && !line_end(at(fwd, index))
                && !is(fwd, index, b'#')
                && !is(fwd, index, b'{')
                && !is(fwd, index, b'}')
                && !is(fwd, index, b'[')
                && !is(fwd, index, b']')
        } else {
            line_indent >= minimum_indent
                && index < fwd.len()
                && !line_end(at(fwd, index))
                && !is(fwd, index, b'#')
                && !doc_marker
                && !sequence_marker
        };

        if can_continue {
            // A line holding `: ` is a new pair, not a continuation.
            let mut peek = index;
            let mut is_pair = false;
            while peek < fwd.len() && !line_end(at(fwd, peek)) {
                if is(fwd, peek, b':') && blank_line_end_or_eof(at(fwd, peek + 1)) {
                    is_pair = true;
                    break;
                }
                if is(fwd, peek, b'}') || is(fwd, peek, b']') || is(fwd, peek, b',') {
                    break;
                }
                peek += 1;
            }
            if !is_pair || in_flow {
                let continuation = scan_line(fwd, &mut index);
                if !continuation.is_empty() {
                    if blank_lines > 0 {
                        for _ in 0..blank_lines {
                            text.push('\n');
                        }
                    } else {
                        text.push(' ');
                    }
                    text.push_str(&continuation);
                    total_consumed = index;
                    rows += 1;
                    continue;
                }
            }
        }
        // Not a continuation. The canonical handler puts `i` back to
        // the newline here; nothing reads it after the loop, because the
        // token's span is `total_consumed`, which was last set at the
        // end of a line that WAS taken.
        break;
    }

    let text = trim_end(&text).to_string();
    if text.is_empty() {
        return Act::still(cursor);
    }

    if let Some(value) = crate::yaml_keyword(&text) {
        let next = advance_cols(src, cursor, text.len());
        return Act::moved(next, Some(Tok::new("#VL", value, text, cursor)));
    }

    if let Some(number) = crate::js_to_number(&text) {
        if !number.is_nan() {
            let next = advance_cols(src, cursor, text.len());
            return Act::moved(
                next,
                Some(Tok::new("#NR", Value::Number(number), text, cursor)),
            );
        }
    }

    let source = fwd[..total_consumed].to_string();
    let columns = src[cursor.0..(cursor.0 + total_consumed).min(src.len())]
        .chars()
        .count();
    Act::moved(
        (
            cursor.0 + total_consumed,
            cursor.1 + rows,
            cursor.2 + columns,
        ),
        Some(Tok::new("#TX", Value::String(text), source, cursor)),
    )
}

// ---------------------------------------------------------------------
// Typed tags
// ---------------------------------------------------------------------

/// `!!str`, `!!int`, `!!float`, `!!bool`, `!!null` and any other typed
/// tag, applied to the value that follows. `Ok` means the tag was
/// consumed and the value is on a later line; `Err` carries the token.
pub(crate) fn type_tag(
    src: &str,
    fwd: &str,
    cursor: (usize, usize, usize),
    context: &mut Context,
) -> Result<(usize, usize, usize), Box<Act>> {
    let mut tag_end = 2;
    while tag_end < fwd.len() {
        let byte = at(fwd, tag_end);
        // A tab does not end a tag name here either.
        if byte == i32::from(b' ')
            || line_end(byte)
            || byte == i32::from(b',')
            || byte == i32::from(b'}')
            || byte == i32::from(b']')
            || byte == i32::from(b':')
        {
            break;
        }
        tag_end += 1;
    }
    let tag = fwd[2..tag_end].to_string();
    let mut value_start = tag_end;
    if is(fwd, value_start, b' ') {
        value_start += 1;
    }
    let mut value_end = value_start;

    // An anchor between the tag and the value.
    let mut anchor_name = String::new();
    if is(fwd, value_start, b'&') {
        // `fwd[anchorEnd] !== ' ' && !== '\n' && !== '\r'`: a SPACE or
        // a line end ends the name here, and a TAB does not. It is not
        // the anchor scan the lexer's own `&name` handler runs, which
        // stops at a tab and at a flow indicator too.
        let mut anchor_end = value_start + 1;
        while anchor_end < fwd.len() && !is(fwd, anchor_end, b' ') && !line_end(at(fwd, anchor_end))
        {
            anchor_end += 1;
        }
        anchor_name = fwd[value_start + 1..anchor_end].to_string();
        let mut pending = indexmap::IndexMap::new();
        pending.insert("n".to_string(), Value::String(anchor_name.clone()));
        pending.insert("i".to_string(), Value::Bool(true));
        state::list_push(context, state::PENDING_ANCHORS, Value::object(pending));
        if is(fwd, anchor_end, b' ') {
            anchor_end += 1;
        }
        value_start = anchor_end;
        value_end = value_start;
    }

    let redefined = !matches!(
        state::map_get(context, state::TAG_HANDLES, "!!"),
        None | Some(Value::Undefined)
    );

    // A quoted value.
    if is(fwd, value_start, b'"') || is(fwd, value_start, b'\'') {
        let quote = at(fwd, value_start);
        value_end = value_start + 1;
        while value_end < fwd.len() && at(fwd, value_end) != quote {
            if is(fwd, value_end, b'\\') && quote == i32::from(b'"') {
                value_end += 1;
            }
            value_end += 1;
        }
        if at(fwd, value_end) == quote {
            value_end += 1;
        }
        // `fwd.substring(valStart + 1, valEnd - 1)`. An unterminated quote
        // leaves `value_end` at the end of the source, and an escape at
        // the very end leaves it one past: neither index can be sliced
        // directly, and stepping one BYTE back from the end lands inside
        // a multibyte character where the canonical steps one UTF-16
        // unit.
        let raw = js_substring_less_one_unit(fwd, value_start + 1, value_end);
        let value = convert_tag(&tag, &raw, redefined, true);
        if !anchor_name.is_empty() {
            state::map_set(context, state::ANCHORS, anchor_name, value.clone());
        }
        let name = token_name_for(&value, false);
        let source = js_substring(fwd, 0, value_end).to_string();
        let next = advance_cols(src, cursor, value_end);
        return Err(Box::new(Act::moved(
            next,
            Some(Tok::new(name, value, source, cursor)),
        )));
    }

    // The value is on the next line: consume the tag and look again.
    if line_end(at(fwd, value_start)) && value_start + 1 < fwd.len() {
        let mut newline = value_start;
        if is(fwd, newline, b'\r') {
            newline += 1;
        }
        if is(fwd, newline, b'\n') {
            newline += 1;
        }
        return Ok((cursor.0 + newline, cursor.1 + 1, 0));
    }

    // An unquoted value, to `: `, ` #`, the line end or a flow indicator.
    while value_end < fwd.len() {
        let byte = at(fwd, value_end);
        if line_end(byte)
            || byte == i32::from(b',')
            || byte == i32::from(b'}')
            || byte == i32::from(b']')
        {
            break;
        }
        // `fwd[valEnd+1] === ' ' || '\n' || '\r' || undefined`: a
        // literal SPACE, spelled out, with no tab among them. A tab
        // after the colon leaves the colon inside the value.
        if byte == i32::from(b':')
            && (is(fwd, value_end + 1, b' ')
                || line_end(at(fwd, value_end + 1))
                || at(fwd, value_end + 1) < 0)
        {
            break;
        }
        if byte == i32::from(b' ') && is(fwd, value_end + 1, b'#') {
            break;
        }
        value_end += 1;
    }
    let raw = trim_end(&fwd[value_start..value_end]).to_string();
    let value = convert_tag(&tag, &raw, redefined, false);
    if !anchor_name.is_empty() {
        state::map_set(context, state::ANCHORS, anchor_name, value.clone());
    }
    let name = token_name_for(&value, true);
    let source = fwd[..value_end].to_string();
    let next = advance_cols(src, cursor, value_end);
    Err(Box::new(Act::moved(
        next,
        Some(Tok::new(name, value, source, cursor)),
    )))
}

/// Apply a typed tag's conversion. When `%TAG !!` has been redefined the
/// tag is a user handle, not a YAML core type, and only a quoted value's
/// raw text survives; an unquoted one still takes the `str` conversion,
/// as the canonical handler leaves it.
fn convert_tag(tag: &str, raw: &str, redefined: bool, quoted: bool) -> Value {
    if redefined {
        return Value::String(raw.to_string());
    }
    match tag {
        "str" if !quoted => Value::String(raw.to_string()),
        "int" => Value::Number(crate::parse_int(raw)),
        "float" => Value::Number(crate::parse_float(raw)),
        "bool" => Value::Bool(raw == "true" || raw == "True" || raw == "TRUE"),
        "null" => Value::Null,
        _ => Value::String(raw.to_string()),
    }
}

/// The token identity a converted value takes. An empty string becomes
/// `#ST` in the unquoted branch, which the canonical handler does because
/// the engine reads an empty `#TX` badly inside a flow collection.
fn token_name_for(value: &Value, empty_is_string_token: bool) -> &'static str {
    match value {
        Value::String(text) if empty_is_string_token && text.is_empty() => "#ST",
        Value::String(_) => "#TX",
        Value::Number(_) => "#NR",
        _ => "#VL",
    }
}
