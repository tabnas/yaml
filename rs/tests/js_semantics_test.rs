// Where JavaScript's own string semantics decide a parse.
//
// The canonical plugin is JavaScript, so its character classes are
// JavaScript's: `\s` is the `White_Space` property MINUS U+0085 and PLUS
// U+FEFF, `\w` and `\b` are ASCII, and a string is a sequence of UTF-16
// code units rather than of Unicode scalars. Rust's own predicates
// (`char::is_whitespace`, `char::is_alphanumeric`, `str::trim`) and its
// scalar strings answer differently on exactly those points, so every
// port of a canonical `trim`, `\s`, `\w`, `\b` or `String.fromCharCode`
// has to spell the JavaScript rule out. `crate::js_space` is that
// spelling for whitespace.
//
// None of these is a shared fixture row: the Go port answers each of
// them with Go's native semantics, so a row in `test/spec/*.tsv` would
// be red there. The comment on each case records what Go printed, and
// `../DIVERGENCE.md` carries the two tables. The one case of this shape
// that Go DOES get right is a fixture row, in
// `test/spec/complex-keys.tsv`.

mod common;

use common::{expect, json, plain};
use serde_json::json as j;

// ===== TAGGED NUMBERS =====

/// `!!int` and `!!float` are `parseInt` and `parseFloat`, which skip
/// JavaScript's whitespace and no other. A byte-order mark is whitespace
/// there and not in Rust; a next-line character is whitespace in Rust
/// and not there.
///
/// Measured. `a: !!float <U+FEFF>1`: TypeScript `1` (ts/src/yaml.ts run
/// under Node), Go `"﻿1"`, Rust `1`. `a: !!float <U+0085>1`:
/// TypeScript `NaN`, Go `"1"`, Rust `NaN`.
#[test]
fn a_tagged_number_skips_javascript_whitespace() {
    expect("a: !!float \u{feff}1", j!({"a": 1}));
    expect("a: !!int \u{feff}1", j!({"a": 1}));
    // `parseFloat` and `parseInt` both give NaN, which the engine has no
    // JSON spelling for and renders as null.
    expect("a: !!float \u{85}1", j!({"a": null}));
    expect("a: !!int \u{85}1", j!({"a": null}));
    for src in ["a: !!float \u{85}1", "a: !!int \u{85}1"] {
        let value = common::parse(src);
        let tabnas::Value::Object(entries) = &value else {
            panic!("expected a mapping: {value:?}");
        };
        let Some(tabnas::Value::Number(number)) = entries.get("a") else {
            panic!("expected a number: {value:?}");
        };
        assert!(number.is_nan(), "{src:?} should be NaN, got {number}");
    }
    // The control: with an ordinary space the tag converts either way.
    expect("a: !!float 1", j!({"a": 1}));
}

// ===== STRUCTURAL TAGS =====

/// The structural tag pattern is
/// `^!!(seq|map|omap|set|pairs|binary|ordered|python/\S*)\b`, and the
/// trailing `\b` binds to the `python/` alternative too. `/` is not a
/// word character, so the boundary holds only where the non-blank run
/// after the slash carries an ASCII word character, which `\S*`
/// backtracks to find.
///
/// Measured. `a: !!python/ [1]`: TypeScript refuses with `unexpected`
/// (ts/src/yaml.ts under Node), Go `{"a":[1]}`, Rust refuses.
/// `a: !!python/<U+00E9> [1]`: TypeScript refuses, Go `{"a":[1]}`, Rust
/// refuses.
#[test]
fn a_python_tag_needs_a_word_boundary() {
    let parser = tabnas_yaml::make();
    // No word character after the slash, so the tag is not structural
    // and the flow list is left unclaimed.
    for src in [
        "a: !!python/ [1]",
        "a: !!python/\u{e9} [1]",
        "a: !!python/-- [1]",
        "a: !!python/!! [1]",
    ] {
        let error = parser.parse(src).expect_err("the canonical port refuses");
        assert_eq!(error.code, "unexpected", "{src:?}");
    }
    // One ASCII word character anywhere in the run is enough, because
    // `\S*` stops just before the first of them.
    for src in [
        "a: !!python/object [1]",
        "a: !!python/-1 [1]",
        "a: !!python/_ [1]",
        "a: !!python/x! [1]",
        "a: !!python/\u{e9}x [1]",
    ] {
        assert_eq!(json(src), j!({"a": [1]}), "{src:?}");
    }
    // The named alternatives carry the same boundary, and always did.
    assert_eq!(json("a: !!seq [1]"), j!({"a": [1]}));
    assert!(parser.parse("a: !!seqx [1]").is_err());
}

