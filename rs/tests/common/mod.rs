// Shared test helpers. Cargo compiles this module into EVERY integration
// test binary, so an item only one binary uses is dead code in the
// others; the allow keeps that from being a warning rather than hiding
// anything real.
#![allow(dead_code)]

use std::path::{Path, PathBuf};

use tabnas_support::Value as Expect;

/// The repository's shared fixture directory.
pub fn spec_dir() -> PathBuf {
    tabnas_support::find_spec_dir(Some(Path::new(env!("CARGO_MANIFEST_DIR"))))
        .expect("test/spec sits at the repository root")
}

/// The repository root, found by walking up from this crate.
pub fn repo_root() -> PathBuf {
    spec_dir()
        .parent()
        .and_then(Path::parent)
        .expect("test/spec has a grandparent")
        .to_path_buf()
}

/// A parse result in the fixture data model, with YAML's non-finite
/// numbers written the way the fixtures write them.
///
/// The canonical suites do the same two steps: the TypeScript runner's
/// `canon` hook rewrites an infinite or not-a-number value as a marker
/// string, and the Go runner's `jsonFlatten` then renders the result as
/// plain JSON. Here one walk does both, because the engine's own
/// `to_json` has nowhere to put a non-finite number and would turn it
/// into null before the marker could be written.
pub fn canon(value: &tabnas::Value) -> Expect {
    match value {
        tabnas::Value::Undefined => Expect::Undefined,
        tabnas::Value::Null => Expect::Null,
        tabnas::Value::Bool(flag) => Expect::Bool(*flag),
        tabnas::Value::Number(number) => {
            if number.is_nan() {
                Expect::String("@@NaN".to_string())
            } else if *number == f64::INFINITY {
                Expect::String("@@Infinity".to_string())
            } else if *number == f64::NEG_INFINITY {
                Expect::String("@@-Infinity".to_string())
            } else {
                Expect::Number(*number)
            }
        }
        tabnas::Value::String(text) => Expect::String(text.clone()),
        tabnas::Value::Text(text) => Expect::String(text.string.clone()),
        tabnas::Value::Array(items) => Expect::Array(items.iter().map(canon).collect()),
        tabnas::Value::ListRef(list) => Expect::Array(list.value.iter().map(canon).collect()),
        tabnas::Value::Object(entries) => Expect::Object(
            entries
                .iter()
                .map(|(key, value)| (key.clone(), canon(value)))
                .collect(),
        ),
        tabnas::Value::MapRef(map) => Expect::Object(
            map.value
                .iter()
                .map(|(key, value)| (key.clone(), canon(value)))
                .collect(),
        ),
    }
}

/// A parse result as plain JSON, for the in-language assertions. Whole
/// numbers render as integers, which is what a JSON round-trip gives in
/// the other two runtimes.
pub fn plain(value: &tabnas::Value) -> serde_json::Value {
    whole(value.to_json())
}

pub fn whole(value: serde_json::Value) -> serde_json::Value {
    use serde_json::Value;
    match value {
        Value::Number(number) => match number.as_f64() {
            Some(float) if float.fract() == 0.0 && float.abs() < 9.0e15 => {
                Value::Number((float as i64).into())
            }
            _ => Value::Number(number),
        },
        Value::Array(items) => Value::Array(items.into_iter().map(whole).collect()),
        Value::Object(entries) => Value::Object(
            entries
                .into_iter()
                .map(|(key, value)| (key, whole(value)))
                .collect(),
        ),
        other => other,
    }
}

/// Parse with the shared default parser and fail loudly.
pub fn parse(src: &str) -> tabnas::Value {
    tabnas_yaml::parse(src).unwrap_or_else(|error| panic!("parse {src:?}: {error}"))
}

/// Parse and render as plain JSON.
pub fn json(src: &str) -> serde_json::Value {
    plain(&parse(src))
}

/// The keys of a parsed mapping, in source order.
pub fn keys(value: &tabnas::Value) -> Vec<String> {
    match value {
        tabnas::Value::Object(entries) => entries.keys().cloned().collect(),
        tabnas::Value::MapRef(map) => map.value.keys().cloned().collect(),
        other => panic!("expected a mapping, got {other:?}"),
    }
}

/// Assert a source parses to the given JSON.
#[track_caller]
pub fn expect(src: &str, want: serde_json::Value) {
    let got = json(src);
    assert_eq!(got, whole(want), "input {src:?}");
}
