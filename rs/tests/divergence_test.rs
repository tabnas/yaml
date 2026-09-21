// The recorded divergences, pinned.
//
// Every entry in ../../DIVERGENCE.md that a shared fixture cannot express
// has a test here. They fail on REPAIR as loudly as on regression: a
// divergence that quietly goes away should be deleted from the register
// in the same change, not left behind as a claim nobody checks.
//
// The one divergence not pinned here is UNDEFINED against null, which has
// its own file, `undefined_test.rs`.

mod common;

use common::plain;
use serde_json::json as j;

/// **Nesting past 127 containers is refused**, with the engine's
/// `cancel` code, where TypeScript and Go have no limit.
///
/// Inherited from `tabnas-jsonic`, which installs the budget, and from
/// the engine beneath it: the parse loop is iterative, but displaying,
/// converting or dropping a value walks the tree with the call stack, so
/// an unbounded document ends the process rather than returning an
/// error. See jsonic's own register for the measurements.
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

/// **A tag whose value is a `%TAG` directive line resolves differently.**
///
/// The engines dispatch their matcher chain on the FIRST character of a
/// lexer call, and the two chains are not the same, so what runs after
/// this plugin's matcher has moved the cursor differs. Most of that is
/// compensated for (see `crate::lex::claimable_after_move` and the
/// number stand-in in `crate::text::check`); this shape is not.
///
/// Measured:
///
///  input                                    TypeScript
///  `!!int%TAG !! x:\t\n... ... %YAML 1.20o17 `
///    TypeScript  `["!! x:", "... %YAML 1.20o17"]`
///    Rust        `[{"!! x": null}, "... %YAML 1.20o17"]`
#[test]
fn a_tag_before_a_directive_line_resolves_differently() {
    let value = tabnas_yaml::make()
        .parse("!!int%TAG !! x:\t\n... ... %YAML 1.20o17 ")
        .expect("the document parses in both runtimes");
    assert_eq!(
        plain(&value),
        j!([{"!! x": null}, "... %YAML 1.20o17"]),
        "TypeScript reads the first document as the string \"!! x:\""
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
