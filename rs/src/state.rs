/* Copyright (c) 2021-2026 Richard Rodger, MIT License */

//! Per-parse state, held in the parse context's `u` bag.
//!
//! The canonical TypeScript keeps anchors, pending anchors, pending
//! tokens, tag handles, the stream accumulators and the flow-depth cache
//! as closures over the plugin installation, and the Go port carries the
//! same thirteen variables per instance. Neither can be done here:
//! [`tabnas::Tabnas::parse`] takes `&self` and the instance is
//! `Send + Sync`, so anything written during a parse has to belong to
//! that parse. Every one of those variables lives in `Context::u`
//! instead, under a `yaml` prefix, and is read and written through the
//! accessors below.
//!
//! The keys are strings and the values are engine [`Value`]s, which is
//! what the bag holds. A container is behind an `Arc`, so reading one is
//! a refcount bump and writing through `Arc::make_mut` copies only when
//! a snapshot still holds the old contents.

use indexmap::IndexMap;
use std::sync::Arc;
use tabnas::{Context, Value};

use crate::lex::BOM_TEXT;
use crate::source::Src;

/// Set once the first lexer call of a parse has run the reset that the
/// canonical `seenLex` WeakSet stands in for.
pub(crate) const INIT: &str = "yamlInit";

/// `anchors`: anchor name to its recorded value.
pub(crate) const ANCHORS: &str = "yamlAnchors";

/// `pendingAnchors`: anchors claimed by the next `val` rule, each an
/// object `{n: name, i: inline}`.
pub(crate) const PENDING_ANCHORS: &str = "yamlPendingAnchors";

/// `declaredAnchors`: every anchor name the lexer has passed, each mapped
/// to `true`, so an alias can be checked against YAML's rule that its
/// anchor occurs before it. [`ANCHORS`] cannot answer that: a value is
/// recorded only when its node closes, so `&a [*a]` names an anchor that
/// occurs before it but is not recorded yet.
pub(crate) const DECLARED_ANCHORS: &str = "yamlDeclaredAnchors";

/// `pendingExplicitCL`: a `#CL` owed to the explicit-key handler.
pub(crate) const PENDING_CL: &str = "yamlPendingCL";

/// `pendingTokens`: tokens the explicit-key handler queued, serialized
/// (see [`crate::lex::Tok`]) because the bag holds values, not tokens.
pub(crate) const PENDING_TOKENS: &str = "yamlPendingTokens";

/// `tagHandles`: `%TAG` handle to prefix.
pub(crate) const TAG_HANDLES: &str = "yamlTagHandles";

/// The `stream` rule's accumulated document values.
pub(crate) const DOCS: &str = "yamlDocs";

/// The `stream` rule's accumulated per-document metadata.
pub(crate) const METAS: &str = "yamlMetas";

/// The metadata of the document currently open, absent when none is.
pub(crate) const CUR_META: &str = "yamlCurMeta";

/// The incremental flow-collection scan: depth, how far it has read, and
/// whether it is inside a single or double quoted scalar.
pub(crate) const FLOW_DEPTH: &str = "yamlFlowDepth";
pub(crate) const FLOW_POS: &str = "yamlFlowPos";
pub(crate) const FLOW_UPTO: &str = "yamlFlowUpTo";
pub(crate) const FLOW_SQ: &str = "yamlFlowSingle";
pub(crate) const FLOW_DQ: &str = "yamlFlowDouble";

/// The virtual cursor: the row, the column, and the byte offset they
/// were computed for. See [`crate::lex`] for why this port keeps its own.
pub(crate) const V_ROW: &str = "yamlVirtualRow";
pub(crate) const V_COL: &str = "yamlVirtualCol";
pub(crate) const V_POS: &str = "yamlVirtualPos";

/// Set when the YAML matcher moved the cursor in this lexer call and
/// produced no token. See [`crate::text::check`].
pub(crate) const MOVED: &str = "yamlMoved";

