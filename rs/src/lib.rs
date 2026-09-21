/* Copyright (c) 2021-2026 Richard Rodger, MIT License */

//! A core-subset YAML grammar plugin for the
//! [`tabnas`](https://github.com/tabnas/parser) parsing engine.
//!
//! Unlike most tabnas grammars this one layers on
//! [`tabnas_jsonic`](https://github.com/tabnas/jsonic) rather than on the
//! bare engine: YAML's flow collections are relaxed JSON, and the block
//! forms are added around jsonic's `val` / `map` / `list` / `pair` /
//! `elem` rules.
//!
//! ```
//! fn main() -> Result<(), Box<dyn std::error::Error>> {
//!     let value = tabnas_yaml::parse("name: Alice\nitems:\n  - one\n  - two\n")?;
//!     assert_eq!(
//!         value.to_string(),
//!         r#"{"name":"Alice","items":["one","two"]}"#
//!     );
//!     Ok(())
//! }
//! ```

#![forbid(unsafe_code)]
// The engine's error type is 664 bytes and every entry point returns it,
// as the other tabnas crates' entry points do.
#![allow(clippy::result_large_err)]

use std::cell::RefCell;
use std::rc::Rc;
use std::sync::{Arc, OnceLock};

use indexmap::IndexMap;
use serde_json::json;
use tabnas::{
    Context, GrammarSpec, Plugin, PluginError, Rule, Tabnas, Token, Value, TIN_ST, TIN_TX, TIN_VL,
};

mod lex;
mod state;
mod text;

/// Every `rust` fence in the README is compiled and run as a doctest.
#[cfg(doctest)]
#[doc = include_str!("../README.md")]
mod readme_examples {}

/// VERSION is this package's version. It MUST equal `ts/package.json`
/// "version": the release orchestrator rewrites all three constants, and
/// `tests/version_test.rs` fails the build if they drift. Mirrors
/// `const VERSION` in `ts/src/yaml.ts` and `go/yaml.go`.
pub const VERSION: &str = "0.5.7";

/// The plugin name, which is also the key its options sit under.
const PLUGIN_NAME: &str = "yaml";

/// Set on the instance once the plugin has installed, so a re-application
/// during option resolution does not install the grammar twice.
const INSTALLED: &str = "yaml-installed";

/// Errors this crate returns are the engine's, re-exported so a caller
/// does not need `tabnas` in scope to name them.
pub use tabnas::TabnasError as YamlError;

// --- BEGIN EMBEDDED yaml-grammar.jsonic ---
const GRAMMAR_TEXT: &str = r##"
# YAML Grammar Definition
# Parsed by a standard Tabnas instance and passed to tabnas.grammar()
# Function references (@ prefixed) are resolved against the refs map.
# State handlers (bo/ao/bc/ac) remain wired in code, since they use
# closures over per-parse state (anchors, pendingAnchors, etc.).