// ===== QUOTED SCALARS =====

/// `\uXXXX` is `String.fromCharCode`, which appends a UTF-16 CODE UNIT,
/// so an adjacent high and low surrogate are one astral character and
/// not two escapes. `\U0000XXXX` is `String.fromCodePoint`, which
/// produces the same unit for a surrogate value, so the two spellings
/// pair with each other.
///
/// Measured. `a: "😀"`: TypeScript `{"a":"\u{1F600}"}`
/// (ts/src/yaml.ts under Node), Go `{"a":"\u{FFFD}\u{FFFD}"}`, Rust
/// `{"a":"\u{1F600}"}`.
#[test]
fn a_surrogate_pair_escape_is_one_character() {
    expect(r#"a: "😀""#, j!({"a": "\u{1f600}"}));
    expect(r#"a: "x😀y""#, j!({"a": "x\u{1f600}y"}));
    // Either half may be written as the eight digit escape.
    expect(r#"a: "\U0000D83D\uDE00""#, j!({"a": "\u{1f600}"}));
    expect(r#"a: "\uD83D\U0000DE00""#, j!({"a": "\u{1f600}"}));
    // A line continuation appends nothing, so the pair still meets.
    expect("a: \"\\uD83D\\\n\\uDE00\"", j!({"a": "\u{1f600}"}));
    // The controls: the astral escape and a literal astral character
    // were always one character.
    expect(r#"a: "\U0001F600""#, j!({"a": "\u{1f600}"}));
    expect("a: \"\u{1f600}\"", j!({"a": "\u{1f600}"}));
}

/// The pairing has one side neither port can reach: a surrogate escape
/// with no partner beside it. TypeScript keeps the code unit, because a
/// JavaScript string is UTF-16; a Rust string holds Unicode scalars, so
/// this port folds it to the replacement character, exactly as the
/// engine's own string lexer folds one. `../DIVERGENCE.md` carries the
/// table, under "UTF-16 escapes in a double quoted scalar".
///
/// Measured. `a: "\uD83D"`: TypeScript a lone high surrogate
/// (ts/src/yaml.ts under Node), Go U+FFFD, Rust U+FFFD.
#[test]
fn an_unpaired_surrogate_escape_folds() {
    expect(r#"a: "\uD83D""#, j!({"a": "\u{fffd}"}));
    expect(r#"a: "\uDE00""#, j!({"a": "\u{fffd}"}));
    // Anything between the halves breaks the pair, including a space,
    // so each one folds on its own.
    expect(r#"a: "\uD83D \uDE00""#, j!({"a": "\u{fffd} \u{fffd}"}));
    // Two highs in a row: the first has no partner.
    expect(r#"a: "\uD83D😀""#, j!({"a": "\u{fffd}\u{1f600}"}));
    // A high at the very end of the scalar is flushed when it closes.
    expect(r#"a: "x\uD83D""#, j!({"a": "x\u{fffd}"}));
}

// ===== WHOLE SOURCE =====

/// A source with nothing but whitespace in it yields one null, and
/// whether a character counts as whitespace is `String.prototype.trim`,
/// which is JavaScript's set again.
///
/// Measured. `"<U+FEFF>"`: TypeScript `null` (ts/src/yaml.ts under
/// Node), Go `"﻿"`, Rust `null`. `"<U+0085>"`: TypeScript
/// `"\u0085"`, Go `null`, Rust `"\u0085"`.
#[test]
fn an_empty_source_is_empty_by_javascript_whitespace() {
    assert_eq!(json("\u{feff}"), j!(null));
    assert_eq!(json("\u{feff}\u{feff}"), j!(null));
    assert_eq!(json(" \u{feff}\n\u{feff} "), j!(null));
    // A next-line character is not whitespace in JavaScript, so a source
    // made only of them is a plain scalar.
    assert_eq!(json("\u{85}"), j!("\u{85}"));
    assert_eq!(json(" \u{85} "), j!("\u{85}"));
    // The controls, on the blanks every runtime agrees about.
    assert_eq!(json("   \n "), j!(null));
    assert_eq!(json("# c\n"), j!(null));
}

// ===== INLINE ANCHORS =====

/// An inline anchor records its scalar as soon as the anchor is seen, so
/// an alias in the same document can resolve it, and the canonical port
/// trims that text with `String.prototype.trim`.
///
/// Measured. `&n <U+FEFF>v: 1\nb: *n`: TypeScript
/// `{"﻿v":1,"b":"v"}` (ts/src/yaml.ts under Node), Go
/// `{"﻿v":1,"b":"﻿v"}`, Rust `{"﻿v":1,"b":"v"}`.
#[test]
fn an_inline_anchor_scalar_is_trimmed_by_javascript_whitespace() {
    // The key keeps the mark, because the key is not the recorded
    // scalar; the alias resolves to the trimmed one.
    assert_eq!(
        plain(&common::parse("&n \u{feff}v: 1\nb: *n")),
        j!({"\u{feff}v": 1, "b": "v"})
    );
    // A next-line character is not trimmed there, so it survives into
    // the alias.
    assert_eq!(
        plain(&common::parse("&n v\u{85}: 1\nb: *n")),
        j!({"v\u{85}": 1, "b": "v\u{85}"})
    );
}

// ===== DIRECTIVES =====

/// `%TAG` is matched with `^%TAG\s+(\S+)\s+(\S+)`, so the handle and the
/// prefix are separated by JavaScript's whitespace. A directive that
/// does not match leaves the handle unregistered, and `!!int` then
/// converts instead of passing its text through.
///
/// Measured, all on `%TAG<sep>!! tag:x,2000:\n--- !!int 007` and
/// `%TAG !!<sep>tag:x,2000:\n--- !!int 007`. With U+0085 as the
/// separator: TypeScript `7` (ts/src/yaml.ts under Node), Go `7`, Rust
/// `7`. With U+FEFF: TypeScript `"007"`, Go `7`, Rust `"007"`.
#[test]
fn a_tag_directive_splits_on_javascript_whitespace() {
    // A byte-order mark separates the fields, so the handle registers
    // and `!!int` becomes a user handle whose text passes through.
    assert_eq!(json("%TAG\u{feff}!! tag:x,2000:\n--- !!int 007"), j!("007"));
    assert_eq!(json("%TAG !!\u{feff}tag:x,2000:\n--- !!int 007"), j!("007"));
    // A next-line character does not, so the directive never matches and
    // `!!int` keeps its core meaning.
    assert_eq!(json("%TAG\u{85}!! tag:x,2000:\n--- !!int 007"), j!(7));
    assert_eq!(json("%TAG !!\u{85}tag:x,2000:\n--- !!int 007"), j!(7));
    // The controls: an ordinary space registers, and no directive at all
    // leaves the core tag alone.
    assert_eq!(json("%TAG !! tag:x,2000:\n--- !!int 007"), j!("007"));
    assert_eq!(json("--- !!int 007"), j!(7));
}