/// `literalMergeKey`: the mappings whose `<<` key was quoted, each named
/// by the address of the node cell its pairs share, so that the map rule
/// leaves them unmerged.
pub(crate) const LITERAL_MERGE: &str = "yamlLiteralMerge";

pub(crate) fn flag(context: &Context, key: &str) -> bool {
    matches!(context.u.get(key), Some(Value::Bool(true)))
}

pub(crate) fn set_flag(context: &mut Context, key: &str, value: bool) {
    put(context, key, Value::Bool(value));
}

/// Set the slot `key` to `value`, its key allocated only when the slot is
/// new: the lexer sets a few slots at every token.
fn put(context: &mut Context, key: &str, value: Value) {
    match context.u.get_mut(key) {
        Some(slot) => *slot = value,
        None => {
            context.u.insert(key.to_string(), value);
        }
    }
}

pub(crate) fn num(context: &Context, key: &str) -> Option<f64> {
    match context.u.get(key) {
        Some(Value::Number(number)) => Some(*number),
        _ => None,
    }
}

pub(crate) fn set_num(context: &mut Context, key: &str, value: f64) {
    put(context, key, Value::Number(value));
}

pub(crate) fn usize_at(context: &Context, key: &str) -> usize {
    num(context, key).map_or(0, |value| value.max(0.0) as usize)
}

pub(crate) fn set_usize(context: &mut Context, key: &str, value: usize) {
    set_num(context, key, value as f64);
}

/// Read one entry of an object-valued slot.
pub(crate) fn map_get(context: &Context, key: &str, entry: &str) -> Option<Value> {
    match context.u.get(key) {
        Some(Value::Object(map)) => map.get(entry).cloned(),
        _ => None,
    }
}

/// Write one entry of an object-valued slot, creating the object when
/// this is the first write.
pub(crate) fn map_set(context: &mut Context, key: &str, entry: String, value: Value) {
    let slot = context
        .u
        .entry(key.to_string())
        .or_insert_with(|| Value::object(IndexMap::new()));
    if let Value::Object(map) = slot {
        Arc::make_mut(map).insert(entry, value);
    } else {
        let mut map = IndexMap::new();
        map.insert(entry, value);
        *slot = Value::object(map);
    }
}

/// `pruneHold`: how many anchored values are open. A host that streams a
/// document through transduce's `ParserEventSource` empties each array
/// behind the stream once its rule's close pass has gone by, and an
/// anchor is recorded only when its value closes, after the arrays inside
/// it have closed, so the record would hold them emptied: an alias used as
/// a key would then name the emptied text (tabnas/yaml#138). While this
/// count is above zero, a source that honours the hold empties no array.
/// The key is transduce's, the same in every runtime, and lives in the
/// context's bag, not under the `yaml` prefix, because a host reads it.
pub(crate) const PRUNE_HOLD: &str = "pruneHold";

/// Raise or lower [`PRUNE_HOLD`] by `by`, one open anchored value at a time.
pub(crate) fn hold_pruning(context: &mut Context, by: f64) {
    let now = match context.u.get(PRUNE_HOLD) {
        Some(Value::Number(n)) => *n,
        _ => 0.0,
    };
    context
        .u
        .insert(PRUNE_HOLD.to_string(), Value::Number((now + by).max(0.0)));
}

/// Queue an anchor for the next `val` rule to claim, and note that it
/// occurs, for [`anchor_declared`].
pub(crate) fn pend_anchor(context: &mut Context, name: &str, pending: Value) {
    list_push(context, PENDING_ANCHORS, pending);
    map_set_in_cut(
        context,
        DECLARED_ANCHORS,
        name.to_string(),
        Value::Bool(true),
    );
}

