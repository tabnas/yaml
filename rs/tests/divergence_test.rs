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
// One entry has no Rust column to pin. "A tab-only tail at the end of
// the source" is a GO divergence: this port gives the canonical answer,
// the refusal included, so the row that can fail lives in
// `go/divergence_test.go`.

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
}

/// **A column counts Unicode scalars, not UTF-16 code units.** An astral
/// character is one column here and in Go, and two in TypeScript.
///
/// Inherited from the engine: see parser/DIVERGENCE.md, "Column
/// positions for astral characters". Measured:
///
///  input            TypeScript   Go   Rust
///  `{a: <emoji>, ]`    10         9     9
#[test]
fn an_astral_character_is_one_column() {
    let error = tabnas_yaml::make()
        .parse("{a: \u{1F600}, ]")
        .expect_err("the bracket does not close the brace");
    assert_eq!(
        error.col, 9,
        "TypeScript reports 10 because it counts UTF-16 code units"
    );
    // The control: the same document with an ASCII value agrees exactly.
    let ascii = tabnas_yaml::make()
        .parse("{a: x, ]")
        .expect_err("the bracket does not close the brace");
    assert_eq!(ascii.col, 9);
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

/// **A refusal both runtimes make can be reported at a different place.**
///
/// Same cause as above: this port refuses at the character its own
/// matcher cannot claim, and the canonical one carries on to a later
/// one. Both refuse, with the same code; only the position differs, and
/// no shared fixture pins a position.
///
/// Measured:
///
///  input                            TypeScript   Rust
///  `[a, b\n{a: 1\n? k\n- x\n`         5:1         3:1
///  `---\n{a: 1\na: 1\n  t2\n`         5:1         4:3
#[test]
fn a_shared_refusal_can_be_reported_at_a_different_place() {
    let parser = tabnas_yaml::make();
    for (src, row, col) in [
        ("[a, b\n{a: 1\n? k\n- x\n", 3, 1),
        ("---\n{a: 1\na: 1\n  t2\n", 4, 3),
    ] {
        let error = parser.parse(src).expect_err("both runtimes refuse this");
        assert_eq!(error.code, "unexpected", "{src:?}");
        assert_eq!((error.row, error.col), (row, col), "{src:?}");
    }
}

/// **A blank line of tabs at the end of the source can cost a token.**
///
/// The same end-of-source guard, reached a different way. A line holding
/// nothing but blanks, one of them a TAB, is skipped by both matchers:
/// YAML counts it blank and the engine refuses a bare tab. The canonical
/// then moves its point and returns no token, and the engine's own
/// matchers take over at the end of the source. This port returns
/// `Act::refuse`, and the explicit key never gets the null it owes.
///
/// Making the guard refuse only when nothing was consumed was measured
/// and is NOT the repair: it lets `a: !é` reach the engine with an
/// `internal` error, which `untrusted_test.rs` catches.
///
/// Measured 2026-09-21:
///
///  input               TypeScript     Go             Rust
///  `? k\n: <TAB>\n`    `{"k":null}`   `{"k":null}`   `ERROR:unexpected`
///  `? k\n: <TAB>`      `{"k":null}`   `{"k":null}`   `ERROR:unexpected`
///  `? k\n:   <TAB>\n`  `{"k":null}`   `{"k":null}`   `ERROR:unexpected`
///  `? k\n:  \n`        `{"k":null}`   `{"k":null}`   `{"k":null}`
///  `? k\n: <TAB>v\n`   `{"k":"v"}`    `{"k":"v"}`    `{"k":"v"}`
#[test]
fn a_trailing_blank_line_of_tabs_costs_an_explicit_keys_value() {
    let parser = tabnas_yaml::make();
    for src in ["? k\n: \t\n", "? k\n: \t", "? k\n:   \t\n"] {
        let error = parser
            .parse(src)
            .expect_err("TypeScript and Go both accept this one");
        assert_eq!(error.code, "unexpected", "{src:?}");
    }

    // The controls: the same shapes without the tab, and the same tab
    // with a value after it, agree in all three runtimes. Without them
    // the rows above would also pass on a port that refused every
    // explicit key.
    assert_eq!(
        plain(&parser.parse("? k\n:  \n").expect("parses")),
        j!({"k": null})
    );
    assert_eq!(
        plain(&parser.parse("? k\n: \tv\n").expect("parses")),
        j!({"k": "v"})
    );
    assert_eq!(
        plain(&parser.parse("? k\n: \t\nz: 1\n").expect("parses")),
        j!({"k": {"z": 1}})
    );
}

/// **A flow collection or a quoted scalar at column 0 after a line
/// break is refused**, in every runtime.
///
/// Not a divergence: a shape that looks like one, kept here because it
/// is surprising and because a port that "fixed" it would diverge. The
/// canonical matcher skips the indent token before such a character, and
/// the engine's remaining matchers were chosen for the line break, so the
/// bracket is unclaimed. A quoted scalar is fine, because this plugin's
/// own matcher lexes it.
#[test]
fn a_flow_collection_at_column_zero_after_a_line_break_is_refused() {
    let parser = tabnas_yaml::make();
    for src in ["# c\n{a: 1}", "   \n[1, 2]", "\n{a: 1}"] {
        let error = parser.parse(src).expect_err("every runtime refuses this");
        assert_eq!(error.code, "unexpected", "{src:?}");
    }
    // The quoted forms do parse, which is what makes the above a
    // property of the matcher chain rather than of column 0.
    assert_eq!(plain(&parser.parse("   \n\"x\"").expect("parses")), j!("x"));
    // A flow collection on the FIRST line is fine, so the refusal is
    // about what the lexer call dispatched on and not about brackets.
    assert_eq!(
        plain(&parser.parse("{a: 1}").expect("parses")),
        j!({"a": 1})
    );
    // The position is the canonical one too, quirk included: the
    // canonical matcher sets the column to zero before the marker it
    // skipped, and this port carries that through.
    let error = parser.parse("# c\n{a: 1}").expect_err("refused");
    assert_eq!((error.row, error.col), (2, 0));
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
