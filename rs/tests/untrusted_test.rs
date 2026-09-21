// Untrusted input, the boundary this crate is held to.
//
// A YAML document routinely arrives from outside the system: a cloned
// repository, a vendor chart, a user upload. Nothing here may panic,
// hang, overflow the stack or take super-linear time, whatever the
// document says. The values a document produces are data, never
// instructions: see ../AGENTS.md.

mod common;

use std::time::Instant;

/// One line per level, each indented two spaces a level deeper than the
/// last. Written as a loop because `map(format!).collect()` over an
/// iterator is a clippy error on the MSRV toolchain the gate runs.
fn indented(depth: usize, line: impl Fn(usize) -> String) -> String {
    let mut src = String::new();
    for level in 0..depth {
        src.push_str(&"  ".repeat(level));
        src.push_str(&line(level));
    }
    src
}

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
            ("block map", indented(depth, |level| format!("k{level}:\n"))),
            ("block sequence", indented(depth, |_| "- \n".to_string())),
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

/// An unterminated construct that ends in a MULTIBYTE character does not
/// panic, and answers what the canonical handler answers.
///
/// Every offset this port computes comes from a scan over BYTES, standing
/// in for the canonical scan over UTF-16 code units. Where the canonical
/// handler steps one unit back from the end of an unterminated construct,
/// or takes a fixed width of escape digits, the same arithmetic in bytes
/// can land inside a character, and slicing there panics. Two handlers
/// did: the typed tag's quoted value and the `\x`/`\u`/`\U` escape. The
/// engine catches a panic raised in a lexer callback and reports it as
/// the `internal` code, so that code is the failure this looks for.
///
/// The named cases are measured against `ts/src/yaml.ts` under Node 22.
#[test]
fn a_multibyte_tail_does_not_panic() {
    let parser = tabnas_yaml::make();
    for (src, want) in [
        // The typed tag's quoted value. The canonical `substring` takes
        // the whole remainder minus its last UTF-16 unit, and SWAPS a
        // reversed pair rather than reading it as empty.
        ("a: !!str \"\u{e9}", Some("{\"a\":\"\"}")),
        ("a: !!str '\u{e9}", Some("{\"a\":\"\"}")),
        ("a: !!str \"\u{4e2d}\u{6587}", Some("{\"a\":\"\u{4e2d}\"}")),
        ("a: !!str \"ab", Some("{\"a\":\"a\"}")),
        ("a: !!str \"", Some("{\"a\":\"\\\"\"}")),
        ("a: !!str '", Some("{\"a\":\"'\"}")),
        ("a: !!str \"\\", Some("{\"a\":\"\\\\\"}")),
        ("a: !!str \"abc\\", Some("{\"a\":\"abc\\\\\"}")),
        ("a: !!str \"\\\u{e9}", Some("{\"a\":\"\\\\\"}")),
        // The escape windows, which are a fixed width of CHARACTERS.
        ("a: \"\\x\u{4e2d}", Some("{\"a\":\"\\u0000\"}")),
        (
            "a: \"\\u\u{4e2d}\u{4e2d}\u{4e2d}\u{4e2d}",
            Some("{\"a\":\"\\u0000\"}"),
        ),
        ("a: \"\\x\u{1F600}", Some("{\"a\":\"\\u0000\"}")),
        // `\U` is `String.fromCodePoint`, which throws where the others
        // fold, so the document is refused: see js_semantics_test.rs.
        (
            "a: \"\\U\u{4e2d}\u{4e2d}\u{4e2d}\u{4e2d}\u{4e2d}\u{4e2d}\u{4e2d}\u{4e2d}",
            None,
        ),
    ] {
        match (parser.parse(src), want) {
            (Ok(value), Some(want)) => assert_eq!(
                serde_json::to_string(&common::plain(&value)).expect("json"),
                want,
                "{src:?}"
            ),
            (Err(error), None) => assert_eq!(error.code, "unexpected", "{src:?}"),
            (Ok(value), None) => panic!("{src:?} parsed to {value}"),
            (Err(error), Some(want)) => panic!("{src:?} failed ({error}), want {want}"),
        }
    }

    // The sweep: every prefix of every construct, followed by a
    // multibyte character, in several contexts.
    let tails = [
        "\u{e9}",
        "\u{4e2d}",
        "\u{1F600}",
        "\u{feff}",
        "\\\u{e9}",
        "\u{e9}\\",
    ];
    let mut swept = 0usize;
    for body in MULTIBYTE_SWEEP_BODIES {
        for cut in (0..=body.len()).filter(|cut| body.is_char_boundary(*cut)) {
            for tail in tails {
                for suffix in ["", "\n b: 1\n", "\"", "]"] {
                    let src = format!("{}{tail}{suffix}", &body[..cut]);
                    swept += 1;
                    if let Err(error) = parser.parse(&src) {
                        assert_ne!(error.code, "internal", "{src:?}: {error}");
                    }
                }
            }
        }
    }
    assert!(swept > 5_000, "the sweep covered only {swept} documents");
}