/// What the cut being made changed of the anchors, newest last, in a parse
/// from a reader: `[key, name, had, old]` for each entry it set, so that a
/// cut the engine makes again over a wider window can take it back
/// (`crate::source::Kept::restore`). Present only while a cut over a
/// window is being made.
pub(crate) const UNDO: &str = "yamlUndo";

/// [`map_set`] for an entry the lexer sets as it cuts a token: an anchor
/// recorded or declared, noted in [`UNDO`] when a cut over a window is
/// being made.
pub(crate) fn map_set_in_cut(context: &mut Context, key: &str, entry: String, value: Value) {
    if matches!(context.u.get(UNDO), Some(Value::Array(_))) {
        let old = map_get(context, key, &entry);
        let record = Value::array(vec![
            Value::String(key.to_string()),
            Value::String(entry.clone()),
            Value::Bool(old.is_some()),
            old.unwrap_or(Value::Undefined),
        ]);
        list_push(context, UNDO, record);
    }
    map_set(context, key, entry, value);
}

/// Take back what the cut being made set, newest first.
pub(crate) fn undo(context: &mut Context) {
    let records = list_take(context, UNDO);
    for record in records.into_iter().rev() {
        let Value::Array(items) = record else {
            continue;
        };
        let (Some(Value::String(key)), Some(Value::String(name)), Some(had), Some(old)) =
            (items.first(), items.get(1), items.get(2), items.get(3))
        else {
            continue;
        };
        if matches!(had, Value::Bool(true)) {
            map_set(context, key, name.clone(), old.clone());
        } else {
            map_take(context, key, name);
        }
    }
}

/// Has an anchor named `name` occurred before this point of the parse?
pub(crate) fn anchor_declared(context: &Context, name: &str) -> bool {
    matches!(context.u.get(DECLARED_ANCHORS), Some(Value::Object(map)) if map.contains_key(name))
}

/// Remove one entry of an object-valued slot, saying whether it was there.
pub(crate) fn map_take(context: &mut Context, key: &str, entry: &str) -> bool {
    match context.u.get_mut(key) {
        Some(Value::Object(map)) if map.contains_key(entry) => {
            Arc::make_mut(map).shift_remove(entry);
            true
        }
        _ => false,
    }
}

/// Replace an object-valued slot wholesale.
pub(crate) fn map_put(context: &mut Context, key: &str, map: IndexMap<String, Value>) {
    context.u.insert(key.to_string(), Value::object(map));
}

/// Append to an array-valued slot, creating the array when this is the
/// first write.
pub(crate) fn list_push(context: &mut Context, key: &str, value: Value) {
    let slot = context
        .u
        .entry(key.to_string())
        .or_insert_with(|| Value::array(Vec::new()));
    if let Value::Array(items) = slot {
        Arc::make_mut(items).push(value);
    } else {
        *slot = Value::array(vec![value]);
    }
}

/// Take the whole array out of an array-valued slot, leaving it empty.
pub(crate) fn list_take(context: &mut Context, key: &str) -> Vec<Value> {
    match context.u.insert(key.to_string(), Value::array(Vec::new())) {
        Some(Value::Array(items)) => (*items).clone(),
        _ => Vec::new(),
    }
}

/// Remove and return the first entry of an array-valued slot.
pub(crate) fn list_shift(context: &mut Context, key: &str) -> Option<Value> {
    let slot = context.u.get_mut(key)?;
    let Value::Array(items) = slot else {
        return None;
    };
    let items = Arc::make_mut(items);
    if items.is_empty() {
        None
    } else {
        Some(items.remove(0))
    }
}

pub(crate) fn list_len(context: &Context, key: &str) -> usize {
    match context.u.get(key) {
        Some(Value::Array(items)) => items.len(),
        _ => 0,
    }
}

pub(crate) fn list_all(context: &Context, key: &str) -> Vec<Value> {
    match context.u.get(key) {
        Some(Value::Array(items)) => (**items).clone(),
        _ => Vec::new(),
    }
}

