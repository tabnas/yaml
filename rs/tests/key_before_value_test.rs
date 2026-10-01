// A mapping's member is named before its value is built (tabnas/yaml#105).
//
// The Rust twin of ../../ts/test/key-before-value.test.ts, which says what
// is recorded and why: a consumer that follows the rule events (as
// tabnas/transduce's rule-event adapter does) is told a member's key by a
// later open pass on the open mapping, the pass of the pair rule that
// pushes the value's rule. A mapping that starts in a sequence entry used
// to name its first member in the pass that opened it (`yamlElemMap`'s),
// so that consumer was never told the key; now `yamlElemPair` reads every
// pair, the first included.

use std::rc::Rc;
use std::sync::{Arc, Mutex};

use tabnas::{RuleState, Value};

/// What the consumer has been told so far.
#[derive(Default)]
struct Told {
    /// The open mappings, innermost last: the node cell, the depth of the
    /// rule that opened it, and its name in `lines`. A cell is identified
    /// by its address only while it is open, when a rule still holds it.
    open: Vec<(usize, usize, String)>,
    made: usize,
    lines: Vec<String>,
}

/// The value `src` parses to, as JSON, and what the consumer is told, in
/// order: `open mN` when a mapping first shows at the end of an open pass,
/// and `mN key K` when a later open pass on the innermost open mapping
/// carries the key K. A mapping stays open until a close pass at its
/// depth, on it, that does not replace the rule. Sequences are left out,
/// as in the Go twin, where a sequence is a slice with no identity.
fn told(src: &str) -> (String, Vec<String>) {
    let mut parser = tabnas_yaml::make();
    let state = Arc::new(Mutex::new(Told::default()));
    let seen = Arc::clone(&state);
    parser.subscribe_rule_done(move |rule, _context, done| {
        if !matches!(&*rule.node.borrow(), Value::Object(_) | Value::MapRef(_)) {
            return;
        }
        let cell = Rc::as_ptr(&rule.node) as usize;
        let mut told = seen.lock().unwrap();
        if done.state == RuleState::Open {
            if !told.open.iter().any(|(open, _, _)| *open == cell) {
                let name = format!("m{}", told.made);
                told.made += 1;
                told.lines.push(format!("open {name}"));
                told.open.push((cell, rule.d, name));
            } else if let (Some((top, _, name)), Some(Value::String(key))) =
                (told.open.last(), rule.u.get("key"))
            {
                if *top == cell {
                    let line = format!("{name} key {key}");
                    told.lines.push(line);
                }
            }
            return;
        }
        let replaces = done.alt.as_ref().is_some_and(|alt| !alt.r.is_empty());
        if !replaces
            && told
                .open
                .last()
                .is_some_and(|(top, d, _)| *top == cell && *d == rule.d)
        {
            told.open.pop();
        }
    });
    let value = parser.parse(src).unwrap_or_else(|e| panic!("{src:?}: {e}"));
    let lines = std::mem::take(&mut state.lock().unwrap().lines);
    (value.to_string(), lines)
}

/// One document: the value it parses to and what the consumer is told.
fn check(src: &str, value: &str, lines: &[&str]) {
    let (got_value, got_lines) = told(src);
    assert_eq!(got_value, value, "{src:?}");
    assert_eq!(got_lines, lines, "{src:?}");
}

#[test]
fn an_explicit_key_that_is_a_mapping_with_a_mapping_value_v9d5() {
    check(
        "- sun: yellow\n- ? earth: blue\n  : moon: white\n",
        r#"[{"sun":"yellow"},{"earth: blue":{"moon":"white"}}]"#,
        &[
            "open m0",
            "m0 key sun",
            "open m1",
            "m1 key earth: blue",
            "open m2",
            "m2 key moon",
        ],
    );
}

#[test]
fn an_explicit_key_whose_value_is_a_sequence() {
    check(
        "- ? a\n  : - b\n    - c\n",
        r#"[{"a":["b","c"]}]"#,
        &["open m0", "m0 key a"],
    );
}

#[test]
fn an_explicit_key_whose_value_is_a_flow_collection() {
    check(
        "- ? a\n  : {b: 1}\n",
        r#"[{"a":{"b":1}}]"#,
        &["open m0", "m0 key a", "open m1", "m1 key b"],
    );
    check(
        "- ? a\n  : [b, c]\n",
        r#"[{"a":["b","c"]}]"#,
        &["open m0", "m0 key a"],
    );
}

#[test]
fn an_explicit_key_that_is_a_flow_collection() {
    check(
        "- ? [a, b]\n  : c\n",
        r#"[{"[a, b]":"c"}]"#,
        &["open m0", "m0 key [a, b]"],
    );
}

#[test]
fn an_explicit_key_with_no_value() {
    check("- ? a\n", r#"[{"a":null}]"#, &["open m0", "m0 key a"]);
}

#[test]
fn an_implicit_key_whose_value_is_a_collection() {
    check(
        "- a:\n    b: 1\n",
        r#"[{"a":{"b":1}}]"#,
        &["open m0", "m0 key a", "open m1", "m1 key b"],
    );
    check(
        "- a:\n  - x\n",
        r#"[{"a":["x"]}]"#,
        &["open m0", "m0 key a"],
    );
}

#[test]
fn a_mapping_in_a_flow_sequence_entry() {
    for src in ["[a: {b: 1}]", "[? a : {b: 1}]"] {
        check(
            src,
            r#"[{"a":{"b":1}}]"#,
            &["open m0", "m0 key a", "open m1", "m1 key b"],
        );
    }
}

#[test]
fn every_later_pair_is_named_as_the_first_is() {
    check(
        "- a: 1\n  b:\n    c: 2\n",
        r#"[{"a":1,"b":{"c":2}}]"#,
        &["open m0", "m0 key a", "m0 key b", "open m1", "m1 key c"],
    );
}

#[test]
fn a_mapping_outside_a_sequence_was_named_first_already() {
    check(
        "? earth: blue\n: moon: white\n",
        r#"{"earth: blue":{"moon":"white"}}"#,
        &["open m0", "m0 key earth: blue", "open m1", "m1 key moon"],
    );
}
