// The recorded divergences, pinned.
//
// Every entry in ../../DIVERGENCE.md with a RUST column that a shared
// fixture cannot express has a test here. They fail on REPAIR as loudly
// as on regression: a divergence that quietly goes away should be
// deleted from the register in the same change, not left behind as a
// claim nobody checks.
//
// Three are pinned elsewhere, beside the code they belong to: UNDEFINED
// against null, in `undefined_test.rs`; the unpaired surrogate table of
// "UTF-16 escapes in a double quoted scalar", in `js_semantics_test.rs`;
// and that entry's second table, the window cut through an astral
// character, in `escape_window_test.rs`.
//

mod common;

use common::plain;
use serde_json::json as j;

/// **Nesting past 127 containers is refused**, with the engine's
/// `cancel` code, where TypeScript and Go have no limit.
///
/// A parse guard this plugin installs over jsonic's, at jsonic's number,
/// counting jsonic's containers and YAML's own block collections. The
/// parse loop is iterative, but displaying, converting or dropping a
/// value walks the tree with the call stack, so an unbounded document
/// ends the process rather than returning an error. See jsonic's own
/// register for the measurements.
#[test]
fn nesting_past_the_depth_limit_is_refused() {
    let parser = tabnas_yaml::make();
    let within = format!("{}{}", "[".repeat(127), "]".repeat(127));
    parser
        .parse(&within)
        .expect("127 containers is inside the limit");
    let past = format!("{}{}", "[".repeat(128), "]".repeat(128));
    let error = parser.parse(&past).expect_err("128 containers is past it");
    assert_eq!(error.code, "cancel");
    // A compact block sequence nests through YAML's own rules.
    parser
        .parse(&format!("{}x\n", "- ".repeat(127)))
        .expect("127 compact sequences is inside the limit");
    let error = parser
        .parse(&format!("{}x\n", "- ".repeat(128)))
        .expect_err("128 compact sequences is past it");
    assert_eq!(error.code, "cancel");
    // And through a later entry or pair, where the rule holding the
    // container has been replaced by its rotation. `depth` sequences, each
    // in the second entry of the one above, `["x", ["x", ... "y"]]`:
    let second_entries = |depth: usize| {
        let mut src = String::from("- x\n");
        for level in 0..depth - 1 {
            src.push_str(&format!("{}- - x\n", "  ".repeat(level)));
        }
        src + &format!("{}- y\n", "  ".repeat(depth - 1))
    };
    parser
        .parse(&second_entries(127))
        .expect("127 sequences, each in a second entry, is inside the limit");
    let error = parser
        .parse(&second_entries(128))
        .expect_err("128 sequences, each in a second entry, is past it");
    assert_eq!(error.code, "cancel");
    // `depth` levels of a mapping in a sequence, each in the second pair
    // of the one above: two containers a level.
    let second_pairs = |depth: usize| {
        let mut src = String::new();
        for level in 0..depth {
            src.push_str(&format!("{}- a: 1\n", " ".repeat(4 * level)));
            src.push_str(&format!("{}b:\n", " ".repeat(4 * level + 2)));
        }
        src + &format!("{}x\n", " ".repeat(4 * depth))
    };
    parser
        .parse(&second_pairs(63))
        .expect("126 containers, through second pairs, is inside the limit");
    let error = parser
        .parse(&second_pairs(64))
        .expect_err("128 containers, through second pairs, is past it");
    assert_eq!(error.code, "cancel");
}

/// **A column counts Unicode scalars, not UTF-16 code units.** An astral
/// character is one column here and in Go, and two in TypeScript.
///
/// Inherited from the engine: see parser/DIVERGENCE.md, "Column
/// positions for astral characters". Measured, with every column the one
/// the token's offset gives:
///
///  input            TypeScript   Go   Rust
///  `{a: <emoji>, ]`     9         8     8
#[test]
fn an_astral_character_is_one_column() {
    let error = tabnas_yaml::make()
        .parse("{a: \u{1F600}, ]")
        .expect_err("the bracket does not close the brace");
    assert_eq!(
        error.col, 8,
        "TypeScript reports 9 because it counts UTF-16 code units"
    );
    // The control: the same document with an ASCII value agrees exactly.
    let ascii = tabnas_yaml::make()
        .parse("{a: x, ]")
        .expect_err("the bracket does not close the brace");
    assert_eq!(ascii.col, 8);
}