pub(crate) fn clear(context: &mut Context, key: &str) {
    context.u.shift_remove(key);
}

/// The reset the canonical matcher performs on the first lexer call of a
/// parse. Here the bag starts empty, so this only has to plant the flag
/// and the empty containers the accessors expect.
pub(crate) fn initialise(context: &mut Context) {
    set_flag(context, INIT, true);
    map_put(context, ANCHORS, IndexMap::new());
    map_put(context, DECLARED_ANCHORS, IndexMap::new());
    map_put(context, TAG_HANDLES, IndexMap::new());
    context
        .u
        .insert(PENDING_ANCHORS.to_string(), Value::array(Vec::new()));
    context
        .u
        .insert(PENDING_TOKENS.to_string(), Value::array(Vec::new()));
    context.u.insert(DOCS.to_string(), Value::array(Vec::new()));
    context
        .u
        .insert(METAS.to_string(), Value::array(Vec::new()));
    clear(context, CUR_META);
    set_flag(context, PENDING_CL, false);
    set_usize(context, FLOW_DEPTH, 0);
    set_usize(context, FLOW_POS, 0);
    set_usize(context, FLOW_UPTO, 0);
    set_flag(context, FLOW_SQ, false);
    set_flag(context, FLOW_DQ, false);
}

/// Bring the flow-collection depth cache up to `target`, a byte offset.
///
/// The canonical `updateFlowState` scans forward from where it last
/// stopped. Rescanning from zero on every token would make the whole parse
/// quadratic, which is what the cache exists to prevent.
///
/// The scan reads the source the way the lexer does, or text is counted
/// as syntax (tabnas/yaml#89). A quoted region is skipped, and so are the
/// regions where a bracket or a quote is ordinary text: a comment, a block
/// scalar's content lines, and, in block context, any `{` `[` `"` `'` that
/// does not begin a node. The scan may run past `target` (to the end of a
/// comment or a block scalar), since the lexer never stops inside either.
pub(crate) fn update_flow(context: &mut Context, src: Src<'_>, target: usize) {
    // Each field is a hashed lookup in the context bag, so the scan reads
    // only what it needs and writes back only what changed. The lexer asks
    // at the same offset several times per token, and then nothing can have
    // changed at all.
    let up_to = usize_at(context, FLOW_UPTO);
    if target == up_to {
        return;
    }
    let before = (
        usize_at(context, FLOW_DEPTH),
        usize_at(context, FLOW_POS),
        flag(context, FLOW_SQ),
        flag(context, FLOW_DQ),
    );
    let (mut depth, mut pos, mut single, mut double) = before;
    if target < up_to {
        depth = 0;
        pos = 0;
        single = false;
        double = false;
    }
    set_usize(context, FLOW_UPTO, target);
    let target = src.clamp(target);
    let mut index = pos;
    while index < target {
        let character = src.at(index) as u8;
        // Fast path: only ten bytes matter in any state.
        if !FLOW_SCAN_BYTES[usize::from(character)] {
            index += 1;
            continue;
        }
        if double {
            if character == b'\\' {
                index += 1;
            } else if character == b'"' {
                double = false;
            }
            index += 1;
            continue;
        }
        if single {
            if character == b'\'' {
                if src.is(index + 1, b'\'') {
                    index += 1;
                } else {
                    single = false;
                }
            }
            index += 1;
            continue;
        }
        if character == b'#' && (index == 0 || is_space(src.at(index - 1) as u8)) {
            while src.has(index) && !src.is(index, b'\n') && !src.is(index, b'\r') {
                index += 1;
            }
            continue;
        }
        if depth > 0 {
            match character {
                b'{' | b'[' => depth += 1,
                b'}' | b']' => depth -= 1,
                b'"' => double = true,
                b'\'' => {
                    // An apostrophe inside a word does not open a scalar.
                    let previous = if index > 0 {
                        src.at(index - 1) as u8
                    } else {
                        0
                    };
                    if !previous.is_ascii_alphanumeric() {
                        single = true;
                    }
                }
                _ => {}
            }
            index += 1;
            continue;
        }
        // Block context: only a node's first character opens anything.
        if matches!(character, b'{' | b'[' | b'"' | b'\'' | b'|' | b'>') {
            if !starts_node(src, index) {
                // Inside a plain scalar, which nothing can open until it
                // ends at `: `, ` #` or the end of the line: skip to there.
                index = plain_scalar_rest(src, index);
                continue;
            }
            match character {
                b'{' | b'[' => depth += 1,
                b'"' => double = true,
                b'\'' => single = true,
                _ => {
                    let end = block_scalar_end(src, index);
                    if end > index {
                        index = end;
                        continue;
                    }
                }
            }
        }
        index += 1;
    }
    if depth != before.0 {
        set_usize(context, FLOW_DEPTH, depth);
    }
    if index != before.1 {
        set_usize(context, FLOW_POS, index);
    }
    if single != before.2 {
        set_flag(context, FLOW_SQ, single);
    }
    if double != before.3 {
        set_flag(context, FLOW_DQ, double);
    }
}

