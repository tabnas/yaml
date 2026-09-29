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

/// Set once the first lexer call of a parse has run the reset that the
/// canonical `seenLex` WeakSet stands in for.
pub(crate) const INIT: &str = "yamlInit";

/// `anchors`: anchor name to its recorded value.
pub(crate) const ANCHORS: &str = "yamlAnchors";

/// `pendingAnchors`: anchors claimed by the next `val` rule, each an
/// object `{n: name, i: inline}`.
pub(crate) const PENDING_ANCHORS: &str = "yamlPendingAnchors";

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

pub(crate) fn flag(context: &Context, key: &str) -> bool {
    matches!(context.u.get(key), Some(Value::Bool(true)))
}

pub(crate) fn set_flag(context: &mut Context, key: &str, value: bool) {
    context.u.insert(key.to_string(), Value::Bool(value));
}

pub(crate) fn num(context: &Context, key: &str) -> Option<f64> {
    match context.u.get(key) {
        Some(Value::Number(number)) => Some(*number),
        _ => None,
    }
}

pub(crate) fn set_num(context: &mut Context, key: &str, value: f64) {
    context.u.insert(key.to_string(), Value::Number(value));
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
pub(crate) fn update_flow(context: &mut Context, src: &str, target: usize) {
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
    let bytes = src.as_bytes();
    let target = target.min(bytes.len());
    let mut index = pos;
    while index < target {
        let character = bytes[index];
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
                if index + 1 < bytes.len() && bytes[index + 1] == b'\'' {
                    index += 1;
                } else {
                    single = false;
                }
            }
            index += 1;
            continue;
        }
        if character == b'#' && (index == 0 || is_space(bytes[index - 1])) {
            while index < bytes.len() && bytes[index] != b'\n' && bytes[index] != b'\r' {
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
                    let previous = if index > 0 { bytes[index - 1] } else { 0 };
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
            if !starts_node(bytes, index) {
                // Inside a plain scalar, which nothing can open until it
                // ends at `: `, ` #` or the end of the line: skip to there.
                index = plain_scalar_rest(bytes, index);
                continue;
            }
            match character {
                b'{' | b'[' => depth += 1,
                b'"' => double = true,
                b'\'' => single = true,
                _ => {
                    let end = block_scalar_end(bytes, index);
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

/// U+FEFF, the byte order mark, in UTF-8. A stream may open with it; the
/// offset after it is a line start.
const BOM_BYTES: &[u8] = "\u{FEFF}".as_bytes();

fn is_space(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\n' | b'\r')
}

/// Whether the byte at `index` (block context) is the first character of
/// a node: first on its line, or after a `: ` / `- ` / `? ` indicator, a
/// `---` marker, or a node property (`&anchor`, `!tag`) that itself starts
/// a node. Anything else is inside a plain scalar. Mirrors the canonical
/// `startsYamlNode`.
fn starts_node(bytes: &[u8], index: usize) -> bool {
    let mut p = index;
    while p > 0 && matches!(bytes[p - 1], b' ' | b'\t') {
        p -= 1;
    }
    if p == 0 || matches!(bytes[p - 1], b'\n' | b'\r') || (p == 3 && bytes.starts_with(BOM_BYTES)) {
        return true;
    }
    if p == index {
        return false;
    }
    let p = p - 1;
    let pc = bytes[p];
    if pc == b':' {
        return true;
    }
    if pc == b'-' || pc == b'?' {
        if pc == b'-'
            && p >= 2
            && bytes[p - 1] == b'-'
            && bytes[p - 2] == b'-'
            && (p < 3 || matches!(bytes[p - 3], b'\n' | b'\r'))
        {
            return true;
        }
        return starts_node(bytes, p);
    }
    let mut w = p;
    while w > 0 && !is_space(bytes[w - 1]) {
        w -= 1;
    }
    if bytes[w] == b'&' || bytes[w] == b'!' {
        return starts_node(bytes, w);
    }
    false
}

/// From `index` inside a block-context plain scalar, the offset where it
/// can end: the next line break, a `:` followed by whitespace or the end,
/// or a `#` after whitespace. Mirrors the canonical `plainScalarRest`.
fn plain_scalar_rest(bytes: &[u8], index: usize) -> usize {
    let mut j = index + 1;
    while j < bytes.len() {
        match bytes[j] {
            b'\n' | b'\r' => return j,
            b':' if bytes.get(j + 1).is_none_or(|next| is_space(*next)) => return j,
            b'#' if matches!(bytes[j - 1], b' ' | b'\t') => return j,
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
fn block_scalar_end(bytes: &[u8], index: usize) -> usize {
    let at = |k: usize| bytes.get(k).copied();
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
        while j < bytes.len() && !matches!(bytes[j], b'\n' | b'\r') {
            j += 1;
        }
    }
    if j < bytes.len() && !matches!(bytes[j], b'\n' | b'\r') {
        return index;
    }
    let mut ls = index;
    while ls > 0 && !matches!(bytes[ls - 1], b'\n' | b'\r') {
        ls -= 1;
    }
    if ls == 0 && bytes.starts_with(BOM_BYTES) {
        ls = BOM_BYTES.len();
    }
    let mut line_indent = 0usize;
    while at(ls + line_indent) == Some(b' ') {
        line_indent += 1;
    }
    let is_root = ls + line_indent == index || bytes[ls..].starts_with(b"---");
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
        let has_colon =
            (start..index).any(|ci| bytes[ci] == b':' && matches!(at(ci + 1), Some(b' ' | b'\t')));
        if has_colon {
            let mut si = start;
            while si < index && bytes[si] == b'-' && matches!(at(si + 1), Some(b' ' | b'\t')) {
                base += 2;
                si += 2;
                while si < index && bytes[si] == b' ' {
                    base += 1;
                    si += 1;
                }
            }
        }
        block_indent = Some(base + explicit);
    }
    let mut end = pos;
    while pos < bytes.len() {
        let mut n = 0usize;
        while at(pos + n) == Some(b' ') {
            n += 1;
        }
        let c = at(pos + n);
        let mut line_stop = pos + n;
        while line_stop < bytes.len() && !matches!(bytes[line_stop], b'\n' | b'\r') {
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
        if n == 0 && (bytes[pos..].starts_with(b"---") || bytes[pos..].starts_with(b"...")) {
            break;
        }
        end = line_stop;
        pos = next;
    }
    end
}

/// The flow depth at `target`, after bringing the cache up to it.
pub(crate) fn flow_depth_at(context: &mut Context, src: &str, target: usize) -> usize {
    update_flow(context, src, target);
    usize_at(context, FLOW_DEPTH)
}
