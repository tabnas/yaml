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
    set_flag(context, FLOW_SQ, false);
    set_flag(context, FLOW_DQ, false);
}

/// Bring the flow-collection depth cache up to `target`, a byte offset.
///
/// The canonical `updateFlowState` scans forward from where it last
/// stopped, skipping quoted regions so a bracket inside a scalar does not
/// change the depth. Rescanning from zero on every token would make the
/// whole parse quadratic, which is what the cache exists to prevent.
pub(crate) fn update_flow(context: &mut Context, src: &str, target: usize) {
    let mut depth = usize_at(context, FLOW_DEPTH);
    let mut pos = usize_at(context, FLOW_POS);
    let mut single = flag(context, FLOW_SQ);
    let mut double = flag(context, FLOW_DQ);
    if target < pos {
        depth = 0;
        pos = 0;
        single = false;
        double = false;
    }
    let bytes = src.as_bytes();
    let target = target.min(bytes.len());
    let mut index = pos;
    while index < target {
        let character = bytes[index];
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
                if index + 1 < target && bytes[index + 1] == b'\'' {
                    index += 1;
                } else {
                    single = false;
                }
            }
            index += 1;
            continue;
        }
        match character {
            b'{' | b'[' => depth += 1,
            b'}' | b']' => {
                depth = depth.saturating_sub(1);
            }
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
    }
    set_usize(context, FLOW_DEPTH, depth);
    set_usize(context, FLOW_POS, target);
    set_flag(context, FLOW_SQ, single);
    set_flag(context, FLOW_DQ, double);
}

/// The flow depth at `target`, after bringing the cache up to it.
pub(crate) fn flow_depth_at(context: &mut Context, src: &str, target: usize) -> usize {
    update_flow(context, src, target);
    usize_at(context, FLOW_DEPTH)
}