/// The bytes `update_flow` acts on: quotes, the escape backslash, the
/// comment mark, flow brackets, and block scalar indicators. Every other
/// byte leaves its state unchanged. Mirrors the canonical `FLOW_SCAN_CHARS`.
const FLOW_SCAN_BYTES: [bool; 256] = {
    let mut table = [false; 256];
    let marked = b"\"'\\#{}[]|>";
    let mut i = 0;
    while i < marked.len() {
        table[marked[i] as usize] = true;
        i += 1;
    }
    table
};

fn is_space(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\n' | b'\r')
}

/// Whether the byte at `index` (block context) is the first character of
/// a node: first on its line, or after a `: ` / `- ` / `? ` indicator, a
/// `---` marker, or a node property (`&anchor`, `!tag`) that itself starts
/// a node. Anything else is inside a plain scalar. Mirrors the canonical
/// `startsYamlNode`.
fn starts_node(src: Src<'_>, index: usize) -> bool {
    let byte = |k: usize| src.at(k) as u8;
    let mut p = index;
    while p > 0 && matches!(byte(p - 1), b' ' | b'\t') {
        p -= 1;
    }
    if p == 0 || matches!(byte(p - 1), b'\n' | b'\r') || (p == 3 && src.starts_with(0, BOM_TEXT)) {
        return true;
    }
    if p == index {
        return false;
    }
    let p = p - 1;
    let pc = byte(p);
    if pc == b':' {
        return true;
    }
    if pc == b'-' || pc == b'?' {
        if pc == b'-'
            && p >= 2
            && byte(p - 1) == b'-'
            && byte(p - 2) == b'-'
            && (p < 3 || matches!(byte(p - 3), b'\n' | b'\r'))
        {
            return true;
        }
        return starts_node(src, p);
    }
    let mut w = p;
    while w > 0 && !is_space(byte(w - 1)) {
        w -= 1;
    }
    if byte(w) == b'&' || byte(w) == b'!' {
        return starts_node(src, w);
    }
    false
}

/// From `index` inside a block-context plain scalar, the offset where it
/// can end: the next line break, a `:` followed by whitespace or the end,
/// or a `#` after whitespace. Mirrors the canonical `plainScalarRest`.
fn plain_scalar_rest(src: Src<'_>, index: usize) -> usize {
    let mut j = index + 1;
    while src.has(j) {
        match src.at(j) as u8 {
            b'\n' | b'\r' => return j,
            b':' if !src.has(j + 1) || is_space(src.at(j + 1) as u8) => return j,
            b'#' if matches!(src.at(j - 1) as u8, b' ' | b'\t') => return j,
            _ => {}
        }
        j += 1;
    }
    j
}

