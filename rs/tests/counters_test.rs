/* Copyright (c) 2026 Richard Rodger and other contributors, MIT License */
//! The rule passes name anchors and merged keys. The Rust twin of
//! ../../ts/test/counters.test.ts, which says why.

use std::sync::{Arc, Mutex};

use tabnas::RuleState;

/// What `src`'s passes name: the anchors each val names as its own on its
/// open pass, in the order the vals open, and the keys each map names as
/// merged on its close pass, sorted, in the order the maps close.
fn named(src: &str) -> (Vec<String>, Vec<Vec<String>>) {
    let mut parser = tabnas_yaml::make();
    let anchors = Arc::new(Mutex::new(Vec::new()));
    let merged = Arc::new(Mutex::new(Vec::new()));
    let (seen_anchors, seen_merged) = (Arc::clone(&anchors), Arc::clone(&merged));
    parser.subscribe_rule_done(move |rule, _context, done| {
        if rule.name.as_ref() == "val" && done.state == RuleState::Open {
            let mut own: Vec<String> = rule
                .n
                .iter()
                .filter(|(k, v)| k.starts_with('&') && usize::try_from(**v).ok() == Some(rule.i))
                .map(|(k, _)| k[1..].to_string())
                .collect();
            own.sort();
            seen_anchors.lock().unwrap().extend(own);
        }
        if rule.name.as_ref() == "map" && done.state == RuleState::Close {
            let mut keys: Vec<String> = rule
                .n
                .keys()
                .filter(|k| k.starts_with("<<"))
                .map(|k| k[2..].to_string())
                .collect();
            if !keys.is_empty() {
                keys.sort();
                seen_merged.lock().unwrap().push(keys);
            }
        }
    });
    parser.parse(src).unwrap_or_else(|e| panic!("{src:?}: {e}"));
    let anchors = anchors.lock().unwrap().clone();
    let merged = merged.lock().unwrap().clone();
    (anchors, merged)
}

#[test]
fn an_anchored_val_names_its_anchors_on_its_own_passes() {
    for (src, want) in [
        (
            "a: &x [1, 2]\nb: &y\n  p: 1\nc: *x\nd: &z 3\n",
            vec!["x", "y", "z"],
        ),
        ("a: &o [&i [1], 2]\nb: *o\n", vec!["o", "i"]),
        ("- &a\n  - 1\n- *a\n", vec!["a"]),
        ("a: [1, 2]\nb: {c: 3}\n", vec![]),
    ] {
        assert_eq!(named(src).0, want, "{src:?}");
    }
}

#[test]
fn the_anchored_val_names_the_anchor_on_its_close_pass_too() {
    let mut parser = tabnas_yaml::make();
    let passes = Arc::new(Mutex::new(Vec::new()));
    let seen = Arc::clone(&passes);
    parser.subscribe_rule_done(move |rule, _context, done| {
        let own = rule
            .n
            .get("&x")
            .is_some_and(|v| usize::try_from(*v).ok() == Some(rule.i));
        if rule.name.as_ref() == "val" && own {
            seen.lock().unwrap().push(done.state);
        }
    });
    parser.parse("a: &x {p: [1]}\nb: *x\n").expect("it parses");
    assert_eq!(
        *passes.lock().unwrap(),
        vec![RuleState::Open, RuleState::Close]
    );
}

#[test]
fn a_map_names_the_keys_its_merge_brought_and_only_those() {
    for (src, want) in [
        (
            "a: &a {x: 1, y: 1}\nb:\n  y: 2\n  <<: *a\n",
            vec![vec!["x"]],
        ),
        (
            "a: &a {x: 1}\nb: &b {y: 2, x: 3}\nc: {<<: [*a, *b], z: 3}\n",
            vec![vec!["x", "y"]],
        ),
        ("a: &a {x: 1}\nb:\n  x: 2\n  <<: *a\n", vec![]),
        ("\"<<\": {x: 1}\nb: 2\n", vec![]),
        ("- <<: {x: 1}\n  b: 2\n", vec![]),
        ("a: {<<: 1, b: 2}\n", vec![]),
    ] {
        assert_eq!(named(src).1, want, "{src:?}");
    }
}