/// The constructs the sweep above cuts at every character boundary.
const MULTIBYTE_SWEEP_BODIES: &[&str] = &[
    // quoted scalars and their escapes
    "a: \"abc",
    "a: 'abc",
    "a: \"a\\",
    "a: \"\\x",
    "a: \"\\u",
    "a: \"\\U",
    "a: \"\\xA",
    "a: \"\\u00",
    "a: \"\\U000000",
    "a: \"a\\n b",
    "a: \"a\nb",
    // typed tags
    "a: !!str \"",
    "a: !!str '",
    "a: !!int \"",
    "a: !!float \"",
    "a: !!str ",
    "a: !!str &n \"",
    "a: !!",
    "a: !",
    "a: !<x",
    "a: !tag ",
    // anchors, aliases, merge keys
    "a: &n ",
    "a: *",
    "<<: *n",
    "a: &n [1,",
    // directives and markers
    "%TAG !! t:",
    "%YAML 1.2",
    "---",
    "...",
    // block scalars
    "a: |\n  b",
    "a: >\n  b",
    "a: |2\n  b",
    "a: |-\n  b",
    "a: |+\n  b",
    // plain scalars, keys, comments, numbers
    "a: b",
    "a: 1",
    "a: -1.5e",
    "a: 0x1",
    "? k\n: v",
    "a: # c",
    "a:\n  b: ",
    "- - ",
    "a: [1,",
    "a: {b: ",
];

/// An alias EXPANDS: the canonical handler deep copies the anchored value
/// into every use, so reuse costs what it produces and not what it says.
///
/// Reuse of one anchor stays linear, which is the case that matters. The
/// case that does not is a CHAIN of anchors that each alias the one
/// before twice, where every line doubles the tree. That shape is
/// exponential in all three runtimes, because all three deep copy;
/// `rs/README.md` carries the measurement rather than this port capping
/// what the canonical accepts.
#[test]
fn alias_reuse_of_one_anchor_stays_linear() {
    let parser = tabnas_yaml::make();
    let document = |uses: usize| {
        let mut src = String::from("base: &b [0,1,2,3,4,5,6,7,8,9]\n");
        for use_index in 0..uses {
            src.push_str(&format!("u{use_index}: *b\n"));
        }
        src
    };
    let time = |uses: usize| {
        let src = document(uses);
        let start = Instant::now();
        let value = parser.parse(&src).expect("reuse parses");
        let elapsed = start.elapsed();
        let tabnas::Value::Object(entries) = &value else {
            panic!("a mapping, not {value}");
        };
        assert_eq!(entries.len(), uses + 1);
        elapsed
    };
    // Warm, then compare two sizes an order of magnitude apart. A
    // quadratic expansion would be a hundredfold, which no constant
    // factor at this scale hides.
    let _ = time(200);
    let small = time(200).as_secs_f64().max(1e-6);
    let large = time(2_000).as_secs_f64();
    assert!(
        large < small * 40.0,
        "2000 uses took {large:.4}s against {small:.4}s for 200, which is not linear"
    );
}