/// A block scalar indicator (`|` or `>`, optional chomping/indentation
/// indicators, then only a comment) at `index`: the offset just past its
/// content lines, or `index` when it is not one. Mirrors the canonical
/// `blockScalarEnd`.
fn block_scalar_end(src: Src<'_>, index: usize) -> usize {
    let at = |k: usize| {
        let c = src.at(k);
        (c >= 0).then_some(c as u8)
    };
    let mut j = index + 1;
    let mut explicit = 0usize;
    for _ in 0..2 {
        match at(j) {
            Some(b'+' | b'-') => j += 1,
            Some(c @ b'1'..=b'9') => {
                explicit = usize::from(c - b'0');
                j += 1;
            }
            _ => {}
        }
    }
    while matches!(at(j), Some(b' ' | b'\t')) {
        j += 1;
    }
    if at(j) == Some(b'#') {
        while matches!(at(j), Some(c) if c != b'\n' && c != b'\r') {
            j += 1;
        }
    }
    if matches!(at(j), Some(c) if c != b'\n' && c != b'\r') {
        return index;
    }
    let mut ls = src.line_start(index);
    if ls == 0 && src.starts_with(0, BOM_TEXT) {
        ls = BOM_TEXT.len();
    }
    let mut line_indent = 0usize;
    while at(ls + line_indent) == Some(b' ') {
        line_indent += 1;
    }
    let is_root = ls + line_indent == index || src.starts_with(ls, "---");
    // The indent a content line must exceed; None is the document root's -1.
    let parent: Option<usize> = if is_root && line_indent == 0 {
        None
    } else {
        Some(line_indent)
    };
    let mut pos = j;
    if at(pos) == Some(b'\r') {
        pos += 1;
    }
    if at(pos) == Some(b'\n') {
        pos += 1;
    }
    let mut block_indent = None;
    if explicit > 0 {
        // As the block scalar handler does: after a key on the same line
        // (`- a: |2`), the indent the indicator counts from includes each
        // `- ` before the key, not only the line's leading spaces.
        let mut base = parent.unwrap_or(0);
        let start = ls + line_indent;
        let has_colon = (start..index)
            .any(|ci| at(ci) == Some(b':') && matches!(at(ci + 1), Some(b' ' | b'\t')));
        if has_colon {
            let mut si = start;
            while si < index && at(si) == Some(b'-') && matches!(at(si + 1), Some(b' ' | b'\t')) {
                base += 2;
                si += 2;
                while si < index && at(si) == Some(b' ') {
                    base += 1;
                    si += 1;
                }
            }
        }
        block_indent = Some(base + explicit);
    }
    let mut end = pos;
    while src.has(pos) {
        let mut n = 0usize;
        while at(pos + n) == Some(b' ') {
            n += 1;
        }
        let c = at(pos + n);
        let mut line_stop = pos + n;
        while matches!(at(line_stop), Some(c) if c != b'\n' && c != b'\r') {
            line_stop += 1;
        }
        let mut next = line_stop;
        if at(next) == Some(b'\r') {
            next += 1;
        }
        if at(next) == Some(b'\n') {
            next += 1;
        }
        if matches!(c, None | Some(b'\n' | b'\r')) {
            pos = next;
            continue;
        }
        let indent = match block_indent {
            Some(indent) => indent,
            None => {
                if parent.is_some_and(|parent| n <= parent) {
                    break;
                }
                block_indent = Some(n);
                n
            }
        };
        if n < indent {
            break;
        }
        if n == 0 && (src.starts_with(pos, "---") || src.starts_with(pos, "...")) {
            break;
        }
        end = line_stop;
        pos = next;
    }
    end
}

/// The flow depth at `target`, after bringing the cache up to it.
pub(crate) fn flow_depth_at(context: &mut Context, src: Src<'_>, target: usize) -> usize {
    update_flow(context, src, target);
    usize_at(context, FLOW_DEPTH)
}
