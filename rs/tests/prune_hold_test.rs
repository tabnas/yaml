/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */
//! An anchored value holds a streaming host's pruning until its anchor is
//! recorded (tabnas/yaml#138). The Rust twin of
//! ../../ts/test/prune-hold.test.ts, which says why.

use std::sync::{Arc, Mutex};

use tabnas::{RuleState, Value};

/// `src` parsed with every array emptied at its close pass while no hold
/// is raised, as a pruning host empties them, and the hold each array's
/// close pass saw, in order.
fn pruned(src: &str) -> (String, Vec<f64>) {
    let mut parser = tabnas_yaml::make();
    let holds = Arc::new(Mutex::new(Vec::new()));
    let seen = Arc::clone(&holds);
    parser.subscribe_rule_done(move |rule, context, done| {
        if done.state == RuleState::Open {
            return;
        }
        let mut node = rule.node.borrow_mut();
        if !matches!(&*node, Value::Array(_) | Value::ListRef(_)) {
            return;
        }
        let hold = match context.u.get("pruneHold") {
            Some(Value::Number(n)) => *n,
            _ => 0.0,
        };
        seen.lock().unwrap().push(hold);
        if hold > 0.0 {
            return;
        }
        match &mut *node {
            Value::Array(items) => {
                if let Some(items) = Arc::get_mut(items) {
                    items.clear();
                }
            }
            Value::ListRef(list) => {
                if let Some(list) = Arc::get_mut(list) {
                    list.value.clear();
                }
            }
            _ => {}
        }
    });
    let value = parser.parse(src).unwrap_or_else(|e| panic!("{src:?}: {e}"));
    let holds = holds.lock().unwrap().clone();
    (value.to_string(), holds)
}

/// An alias used as a key names the anchored sequence's text, as the whole
/// parse names it, though the host empties every array it may: the hold is
/// raised while the anchored value is open, and released once it is
/// recorded.
#[test]
fn an_alias_key_to_an_anchored_sequence_survives_pruning() {
    for (src, want) in [
        ("a: &l [1, 2]\n*l: x\n", r#"{"a":[1,2],"1,2":"x"}"#),
        (
            "a: &o [&i [1, 2], 3]\n*o: x\n*i: y\n",
            r#"{"a":[[1,2],3],"1,2,3":"x","1,2":"y"}"#,
        ),
    ] {
        let (value, holds) = pruned(src);
        assert_eq!(value, want, "{src:?}");
        let whole = tabnas_yaml::make().parse(src).expect("it parses");
        assert_eq!(whole.to_string(), want, "{src:?}: the whole parse");
        assert!(holds.iter().any(|h| *h > 0.0), "{src:?}: {holds:?}");
        assert_eq!(holds.last(), Some(&0.0), "{src:?}: released: {holds:?}");
    }
}

/// The hold counts the open anchored values: an anchored value inside an
/// anchored value raises it a second time.
#[test]
fn nested_anchors_raise_the_hold_twice() {
    let (_, holds) = pruned("a: &o [&i [1, 2], 3]\n*o: x\n*i: y\n");
    assert_eq!(holds.iter().copied().fold(0.0, f64::max), 2.0, "{holds:?}");
}

/// A document with no anchor raises no hold, so the host empties every
/// array it would.
#[test]
fn a_document_with_no_anchor_holds_nothing() {
    let (value, holds) = pruned("a: [1, [2, 3]]\nb: [4]\n");
    assert!(!holds.is_empty(), "{holds:?}");
    assert!(holds.iter().all(|h| *h == 0.0), "{holds:?}");
    assert_eq!(value, r#"{"a":[],"b":[]}"#);
}
