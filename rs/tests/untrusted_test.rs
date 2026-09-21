// Untrusted input, the boundary this crate is held to.
//
// A YAML document routinely arrives from outside the system: a cloned
// repository, a vendor chart, a user upload. Nothing here may panic,
// hang, overflow the stack or take super-linear time, whatever the
// document says. The values a document produces are data, never
// instructions: see ../AGENTS.md.

mod common;

use std::time::Instant;

/// Nesting past the depth limit is refused with the engine's `cancel`
/// code rather than growing the call stack.
///
/// The limit comes from `tabnas-jsonic`, which installs a parse budget of
/// 127 containers. This plugin does not raise or lower it. The engine
/// parses iteratively, but displaying, converting or dropping a value
/// walks the tree with the call stack, so an unbounded document ends the
/// process rather than returning an error. TypeScript and Go have no
/// limit, which is a recorded divergence: see ../DIVERGENCE.md.
#[test]
fn deep_nesting_is_refused_not_crashed() {
    let parser = tabnas_yaml::make();
    // A block shape indents by two spaces a level, so its source grows
    // with the square of the depth; the flow shapes go far deeper for the
    // same bytes.
    for depth in [130usize, 1_000] {
        for (shape, src) in [
            (
                "flow list",
                format!("{}{}", "[".repeat(depth), "]".repeat(depth)),
            ),
            (
                "flow map",
                format!("{}{}", "{a: ".repeat(depth), "}".repeat(depth)),
            ),
            (
                "block map",
                (0..depth)
                    .map(|level| format!("{}k{level}:\n", "  ".repeat(level)))
                    .collect(),
            ),
            (
                "block sequence",
                (0..depth)
                    .map(|level| format!("{}- \n", "  ".repeat(level)))
                    .collect(),
            ),
        ] {
            let error = parser
                .parse(&src)
                .err()
                .unwrap_or_else(|| panic!("a {shape} nested {depth} deep was accepted"));
            assert_eq!(error.code, "cancel", "a {shape} nested {depth} deep");
        }
    }

    // And very deep indeed, where the source stays small.
    for depth in [50_000usize, 200_000] {
        let src = format!("{}{}", "[".repeat(depth), "]".repeat(depth));
        let error = parser
            .parse(&src)
            .err()
            .unwrap_or_else(|| panic!("a flow list nested {depth} deep was accepted"));
        assert_eq!(error.code, "cancel", "a flow list nested {depth} deep");
    }
}

/// Nesting just inside the limit still parses, so the guard above is not
/// satisfied by a parser that refuses everything.
#[test]
fn nesting_within_the_limit_still_parses() {
    let parser = tabnas_yaml::make();
    let src = format!("{}{}", "[".repeat(100), "]".repeat(100));
    parser.parse(&src).expect("100 levels is within the limit");
}

/// A long document is parsed in about linear time. Two megabytes of one
/// scalar is a shape a quadratic scanner chokes on.
#[test]
fn a_long_document_parses_in_reasonable_time() {
    let parser = tabnas_yaml::make();
    let src = format!("a: {}", "x".repeat(2_000_000));
    let start = Instant::now();
    let value = parser.parse(&src).expect("a long scalar parses");
    let elapsed = start.elapsed();
    assert!(
        matches!(&value, tabnas::Value::Object(entries)
            if matches!(entries.get("a"), Some(tabnas::Value::String(text)) if text.len() == 2_000_000)),
        "the scalar came back changed"
    );
    assert!(
        elapsed.as_secs() < 30,
        "2 MiB took {elapsed:?}, which is the shape of a super-linear scan"
    );
}

/// Unterminated constructs end the parse, one way or the other, and
/// never hang. Each expectation is the canonical one, measured.
#[test]
fn unterminated_constructs_terminate() {
    let parser = tabnas_yaml::make();
    for (src, accepted) in [
        ("[1, 2", true),
        ("{a: 1", true),
        ("a: \"abc", true),
        ("a: 'abc", true),
        ("a: |", true),
        ("&a", false),
        ("[1, 2\n", false),
        ("*missing", true),
    ] {
        let result = parser.parse(src);
        assert_eq!(
            result.is_ok(),
            accepted,
            "{src:?} => {:?}",
            result
                .map(|value| value.to_string())
                .map_err(|error| error.code)
        );
    }
}

/// Empty, blank and comment-only documents are values, not failures.
#[test]
fn empty_documents_are_values() {
    let parser = tabnas_yaml::make();
    for src in ["", " ", "\n\n", "   \n  \n  ", "# only\n", "#\n#\n"] {
        parser
            .parse(src)
            .unwrap_or_else(|error| panic!("{src:?}: {error}"));
    }
}

/// Control characters, a NUL, a byte-order mark, an astral character and
/// a lone carriage return all behave as the canonical parser makes them
/// behave, measured against it.
#[test]
fn odd_characters_are_handled_not_crashed() {
    let parser = tabnas_yaml::make();
    for (label, src, want) in [
        ("control", "a: \u{1}\u{2}", Some("{\"a\":\"\u{1}\u{2}\"}")),
        ("nul", "a: \u{0}", Some("{\"a\":\"\u{0}\"}")),
        ("vertical-tab", "a: \u{b}", Some("{\"a\":\"\u{b}\"}")),
        ("escape", "a: \u{1b}[0m", Some("{\"a\":\"\u{1b}[0m\"}")),
        ("byte-order-mark", "\u{feff}a: 1", Some("{\"\u{feff}a\":1}")),
        ("astral", "a: \u{1F600}", Some("{\"a\":\"\u{1F600}\"}")),
        ("next-line", "a: \u{85}", Some("{\"a\":\"\u{85}\"}")),
        (
            "zero-width-space",
            "a: \u{feff}",
            Some("{\"a\":\"\u{feff}\"}"),
        ),
        // A lone carriage return between two pairs is refused, as it is
        // in TypeScript: the line it opens has no indent token.
        ("lone-carriage-return", "a: 1\rb: 2", None),
    ] {
        match (parser.parse(src), want) {
            (Ok(value), Some(want)) => assert_eq!(value.to_string(), want, "{label}"),
            (Err(error), None) => assert_eq!(error.code, "unexpected", "{label}"),
            (Ok(value), None) => panic!("{label}: {src:?} parsed to {value}"),
            (Err(error), Some(want)) => panic!("{label}: {src:?} failed ({error}), want {want}"),
        }
    }
}

/// A tab inside a document is not a crash, whatever else it is.
#[test]
fn tabs_do_not_crash() {
    let parser = tabnas_yaml::make();
    for src in ["a:\n\tb: 1", "\t", "a: 1\n\t\nb: 2", "-\t", "\t\n\t\n"] {
        let _ = parser.parse(src);
    }
}