{
  # Amend val rule: YAML indent/element-marker handling.
  rule: val: open: {
    alts: [
      # Doc-frame markers between docs mean an empty value here; back up so
      # the stream rule consumes the marker and starts the next document.
      { s: '#DS' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DE' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DR' b: 1 a: '@val-set-null' g: yaml }
      # Indent followed by content: push indent rule.
      { s: '#IN' c: '@val-indent-deeper' p: indent a: '@val-set-in-from-o0' g: yaml }
      # Same indent followed by element marker: list value at map level.
      { s: ['#IN' '#EL'] c: '@val-indent-eq-parent' p: yamlBlockList a: '@val-set-in-from-o0' g: yaml }
      # End of input means empty value.
      { s: '#ZZ' b: 1 a: '@val-set-null' g: yaml }
      # Same or lesser indent after a colon means empty value — backtrack.
      { s: '#IN' b: 1 u: { yamlEmpty: true } g: yaml }
      # This value is a list.
      { s: '#EL' p: yamlBlockList a: '@val-set-el-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: val: close: {
    alts: [
      # Doc-frame markers terminate val; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Indent rule: start for block content at a given indent.
  rule: indent: open: [
    # Key pair => map.
    { s: ['#KEY' '#CL'] p: map b: 2 g: yaml }
    # Element marker => list.
    { s: '#EL' p: list g: yaml }
    # Plain value after indent (for nested scalars).
    { s: '#KEY' a: '@indent-plain-value' g: yaml }
  ]

  # YAML block list: handles "- " sequences without consuming "[".
  rule: yamlBlockList: open: [
    # Element value is a key-value map: - key: val
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    # Default: push to val for the element's value.
    { p: val g: yaml }
  ]
  rule: yamlBlockList: close: [
    # Doc-frame markers terminate list; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    # Indent followed by element marker: next element at same level.
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: 'yaml,comma' }
    # Same or lesser indent: close list.
    { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    # Element marker at top level (no preceding newline).
    { s: '#EL' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#ZZ' b: 1 g: 'yaml,end' }
  ]

  # Subsequent elements in a yamlBlockList (via rotation).
  rule: yamlBlockElem: open: [
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    { p: val g: yaml }
  ]
  rule: yamlBlockElem: close: [
    # Doc-frame markers terminate elem; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    { s: '#EL' r: yamlBlockElem g: 'yaml,comma' }
    { s: '#ZZ' b: 1 g: 'yaml,end' }
  ]

  # Amend list rule: close on dedent or same-indent non-element.
  rule: list: close: {
    alts: [
      # Doc-frame markers terminate list; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-le-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Amend map rule: same-indent indent continues map with pair.
  rule: map: open: {
    alts: [
      { s: '#IN' c: '@o0-eq-in' r: pair g: yaml }
    ]
    inject: { append: false }
  }
  rule: map: close: {
    alts: [
      # Doc-frame markers terminate map; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # Amend pair rule: end of input ends pair; dedent closes, same-indent repeats.
  # Also handle YAML flow-mapping shapes Tabnas doesn't have natively:
  # - implicit null values: {a, b: c}  — KEY followed directly by CA or CB
  # - explicit-key marker:  {? k : v}  — leading #QM is consumed
  rule: pair: open: {
    alts: [
      { s: ['#KEY' '#CA'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#KEY' '#CB'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: val u: { pair: true } a: '@qm-pairkey' g: yaml }
      { s: ['#QM' '#KEY' '#CA'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CB'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: '#ZZ' b: 1 g: 'yaml,end' }
    ]
    inject: { append: false }
  }
  rule: pair: close: {
    alts: [
      # Doc-frame markers terminate pair; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: '#IN' c: '@t0-eq-in' r: pair g: 'yaml,comma' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
    ]
    inject: { append: false }
  }

  # yamlElemMap: "- key: val" patterns.
  rule: yamlElemMap: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemMap: close: [
    # Doc-frame markers terminate elem-map; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: 'yaml,comma' }
    { s: '#IN' b: 1 g: 'yaml,close' }
    { s: '#CA' b: 1 g: 'yaml,comma' }
    { s: '#CS' b: 1 g: 'yaml,close' }
    { s: '#CB' b: 1 g: 'yaml,close' }
    { s: '#ZZ' g: 'yaml,end' }
  ]

  # Additional pairs in a yamlElemMap.
  rule: yamlElemPair: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemPair: close: [
    # Doc-frame markers terminate elem-pair; back up for the stream rule.
    { s: '#DS' b: 1 g: 'yaml,end' }
    { s: '#DE' b: 1 g: 'yaml,end' }
    { s: '#DR' b: 1 g: 'yaml,end' }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: 'yaml,comma' }
    { s: '#IN' b: 1 g: 'yaml,close' }
    { s: '#CA' b: 1 g: 'yaml,comma' }
    { s: '#CS' b: 1 g: 'yaml,close' }
    { s: '#CB' b: 1 g: 'yaml,close' }
    { s: '#ZZ' g: 'yaml,end' }
  ]

  # Amend elem rule for YAML sequences ("- key: val" at top level of [ ... ]).
  # Also handle flow-sequence explicit-key entries: [? k : v] is a single-pair
  # map element. Eat the leading #QM, then back up KEY+CL so yamlElemMap
  # consumes them as a normal pair.
  rule: elem: open: {
    alts: [
      { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: elem: close: {
    alts: [
      # Doc-frame markers terminate elem; back up for the stream rule.
      { s: '#DS' b: 1 g: 'yaml,end' }
      { s: '#DE' b: 1 g: 'yaml,end' }
      { s: '#DR' b: 1 g: 'yaml,end' }
      { s: ['#IN' '#EL'] c: '@t0-eq-in' r: elem g: 'yaml,comma' }
      { s: '#IN' c: '@t0-eq-in' b: 1 g: 'yaml,close' }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: 'yaml,close' }
      { s: '#EL' r: elem g: 'yaml,comma' }
    ]
    inject: { append: false }
  }
}
"##;
// --- END EMBEDDED yaml-grammar.jsonic ---

/// The options this plugin takes.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct YamlOptions {
    /// When true, a parse returns `{meta, content}` instead of bare
    /// content. `meta` is a per-document `{directives, explicit, ended}`
    /// object for a single document, or an array of them for a stream.
    pub meta: bool,
}

impl YamlOptions {
    /// The option bag as the engine's plugin values.
    fn to_value(self) -> Value {
        let mut map = IndexMap::new();
        map.insert("meta".to_string(), Value::Bool(self.meta));
        Value::object(map)
    }
}

// ---------------------------------------------------------------------
// Value helpers
// ---------------------------------------------------------------------

/// Assign a rule's node: the Rust spelling of TypeScript `r.node = v`.
///
/// A pushed or replaced rule SHARES its parent's node cell, so writing
/// through `rule.node.borrow_mut()` would overwrite the parent's node
/// too. Assigning installs a fresh cell instead. The cell is borrowed
/// directly only to mutate a container the rule genuinely shares.
fn set_node(rule: &mut Rule, value: Value) {
    rule.node = Rc::new(RefCell::new(value));
}

fn map_insert(node: &mut Value, key: String, value: Value) {
    match node {
        Value::Object(map) => {
            Arc::make_mut(map).insert(key, value);
        }
        Value::MapRef(map) => {
            Arc::make_mut(map).value.insert(key, value);
        }
        _ => {}
    }
}

fn map_entries(node: &Value) -> Option<&IndexMap<String, Value>> {
    match node {
        Value::Object(map) => Some(map),
        Value::MapRef(map) => Some(&map.value),
        _ => None,
    }
}

fn list_push(node: &mut Value, value: Value) {
    match node {
        Value::Array(items) => Arc::make_mut(items).push(value),
        Value::ListRef(list) => Arc::make_mut(list).value.push(value),
        _ => {}
    }
}

/// Whether `typeof value === 'object'` would hold in JavaScript, with
/// `null` excluded as the canonical guards exclude it.
fn is_container(value: &Value) -> bool {
    matches!(
        value,
        Value::Object(_) | Value::Array(_) | Value::MapRef(_) | Value::ListRef(_) | Value::Text(_)
    )
}

/// The `JSON.parse(JSON.stringify(v))` the canonical implementation
/// copies an anchored value with: containers are rebuilt, an `undefined`
/// entry is dropped from an object and becomes null in an array, and a
/// non-finite number becomes null.
fn deep_copy(value: &Value) -> Value {
    match value {
        Value::Object(map) => {
            let mut out = IndexMap::new();
            for (key, entry) in map.iter() {
                if matches!(entry, Value::Undefined) {
                    continue;
                }
                out.insert(key.clone(), deep_copy(entry));
            }
            Value::object(out)
        }
        Value::MapRef(map) => {
            let mut out = IndexMap::new();
            for (key, entry) in map.value.iter() {
                if matches!(entry, Value::Undefined) {
                    continue;
                }
                out.insert(key.clone(), deep_copy(entry));
            }
            Value::object(out)
        }
        Value::Array(items) => Value::array(items.iter().map(deep_copy).collect()),
        Value::ListRef(list) => Value::array(list.value.iter().map(deep_copy).collect()),
        Value::Undefined => Value::Null,
        Value::Number(number) if !number.is_finite() => Value::Null,
        Value::Text(text) => Value::String(text.string.clone()),
        other => other.clone(),
    }
}

/// `String(value)` as JavaScript spells it, which is how a non-string key
/// becomes an object property name.
pub(crate) fn string_of(value: &Value) -> String {
    match value {
        Value::Undefined => "undefined".to_string(),
        Value::Null => "null".to_string(),
        Value::Bool(flag) => flag.to_string(),
        Value::Number(number) => js_number_to_string(*number),
        Value::String(text) => text.clone(),
        Value::Text(text) => text.string.clone(),
        Value::Array(items) => items
            .iter()
            .map(|item| match item {
                Value::Undefined | Value::Null => String::new(),
                other => string_of(other),
            })
            .collect::<Vec<_>>()
            .join(","),
        Value::ListRef(list) => string_of(&Value::array(list.value.clone())),
        Value::Object(_) | Value::MapRef(_) => "[object Object]".to_string(),
    }
}

/// `String(number)` as JavaScript spells it: the shortest digit string
/// that reads back as the same double, in plain decimal while the decimal
/// point stays inside `(-6, 21]` and in exponent form outside it.
fn js_number_to_string(number: f64) -> String {
    if number.is_nan() {
        return "NaN".to_string();
    }
    // Catches -0.0 as well: JavaScript spells both zeros "0".
    if number == 0.0 {
        return "0".to_string();
    }
    if number < 0.0 {
        return format!("-{}", js_number_to_string(-number));
    }
    if number.is_infinite() {
        return "Infinity".to_string();
    }
    let shortest = format!("{number:e}");
    let shortest_k = shortest
        .split_once('e')
        .map(|(mantissa, _)| mantissa.chars().filter(char::is_ascii_digit).count())
        .expect("a finite f64 always formats with an exponent");
    let exponential = format!("{:.*e}", shortest_k - 1, number);
    let (mantissa, exponent) = exponential
        .split_once('e')
        .expect("a finite f64 always formats with an exponent");
    let digits = mantissa
        .chars()
        .filter(|digit| *digit != '.')
        .collect::<String>();
    let digits = digits.trim_end_matches('0');
    let digits = if digits.is_empty() { "0" } else { digits };
    let k = digits.len() as i32;
    let n = exponent
        .parse::<i32>()
        .expect("a formatted exponent is an integer")
        + 1;
    if (k..=21).contains(&n) {
        let mut text = digits.to_string();
        text.push_str(&"0".repeat((n - k) as usize));
        text
    } else if (1..=21).contains(&n) {
        let point = n as usize;
        format!("{}.{}", &digits[..point], &digits[point..])
    } else if (-5..=0).contains(&n) {
        format!("0.{}{}", "0".repeat(-n as usize), digits)
    } else {
        let sign = if n - 1 < 0 { '-' } else { '+' };
        let power = (n - 1).abs();
        if k == 1 {
            format!("{digits}e{sign}{power}")
        } else {
            format!("{}.{}e{sign}{power}", &digits[..1], &digits[1..])
        }
    }
}

/// `Number(text)`, the conversion the unary plus in the canonical scalar
/// handler performs. `None` stands for `NaN`, which is how a plain scalar
/// stays text.
pub(crate) fn js_to_number(text: &str) -> Option<f64> {
    let trimmed = text.trim_matches(js_space);
    if trimmed.is_empty() {
        return Some(0.0);
    }
    if let Some(radix) = match trimmed.get(..2) {
        Some("0x" | "0X") => Some(16u32),
        Some("0o" | "0O") => Some(8),
        Some("0b" | "0B") => Some(2),
        _ => None,
    } {
        let digits = &trimmed[2..];
        if digits.is_empty() || !digits.chars().all(|d| d.is_digit(radix)) {
            return None;
        }
        let mut value = 0.0f64;
        for digit in digits.chars() {
            value = value * f64::from(radix) + f64::from(digit.to_digit(radix).unwrap_or(0));
        }
        return Some(value);
    }
    let (sign, body) = match trimmed.strip_prefix('-') {
        Some(rest) => (-1.0, rest),
        None => (1.0, trimmed.strip_prefix('+').unwrap_or(trimmed)),
    };
    if body == "Infinity" {
        return Some(sign * f64::INFINITY);
    }
    if !is_decimal_literal(body) {
        return None;
    }
    body.parse::<f64>().ok().map(|value| sign * value)
}

/// JavaScript's `\s`, which is also its `StrWhiteSpace`: the ASCII
/// blanks, the Unicode space separators, the two line separators and the
/// byte-order mark. Rust's own `char::is_whitespace` is the `White_Space`
/// property, which differs in both directions: it counts U+0085 and it
/// does not count U+FEFF. Both of those decide a scalar here.
pub(crate) fn js_space(character: char) -> bool {
    matches!(
        character,
        '\t' | '\n' | '\u{b}' | '\u{c}' | '\r' | ' ' | '\u{a0}' | '\u{1680}' | '\u{2000}'
            ..='\u{200a}'
                | '\u{2028}'
                | '\u{2029}'
                | '\u{202f}'
                | '\u{205f}'
                | '\u{3000}'
                | '\u{feff}'
    )
}

/// The ECMAScript `StrUnsignedDecimalLiteral` shape, which is narrower
/// than what Rust's own parser accepts: `inf`, `nan` and `1_0` are not
/// numbers in JavaScript.
fn is_decimal_literal(body: &str) -> bool {
    let bytes = body.as_bytes();
    let mut index = 0;
    let mut integral = 0;
    while index < bytes.len() && bytes[index].is_ascii_digit() {
        index += 1;
        integral += 1;
    }
    let mut fractional = 0;
    if index < bytes.len() && bytes[index] == b'.' {
        index += 1;
        while index < bytes.len() && bytes[index].is_ascii_digit() {
            index += 1;
            fractional += 1;
        }
    }
    if integral == 0 && fractional == 0 {
        return false;
    }
    if index < bytes.len() && (bytes[index] == b'e' || bytes[index] == b'E') {
        index += 1;
        if index < bytes.len() && (bytes[index] == b'+' || bytes[index] == b'-') {
            index += 1;
        }
        let mut exponent = 0;
        while index < bytes.len() && bytes[index].is_ascii_digit() {
            index += 1;
            exponent += 1;
        }
        if exponent == 0 {
            return false;
        }
    }
    index == bytes.len()
}

/// `parseInt(text, 10)`.
pub(crate) fn parse_int(text: &str) -> f64 {
    let trimmed = text.trim_start();
    let bytes = trimmed.as_bytes();
    let mut index = 0;
    if index < bytes.len() && (bytes[index] == b'+' || bytes[index] == b'-') {
        index += 1;
    }
    let start = index;
    while index < bytes.len() && bytes[index].is_ascii_digit() {
        index += 1;
    }
    if index == start {
        return f64::NAN;
    }
    trimmed[..index].parse::<f64>().unwrap_or(f64::NAN)
}

/// `parseFloat(text)`.
pub(crate) fn parse_float(text: &str) -> f64 {
    let trimmed = text.trim_start();
    let bytes = trimmed.as_bytes();
    let mut index = 0;
    if index < bytes.len() && (bytes[index] == b'+' || bytes[index] == b'-') {
        index += 1;
    }
    if trimmed[index..].starts_with("Infinity") {
        return if bytes.first() == Some(&b'-') {
            f64::NEG_INFINITY
        } else {
            f64::INFINITY
        };
    }
    let mut digits = 0;
    while index < bytes.len() && bytes[index].is_ascii_digit() {
        index += 1;
        digits += 1;
    }
    if index < bytes.len() && bytes[index] == b'.' {
        index += 1;
        while index < bytes.len() && bytes[index].is_ascii_digit() {
            index += 1;
            digits += 1;
        }
    }
    if digits == 0 {
        return f64::NAN;
    }
    let mantissa_end = index;
    if index < bytes.len() && (bytes[index] == b'e' || bytes[index] == b'E') {
        let mut probe = index + 1;
        if probe < bytes.len() && (bytes[probe] == b'+' || bytes[probe] == b'-') {
            probe += 1;
        }
        let exponent_start = probe;
        while probe < bytes.len() && bytes[probe].is_ascii_digit() {
            probe += 1;
        }
        if probe > exponent_start {
            index = probe;
        }
    }
    trimmed[..index.max(mantissa_end)]
        .parse::<f64>()
        .unwrap_or(f64::NAN)
}

/// One Unicode scalar from a hexadecimal escape's digits. A code point
/// with no scalar of its own, which is every lone surrogate, folds to the
/// replacement character, as the engine's own string lexer folds one.
pub(crate) fn code_point(digits: &str) -> char {
    u32::from_str_radix(digits, 16)
        .ok()
        .and_then(char::from_u32)
        .unwrap_or('\u{fffd}')
}

/// The YAML value keywords, and the three non-finite numbers that have
/// no JSON spelling.
pub(crate) fn yaml_keyword(text: &str) -> Option<Value> {
    Some(match text {
        "true" | "True" | "TRUE" | "yes" | "Yes" | "YES" | "on" | "On" | "ON" => Value::Bool(true),
        "false" | "False" | "FALSE" | "no" | "No" | "NO" | "off" | "Off" | "OFF" => {
            Value::Bool(false)
        }
        "null" | "Null" | "NULL" | "~" => Value::Null,
        ".inf" | ".Inf" | ".INF" => Value::Number(f64::INFINITY),
        "-.inf" | "-.Inf" | "-.INF" => Value::Number(f64::NEG_INFINITY),
        ".nan" | ".NaN" | ".NAN" => Value::Number(f64::NAN),
        _ => return None,
    })
}

// ---------------------------------------------------------------------
// Grammar references
// ---------------------------------------------------------------------

/// The value a token carries as a mapping key, resolving an alias marker
/// against the anchors recorded so far.
fn extract_key(token: &Token, context: &Context) -> Value {
    if token.tin == TIN_VL {
        if let Value::Object(map) = &token.val {
            if let Some(Value::String(name)) = map.get("__yamlAlias") {
                return match state::map_get(context, state::ANCHORS, name) {
                    Some(value) if !matches!(value, Value::Undefined) => value,
                    _ => Value::String(format!("*{name}")),
                };
            }
        }
    }
    if token.tin == TIN_ST || token.tin == TIN_TX {
        token.val.clone()
    } else {
        Value::String(token.src.as_str().to_string())
    }
}

fn token_key(rule: &Rule, index: usize, context: &Context) -> Value {
    let token = if index == 0 { rule.o0() } else { rule.o1() };
    token.map_or(Value::Undefined, |token| extract_key(token, context))
}

fn counter(rule: &Rule, name: &str) -> Option<i32> {
    rule.n.get(name).copied()
}

fn token_number(value: &Value) -> Option<f64> {
    match value {
        Value::Number(number) => Some(*number),
        _ => None,
    }
}

fn t0_number(context: &Context) -> Option<f64> {
    context.t0().and_then(|token| token_number(&token.val))
}

fn register_refs(parser: &mut Tabnas) {
    // The two lexer matchers.
    parser.imperative_lex_match_ref(lex::MATCHER, lex::yaml_matcher);
    parser.imperative_lex_match_ref(text::MATCHER, text::yaml_text);

    // Conditions.
    parser.alt_condition("@val-indent-deeper", |rule, context| {
        let Some(t0) = t0_number(context) else {
            return false;
        };
        if let Some(Value::Number(list_in)) = rule.k.get("yamlListIn") {
            if t0 <= *list_in {
                return false;
            }
        }
        match rule.k.get("yamlIn") {
            Some(Value::Number(parent)) => t0 > *parent,
            _ => true,
        }
    });
    parser.alt_condition("@val-indent-eq-parent", |rule, context| {
        let Some(t0) = t0_number(context) else {
            return false;
        };
        matches!(rule.k.get("yamlIn"), Some(Value::Number(parent)) if t0 == *parent)
    });
    parser.alt_condition("@t0-eq-in", |rule, context| {
        matches!((t0_number(context), counter(rule, "in")),
            (Some(t0), Some(indent)) if t0 == f64::from(indent))
    });
    parser.alt_condition("@t0-le-in", |rule, context| {
        matches!((t0_number(context), counter(rule, "in")),
            (Some(t0), Some(indent)) if t0 <= f64::from(indent))
    });
    parser.alt_condition("@t0-lt-in", |rule, context| {
        matches!((t0_number(context), counter(rule, "in")),
            (Some(t0), Some(indent)) if t0 < f64::from(indent))
    });
    parser.alt_condition("@o0-eq-in", |rule, _context| {
        let open = rule.o0().and_then(|token| token_number(&token.val));
        matches!((open, counter(rule, "in")),
            (Some(value), Some(indent)) if value == f64::from(indent))
    });
    parser.alt_condition("@t0-eq-map-in", |rule, context| {
        matches!((t0_number(context), rule.k.get("yamlMapIn")),
            (Some(t0), Some(Value::Number(indent))) if t0 == *indent)
    });

    // Actions.
    parser.action_with_context("@val-set-in-from-o0", |rule, _context| {
        if let Some(value) = rule.o0().and_then(|token| token_number(&token.val)) {
            rule.n_mut().insert("in".to_string(), value as i32);
        }
        Ok(())
    });
    parser.action_with_context("@val-set-null", |rule, _context| {
        set_node(rule, Value::Null);
        Ok(())
    });
    parser.action_with_context("@val-set-el-in", |rule, _context| {
        let column = rule.o0().map_or(0, |token| token.site.ci);
        rule.n_mut().insert("in".to_string(), column as i32 - 1);
        Ok(())
    });
    parser.action_with_context("@indent-plain-value", |rule, _context| {
        let node = rule.o0().map_or(Value::Undefined, |token| {
            if token.tin == TIN_ST || token.tin == TIN_TX {
                token.val.clone()
            } else {
                Value::String(token.src.as_str().to_string())
            }
        });
        set_node(rule, node);
        Ok(())
    });
    parser.action_with_context("@set-map-in", |rule, _context| {
        // `r.k.yamlMapIn = r.n.in + 2`. With no `in` counter that is
        // `undefined + 2`, which is NaN, and NaN never equals the indent
        // `@t0-eq-map-in` compares it to. A zero here would instead make
        // the comparison true at the outermost indent.
        let indent = counter(rule, "in").map_or(f64::NAN, |value| f64::from(value + 2));
        rule.k_mut()
            .insert("yamlMapIn".to_string(), Value::Number(indent));
        Ok(())
    });
    parser.action_with_context("@elem-key", |rule, context| {
        let key = token_key(rule, 0, context);
        rule.u_mut().insert("key".to_string(), key);
        Ok(())
    });
    parser.action_with_context("@implicit-null-pair", |rule, context| {
        let key = token_key(rule, 0, context);
        rule.u_mut().insert("key".to_string(), key.clone());
        map_insert(&mut rule.node.borrow_mut(), string_of(&key), Value::Null);
        Ok(())
    });
    parser.action_with_context("@qm-pairkey", |rule, context| {
        let key = token_key(rule, 1, context);
        rule.u_mut().insert("key".to_string(), key);
        Ok(())
    });
    parser.action_with_context("@qm-implicit-null-pair", |rule, context| {
        let key = token_key(rule, 1, context);
        rule.u_mut().insert("key".to_string(), key.clone());
        map_insert(&mut rule.node.borrow_mut(), string_of(&key), Value::Null);
        Ok(())
    });

    // The stream rule's actions.
    parser.action_with_context("@yaml-apply-directive", |rule, context| {
        apply_directive(rule, context);
        Ok(())
    });
    parser.action_with_context("@yaml-mark-explicit", |_rule, context| {
        ensure_meta(context);
        set_meta_flag(context, "explicit", true);
        Ok(())
    });
    parser.action_with_context("@yaml-accum-doc", |rule, context| {
        accumulate_document(rule, context);
        Ok(())
    });
    parser.action_with_context("@yaml-accum-and-directive", |rule, context| {
        accumulate_document(rule, context);
        apply_directive(rule, context);
        Ok(())
    });
    parser.action_with_context("@yaml-finalize-stream", |rule, context| {
        finalize_stream(rule, context);
        Ok(())
    });
}

// ---------------------------------------------------------------------
// The stream rule's bookkeeping
// ---------------------------------------------------------------------

fn ensure_meta(context: &mut Context) {
    if context.u.get(state::CUR_META).is_none() {
        let mut meta = IndexMap::new();
        meta.insert("directives".to_string(), Value::array(Vec::new()));
        meta.insert("explicit".to_string(), Value::Bool(false));
        meta.insert("ended".to_string(), Value::Bool(false));
        context
            .u
            .insert(state::CUR_META.to_string(), Value::object(meta));
    }
}

fn set_meta_flag(context: &mut Context, field: &str, value: bool) {
    ensure_meta(context);
    state::map_set(
        context,
        state::CUR_META,
        field.to_string(),
        Value::Bool(value),
    );
}

fn flush_meta(context: &mut Context, ended: bool) {
    ensure_meta(context);
    if ended {
        set_meta_flag(context, "ended", true);
    }
    let meta = context
        .u
        .shift_remove(state::CUR_META)
        .unwrap_or(Value::Undefined);
    state::list_push(context, state::METAS, meta);
}

fn apply_directive(rule: &mut Rule, context: &mut Context) {
    let source = rule.o0().map_or(String::new(), |token| {
        let src = token.src.as_str();
        if src.is_empty() {
            match &token.val {
                Value::String(text) => text.clone(),
                _ => String::new(),
            }
        } else {
            src.to_string()
        }
    });
    if let Some((handle, prefix)) = tag_directive(&source) {
        state::map_set(context, state::TAG_HANDLES, handle, Value::String(prefix));
    }
    ensure_meta(context);
    let mut directives = match state::map_get(context, state::CUR_META, "directives") {
        Some(Value::Array(items)) => (*items).clone(),
        _ => Vec::new(),
    };
    directives.push(Value::String(source));
    state::map_set(
        context,
        state::CUR_META,
        "directives".to_string(),
        Value::array(directives),
    );
}

/// `%TAG <handle> <prefix>`, the only directive that changes a parse.
fn tag_directive(source: &str) -> Option<(String, String)> {
    let rest = source.strip_prefix("%TAG")?;
    if !rest.starts_with(|character: char| character.is_whitespace()) {
        return None;
    }
    let mut fields = rest.split_whitespace();
    let handle = fields.next()?;
    let prefix = fields.next()?;
    Some((handle.to_string(), prefix.to_string()))
}

fn child_document(rule: &Rule) -> Option<Value> {
    (!rule.child_node.is_undefined()).then(|| rule.child_node.clone())
}

fn accumulate_document(rule: &mut Rule, context: &mut Context) {
    let value = child_document(rule).unwrap_or(Value::Null);
    state::list_push(context, state::DOCS, value);
    // The close token says whether this document ended with `...`.
    let ended = rule.c0().is_some_and(|token| token.name.as_ref() == "#DE");
    flush_meta(context, ended);
}

fn finalize_stream(rule: &mut Rule, context: &mut Context) {
    if let Some(value) = child_document(rule) {
        state::list_push(context, state::DOCS, value);
        flush_meta(context, false);
    } else if context.u.get(state::CUR_META).is_some() {
        // The final document was opened explicitly but its value
        // coalesced away: a bare `---` at end of stream, or the trailing
        // empty document of `---\n---\n---`. Restore the null the other
        // empty documents are given.
        state::list_push(context, state::DOCS, Value::Null);
        flush_meta(context, false);
    }

    let documents = state::list_all(context, state::DOCS);
    let content = match documents.len() {
        0 => Value::Undefined,
        1 => documents[0].clone(),
        _ => Value::array(documents),
    };

    let want_meta = matches!(
        context
            .options
            .plugin
            .get(PLUGIN_NAME)
            .and_then(|options| match options {
                Value::Object(map) => map.get("meta").cloned(),
                _ => None,
            }),
        Some(Value::Bool(true))
    );

    let result = if want_meta {
        let metas = state::list_all(context, state::METAS);
        let meta = match metas.len() {
            0 => Value::Undefined,
            1 => metas[0].clone(),
            _ => Value::array(metas),
        };
        let mut wrapper = IndexMap::new();
        wrapper.insert("meta".to_string(), meta);
        wrapper.insert("content".to_string(), content);
        Value::object(wrapper)
    } else {
        content
    };

    // A rotation through `r: stream` shares the start rule's node cell,
    // which is the parse root, so writing THROUGH the cell is the Rust
    // spelling of the canonical `ctx.root().node = result`.
    *rule.node.borrow_mut() = result;
}

// ---------------------------------------------------------------------
// Rule lifecycle
// ---------------------------------------------------------------------

fn wire_rules(parser: &mut Tabnas) {
    parser.define_rule("val", |spec| {
        spec.add_ao(|rule, context| {
            let pending = state::list_take(context, state::PENDING_ANCHORS);
            if !pending.is_empty() {
                let open_node = rule.node.borrow().clone();
                rule.u_mut()
                    .insert("yamlAnchors".to_string(), Value::array(pending));
                rule.u_mut()
                    .insert("yamlAnchorOpenNode".to_string(), open_node);
            }
        });
        spec.add_bc(|rule, _context| {
            if matches!(rule.u.get("yamlEmpty"), Some(Value::Bool(true))) {
                set_node(rule, Value::Undefined);
            }
        });
        spec.add_ac(|rule, context| {
            // An alias whose anchor was not yet recorded when it lexed.
            let alias = match &*rule.node.borrow() {
                Value::Object(map) => match map.get("__yamlAlias") {
                    Some(Value::String(name)) => Some(name.clone()),
                    _ => None,
                },
                _ => None,
            };
            if let Some(name) = alias {
                let resolved = state::map_get(context, state::ANCHORS, &name).map_or(
                    Value::Undefined,
                    |value| {
                        if is_container(&value) {
                            deep_copy(&value)
                        } else {
                            value
                        }
                    },
                );
                set_node(rule, resolved);
            }

            let Some(Value::Array(anchors)) = rule.u.get("yamlAnchors").cloned() else {
                return;
            };
            let open_node = rule
                .u
                .get("yamlAnchorOpenNode")
                .cloned()
                .unwrap_or(Value::Undefined);
            let node = rule.node.borrow().clone();
            for anchor in anchors.iter() {
                let Value::Object(entry) = anchor else {
                    continue;
                };
                let inline = matches!(entry.get("i"), Some(Value::Bool(true)));
                let Some(Value::String(name)) = entry.get("n") else {
                    continue;
                };
                // An inline anchor that named a scalar keeps that scalar:
                // the container this val ended up holding belongs to a
                // later, deeper value.
                if inline
                    && !matches!(open_node, Value::Undefined | Value::Null)
                    && !is_container(&open_node)
                    && is_container(&node)
                {
                    continue;
                }
                let value = if is_container(&node) {
                    deep_copy(&node)
                } else {
                    node.clone()
                };
                state::map_set(context, state::ANCHORS, name.clone(), value);
            }
        });
    });

    parser.define_rule("indent", |spec| {
        spec.add_bc(|rule, _context| {
            if !rule.child_node.is_undefined() {
                let child = rule.child_node.clone();
                set_node(rule, child);
            }
        });
    });

    parser.define_rule("yamlBlockList", |spec| {
        spec.add_bo(|rule, _context| {
            // A fresh cell, as `r.node = []` installs a fresh array.
            // Every rotation of this rule shares the cell it installs, so
            // the elements all land in one list, which is what the
            // canonical port passes along in `k.yamlBlockArr`.
            set_node(rule, Value::array(Vec::new()));
            set_indent_key(rule, "yamlListIn");
        });
        spec.add_bc(|rule, _context| {
            let value = if rule.child_node.is_undefined() {
                Value::Null
            } else {
                rule.child_node.clone()
            };
            list_push(&mut rule.node.borrow_mut(), value);
        });
    });

    parser.define_rule("yamlBlockElem", |spec| {
        spec.add_bc(|rule, _context| {
            let value = if rule.child_node.is_undefined() {
                Value::Null
            } else {
                rule.child_node.clone()
            };
            list_push(&mut rule.node.borrow_mut(), value);
        });
    });

    parser.define_rule("list", |spec| {
        spec.add_bo(|rule, _context| {
            set_indent_key(rule, "yamlListIn");
            // A YAML block sequence reaches `list` through the indent
            // rule's `#EL` alternate, with no `[` and no implicit-list
            // promotion, so neither of jsonic's array builders runs and
            // the node is still the inherited parent container. Allocate
            // the array here so the element pushes land in a list.
            if rule
                .parent_rule
                .as_ref()
                .is_some_and(|parent| parent.name.as_ref() == "indent")
            {
                set_node(rule, Value::array(Vec::new()));
            }
        });
    });

    parser.define_rule("map", |spec| {
        spec.add_bo(|rule, _context| {
            if counter(rule, "in").is_none() {
                rule.n_mut().insert("in".to_string(), 0);
            }
            set_indent_key(rule, "yamlIn");
        });
        spec.add_ac(|rule, _context| {
            apply_merge_keys(rule);
        });
    });

    parser.define_rule("yamlElemMap", |spec| {
        spec.add_bo(|rule, _context| {
            set_node(rule, Value::object(IndexMap::new()));
        });
        spec.add_bc(|rule, _context| {
            store_elem_pair(rule);
        });
    });

    parser.define_rule("yamlElemPair", |spec| {
        spec.add_bc(|rule, _context| {
            store_elem_pair(rule);
        });
    });
}

/// `r.k.<name> = r.n.in`, including when there is no `in` counter: the
/// canonical assignment writes `undefined` there, and every reader takes
/// that as "no enclosing indent". Writing a zero instead would make a
/// block sequence at the outermost indent look nested inside itself.
fn set_indent_key(rule: &mut Rule, name: &str) {
    let indent =
        counter(rule, "in").map_or(Value::Undefined, |value| Value::Number(f64::from(value)));
    rule.k_mut().insert(name.to_string(), indent);
}

fn store_elem_pair(rule: &mut Rule) {
    let Some(key) = rule.u.get("key").cloned() else {
        return;
    };
    if matches!(key, Value::Null | Value::Undefined) {
        return;
    }
    // A pair whose value never arrived (`- i: ` at end of source) is
    // assigned `undefined` in the canonical implementation, which makes
    // the key present but drops it from every serialization. This
    // engine's values turn `Undefined` into null on the way out, so the
    // entry is left out instead and the serialized map agrees.
    if rule.child_node.is_undefined() {
        return;
    }
    let child = rule.child_node.clone();
    map_insert(&mut rule.node.borrow_mut(), string_of(&key), child);
}

/// Resolve a `<<` merge key in place: the merged entries are appended
/// after the explicit ones, and an explicit key always wins.
fn apply_merge_keys(rule: &mut Rule) {
    let mut node = rule.node.borrow_mut();
    let Some(entries) = map_entries(&node) else {
        return;
    };
    let Some(merge) = entries.get("<<").cloned() else {
        return;
    };
    let sources = match &merge {
        Value::Array(items) => (**items).clone(),
        Value::ListRef(list) => list.value.clone(),
        other => vec![other.clone()],
    };
    match &mut *node {
        Value::Object(map) => {
            Arc::make_mut(map).shift_remove("<<");
        }
        Value::MapRef(map) => {
            Arc::make_mut(map).value.shift_remove("<<");
        }
        _ => {}
    }
    for source in sources {
        let Some(source_entries) = map_entries(&source) else {
            continue;
        };
        let additions: Vec<(String, Value)> = source_entries
            .iter()
            .filter(|(key, _)| {
                map_entries(&node).is_some_and(|entries| !entries.contains_key(*key))
            })
            .map(|(key, value)| (key.clone(), value.clone()))
            .collect();
        for (key, value) in additions {
            map_insert(&mut node, key, value);
        }
    }
}

// ---------------------------------------------------------------------
// Installation
// ---------------------------------------------------------------------

/// The lexer and option overlay this plugin needs, plus the `stream`
/// rule, which the canonical implementation also declares in code rather
/// than in the shared grammar file.
fn options_document() -> serde_json::Value {
    json!({
        "rule": {
            "stream": {
                "open": [
                    // A directive line: apply it, then look again.
                    { "s": "#DR", "a": "@yaml-apply-directive", "r": "stream", "g": "yaml" },
                    // An explicit document start: push val for its content.
                    { "s": "#DS", "a": "@yaml-mark-explicit", "p": "val", "g": "yaml" },
                    // A `...` with no document open terminates nothing, so
                    // it produces no document. Consume it and look again.
                    { "s": "#DE", "r": "stream", "g": "yaml" },
                    // End of source: close immediately.
                    { "s": "#ZZ", "b": 1, "g": "yaml" },
                    // The implicit first document.
                    { "p": "val", "g": "yaml" }
                ],
                "close": [
                    { "s": "#ZZ", "a": "@yaml-finalize-stream", "g": "yaml" },
                    { "s": "#DR", "a": "@yaml-accum-and-directive", "r": "stream", "g": "yaml" },
                    { "s": "#DE", "a": "@yaml-accum-doc", "r": "stream", "g": "yaml" },
                    { "s": "#DS", "b": 1, "a": "@yaml-accum-doc", "r": "stream", "g": "yaml" }
                ]
            }
        },
        "options": {
            // A bare colon is not a YAML token; `: ` is, and the matcher
            // emits it.
            "fixed": { "token": { "#CL": null } },
            // A colon still ends unquoted text.
            "ender": ":",
            // YAML's quoting is not the engine's: both forms are lexed by
            // the YAML matcher, and a backtick is not a quote at all.
            "string": { "chars": "" },
            "lex": {
                "match": {
                    // Below the first built-in band, so YAML syntax is
                    // seen before the fixed-token matcher.
                    "yaml": { "order": 5e5, "make": "@yaml-matcher" },
                    // Between the number matcher and the text matcher,
                    // which is where `options.text.check` runs.
                    "yamlText": { "order": 7.5e6, "make": "@yaml-text" }
                }
            },
            // A document stream is the entry point, and it opens into the
            // shared `val` rule.
            "rule": { "start": "stream" }
        }
    })
}

/// jsonic's numbers are doubles, so `b: 2` arrives as `2.0`; the
/// alternate decoder reads an integer field as an integer. Put every
/// whole number back into integer form.
fn integral_numbers(value: serde_json::Value) -> serde_json::Value {
    match value {
        serde_json::Value::Number(number) => match number.as_f64() {
            Some(float) if float.fract() == 0.0 && float.abs() < 9.0e15 => json!(float as i64),
            _ => serde_json::Value::Number(number),
        },
        serde_json::Value::Array(items) => {
            serde_json::Value::Array(items.into_iter().map(integral_numbers).collect())
        }
        serde_json::Value::Object(fields) => serde_json::Value::Object(
            fields
                .into_iter()
                .map(|(key, value)| (key, integral_numbers(value)))
                .collect(),
        ),
        other => other,
    }
}

/// The grammar document, parsed from the embedded jsonic text exactly as
/// the TypeScript and Go ports parse their copies of it.
fn grammar_document() -> Result<serde_json::Value, PluginError> {
    let parsed = tabnas_jsonic::parse(GRAMMAR_TEXT)
        .map_err(|error| PluginError(format!("yaml: cannot parse the grammar text: {error}")))?;
    let document = integral_numbers(parsed.to_json());
    if document
        .get("rule")
        .and_then(serde_json::Value::as_object)
        .is_none()
    {
        return Err(PluginError(
            "yaml: the grammar text has no `rule` table".to_string(),
        ));
    }
    Ok(document)
}

/// Install YAML on a jsonic-enabled `parser`.
///
/// ```
/// fn main() -> Result<(), Box<dyn std::error::Error>> {
///     let mut parser = tabnas_jsonic::make();
///     tabnas_yaml::yaml(&mut parser, &tabnas_yaml::YamlOptions::default())?;
///     assert_eq!(parser.parse("a: 1")?.to_string(), r#"{"a":1}"#);
///     Ok(())
/// }
/// ```
pub fn yaml(parser: &mut Tabnas, options: &YamlOptions) -> Result<(), PluginError> {
    // Guard against re-entry while options are re-applied, which would
    // install the grammar twice.
    if parser.decoration::<bool>(INSTALLED).is_some() {
        return Ok(());
    }
    // This plugin reshapes jsonic's rules rather than declaring a whole
    // grammar, so an instance without them earns a clear refusal now
    // instead of a grammar that can parse nothing later.
    if !parser.rule_names().iter().any(|name| name == "val") {
        return Err(PluginError(
            "yaml: the jsonic grammar is not installed on this instance; \
             install tabnas_jsonic first"
                .to_string(),
        ));
    }
    parser.decorate(INSTALLED, true);
    parser.set_plugin_options(PLUGIN_NAME, options.to_value());

    // The tokens the matcher emits by name, allocated before any grammar
    // names them.
    for name in ["#IN", "#EL", "#QM", "#DS", "#DE", "#DR"] {
        parser.token(name);
    }

    register_refs(parser);

    let spec = GrammarSpec::from_value(options_document())
        .map_err(|error| PluginError(format!("yaml: invalid option document: {error}")))?;
    parser
        .grammar(&spec)
        .map_err(|error| PluginError(format!("yaml: cannot apply the options: {error}")))?;

    let spec = GrammarSpec::from_value(grammar_document()?)
        .map_err(|error| PluginError(format!("yaml: invalid grammar document: {error}")))?;
    parser
        .grammar(&spec)
        .map_err(|error| PluginError(format!("yaml: cannot apply the grammar: {error}")))?;

    wire_rules(parser);
    Ok(())
}

/// The plugin form of [`yaml`], for [`Tabnas::use_plugin`]. Installed
/// this way the grammar is re-applied to derived instances, as every
/// native plugin is.
///
/// ```
/// fn main() -> Result<(), Box<dyn std::error::Error>> {
///     let mut parser = tabnas_jsonic::make();
///     parser.use_plugin(tabnas_yaml::plugin(), None)?;
///     assert_eq!(parser.parse("- a\n- b")?.to_string(), r#"["a","b"]"#);
///     Ok(())
/// }
/// ```
pub fn plugin() -> Plugin {
    Plugin::new(PLUGIN_NAME, |parser, options| {
        let meta = matches!(
            match options {
                Value::Object(map) => map.get("meta").cloned(),
                _ => None,
            },
            Some(Value::Bool(true))
        );
        yaml(parser, &YamlOptions { meta })
    })
    .with_defaults(YamlOptions::default().to_value())
}

/// Build a YAML parser with caller options.
///
/// ```
/// fn main() -> Result<(), Box<dyn std::error::Error>> {
///     let parser = tabnas_yaml::make_with(tabnas_yaml::YamlOptions { meta: true });
///     let value = parser.parse("---\na: 1\n...")?;
///     assert_eq!(
///         value.to_string(),
///         r#"{"meta":{"directives":[],"explicit":true,"ended":true},"content":{"a":1}}"#
///     );
///     Ok(())
/// }
/// ```
#[must_use]
pub fn make_with(options: YamlOptions) -> Tabnas {
    let mut parser = tabnas_jsonic::make();
    parser
        .use_plugin(plugin(), Some(options.to_value()))
        .expect("the yaml grammar documents are fixed and valid");
    parser
}

/// Build a YAML parser with the default options.
///
/// ```
/// fn main() -> Result<(), Box<dyn std::error::Error>> {
///     let parser = tabnas_yaml::make();
///     assert_eq!(parser.parse("a:\n  b: 1")?.to_string(), r#"{"a":{"b":1}}"#);
///     Ok(())
/// }
/// ```
#[must_use]
pub fn make() -> Tabnas {
    make_with(YamlOptions::default())
}

/// Parse a YAML source string with the shared default parser.
///
/// The parser is built once, on first use, and reused after that, as the
/// other two runtimes do. Reuse is safe: [`Tabnas::parse`] takes `&self`
/// and builds a fresh parse context per call, and every piece of
/// per-parse state this grammar keeps lives in that context.
///
/// ```
/// fn main() -> Result<(), Box<dyn std::error::Error>> {
///     let value = tabnas_yaml::parse("- 1\n- 2\n")?;
///     assert_eq!(value.to_string(), "[1,2]");
///     Ok(())
/// }
/// ```
pub fn parse(src: &str) -> Result<Value, YamlError> {
    static DEFAULT: OnceLock<Tabnas> = OnceLock::new();
    DEFAULT.get_or_init(make).parse(src)
}