/// **A tag whose value is a `%TAG` directive line: REPAIRED, kept as a
/// parity assertion.**
///
/// This was the one VALUE difference the matcher-chain entry recorded,
/// and it was not the matcher chain at all. The typed tag's unquoted
/// value ends at a colon only before a literal SPACE, a line end or the
/// end of the source, and this port ended it before a TAB as well, so
/// the colon reached the grammar as a key separator and the first
/// document became a mapping. `../DIVERGENCE.md` no longer carries the
/// row, and `test/spec/directives.tsv` carries the input instead.
///
/// Measured 2026-09-21, all three runtimes:
/// `!!int%TAG !! x:\t\n... ... %YAML 1.20o17` is
/// `["!! x:", "... %YAML 1.20o17"]` in TypeScript, in Go and here.
#[test]
fn a_tag_before_a_directive_line_resolves_the_canonical_way() {
    let value = tabnas_yaml::make()
        .parse("!!int%TAG !! x:\t\n... ... %YAML 1.20o17 ")
        .expect("the document parses in every runtime");
    assert_eq!(
        plain(&value),
        j!(["!! x:", "... %YAML 1.20o17"]),
        "the first document is the string \"!! x:\", not a mapping"
    );
}

/// **Shared refusals report the canonical position after the engine repair.**
///
/// These were the two representatives of the matcher-chain position
/// divergence. Parser 0.12.9 preserves the deferred bad token, so this
/// port now reaches the same final point as TypeScript and Go. Keep the
/// exact positions here because shared fixtures pin codes, not positions.
///
/// Measured:
///
///  input                            TypeScript   Rust
///  `[a, b\n{a: 1\n? k\n- x\n`         5:1         5:1
///  `---\n{a: 1\na: 1\n  t2\n`         5:1         5:1
#[test]
fn shared_refusals_report_the_canonical_place() {
    let parser = tabnas_yaml::make();
    for src in ["[a, b\n{a: 1\n? k\n- x\n", "---\n{a: 1\na: 1\n  t2\n"] {
        let error = parser.parse(src).expect_err("both runtimes refuse this");
        assert_eq!(error.code, "unexpected", "{src:?}");
        assert_eq!((error.row, error.col), (5, 1), "{src:?}");
    }
}

/// **A row after a carriage return by itself, on a number the engine
/// cuts.**
///
/// A token this plugin builds is placed by its offset. A number that
/// follows a quoted scalar or a closed flow collection on its line is cut
/// by the engine's own number matcher, at the engine's cursor, and that
/// cursor counts a row at `\n` alone (`line.rowChars`), with no way for a
/// matcher to place it. So where a document's line breaks are `\r` by
/// itself and such a number is what the parse refuses, the diagnostic
/// keeps the right offset and reports row 1, with the column counted from
/// the source's start. TypeScript and Go assign the cursor's row and
/// column from the offset, and report those.
///
/// Measured, each refused as `unexpected` at the same offset:
///
///  input                  TypeScript and Go   Rust
///  `a: 1\rb: "x" 3`       2:8                 1:13
///  `a: 1\rb: [1] 3`       2:8                 1:13
///  `a: 1\r\rb: 'x' 3`     3:8                 1:14
///  `a: 1\nb: "x" 3`       2:8                 2:8
///  `a: 1\r\nb: "x" 3`     2:8                 2:8
///  `a: 1\rb: "x" y`       2:8                 2:8
///  `a: 1\rb: [1, }`       2:8                 2:8
///
/// The last four are the controls: the other two line breaks, and tokens
/// this plugin cuts itself after the same carriage return.
#[test]
fn a_number_the_engine_cuts_after_a_lone_carriage_return_keeps_the_engines_row() {
    let parser = tabnas_yaml::make();
    for (src, pos, canonical, here) in [
        ("a: 1\rb: \"x\" 3", 12, (2, 8), (1, 13)),
        ("a: 1\rb: [1] 3", 12, (2, 8), (1, 13)),
        ("a: 1\r\rb: 'x' 3", 13, (3, 8), (1, 14)),
        ("a: 1\nb: \"x\" 3", 12, (2, 8), (2, 8)),
        ("a: 1\r\nb: \"x\" 3", 13, (2, 8), (2, 8)),
        ("a: 1\rb: \"x\" y", 12, (2, 8), (2, 8)),
        ("a: 1\rb: [1, }", 12, (2, 8), (2, 8)),
    ] {
        let error = parser.parse(src).expect_err("every runtime refuses this");
        assert_eq!(error.code, "unexpected", "{src:?}");
        assert_eq!(error.pos, pos, "{src:?}: the offset is the canonical one");
        assert_eq!(
            (error.row, error.col),
            here,
            "{src:?}: TypeScript and Go report {canonical:?}; if this now \
             does too, delete the entry from ../../DIVERGENCE.md and the \
             first three rows' Rust column here"
        );
    }
}

/// **A blank line of tabs at the end of the source: REPAIRED, kept as a
/// parity assertion.**
///
/// A line holding nothing but blanks, one of them a TAB, is skipped by
/// both matchers. This port once refused at the end of the source after
/// such a skip, and the explicit key never got the null it owes. A pass
/// that steps over something with no token of its own and reaches the
/// source's end now cuts the end token itself, in every runtime, so the
/// rows agree. Measured, all three runtimes:
///
///  input               every runtime
///  `? k\n: <TAB>\n`    `{"k":null}`
///  `? k\n: <TAB>`       `{"k":null}`
///  `? k\n:   <TAB>\n`  `{"k":null}`
///  `? k\n:  \n`        `{"k":null}`
///  `? k\n: <TAB>v\n`   `{"k":"v"}`
#[test]
fn a_trailing_blank_line_of_tabs_keeps_an_explicit_keys_value() {
    let parser = tabnas_yaml::make();
    for src in ["? k\n: \t\n", "? k\n: \t", "? k\n:   \t\n", "? k\n:  \n"] {
        assert_eq!(
            plain(&parser.parse(src).expect("parses")),
            j!({"k": null}),
            "{src:?}"
        );
    }
    assert_eq!(
        plain(&parser.parse("? k\n: \tv\n").expect("parses")),
        j!({"k": "v"})
    );
    assert_eq!(
        plain(&parser.parse("? k\n: \t\nz: 1\n").expect("parses")),
        j!({"k": null, "z": 1})
    );
}

/// **A flow collection at column 0 after a line break parses**, in every
/// runtime.
///
/// It was refused once: the canonical matcher skipped the indent token
/// before such a character, and the engine's remaining matchers, chosen
/// for the line break, left the bracket unclaimed. The line break handler
/// now gives it its indent token, as it gives a key's, and JSON text after
/// a blank or a comment first line is read. A quoted scalar that is not a
/// key still has its indent skipped, and its own matcher lexes it.
#[test]
fn a_flow_collection_at_column_zero_after_a_line_break_parses() {
    let parser = tabnas_yaml::make();
    for (src, want) in [
        ("# c\n{a: 1}", j!({"a": 1})),
        ("   \n[1, 2]", j!([1, 2])),
        ("\n{a: 1}", j!({"a": 1})),
        ("   \n\"x\"", j!("x")),
        ("{a: 1}", j!({"a": 1})),
    ] {
        assert_eq!(plain(&parser.parse(src).expect("parses")), want, "{src:?}");
    }
    // Where there is nothing it can be the value of, it is refused, at the
    // place its offset gives.
    let error = parser.parse("a: 1\n{b: 2}").expect_err("refused");
    assert_eq!(error.code, "unexpected");
    assert_eq!((error.row, error.col), (2, 1));
}

/// **An unterminated typed tag folds a split astral character** to the
/// replacement character, where TypeScript keeps half of one.
///
/// The canonical handler ends an unterminated `!!str "` at the end of the
/// source and takes `fwd.substring(valStart + 1, valEnd - 1)`, whose
/// indices count UTF-16 CODE UNITS: the value it keeps is everything up
/// to the last unit. An astral character is two of those units and one
/// Rust scalar, so dropping one unit leaves a LONE HIGH SURROGATE, which
/// a Rust string cannot hold; this port folds it exactly as it folds an
/// escape naming one. A character inside the Basic Multilingual Plane is
/// one unit and one scalar, and agrees exactly.
///
/// Same trade as "UTF-16 escapes in a double quoted scalar", and the same
/// owner: the string model. Measured, with `U+XXXX` for what the table is
/// about, because a lone surrogate has no UTF-8 spelling:
///
///  input                        TypeScript        Go                   Rust
///  `a: !!str "<emoji>`          U+D83D            three U+FFFD         U+FFFD
///  `a: !!str "a<emoji>`         `a` then U+D83D   `a`, three U+FFFD    `a` U+FFFD
///  `a: !!str "<U+4E2D><U+6587>` U+4E2D            U+4E2D, two U+FFFD   U+4E2D
///  `a: !!str "`                 `"`               refused, `internal`  `"`
#[test]
fn an_unterminated_typed_tag_folds_a_split_astral_character() {
    let parser = tabnas_yaml::make();
    for (src, want) in [
        ("a: !!str \"\u{1F600}", "\u{FFFD}"),
        ("a: !!str \"a\u{1F600}", "a\u{FFFD}"),
        ("a: !!str '\u{1F600}", "\u{FFFD}"),
    ] {
        let value = parser
            .parse(src)
            .unwrap_or_else(|error| panic!("{src:?}: {error}"));
        assert_eq!(
            plain(&value),
            j!({ "a": want }),
            "{src:?}: TypeScript keeps the lone high surrogate"
        );
    }

    // The control. A tail inside the Basic Multilingual Plane is one
    // UTF-16 unit and one Rust scalar, so the two runtimes agree, and so
    // does the reversed-index case a `substring` swaps.
    for (src, want) in [
        ("a: !!str \"\u{4e2d}\u{6587}", "\u{4e2d}"),
        ("a: !!str \"\u{e9}", ""),
        ("a: !!str \"ab", "a"),
        ("a: !!str \"", "\""),
        ("a: !!str '", "'"),
    ] {
        let value = parser
            .parse(src)
            .unwrap_or_else(|error| panic!("{src:?}: {error}"));
        assert_eq!(plain(&value), j!({ "a": want }), "{src:?}");
    }
}
