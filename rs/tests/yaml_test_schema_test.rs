// YAML Test Schema conformance: how plain and tagged scalars resolve. The
// Rust half of `ts/test/yaml-test-schema.test.ts` and
// `go/yaml_test_schema_test.go`: same corpus, same ledger, same rules.
//
// Corpus: https://github.com/perlpunk/yaml-test-schema (MIT, Tina Müller),
// vendored byte-identical at `test/yaml-test-schema`; see its README.md for
// the source commit. Every case must read as the YAML 1.2 core schema says
// unless its input is on the checked ledger
// `test/yaml-test-schema-deviations.tsv`, and a listed input must still
// read differently, so a repair fails until its line is deleted.

mod common;

use std::collections::{BTreeMap, BTreeSet};
use std::fs;

use tabnas::Value;

fn corpus() -> BTreeMap<String, [String; 3]> {
    let path = common::repo_root()
        .join("test")
        .join("yaml-test-schema")
        .join("schema-core.json");
    let text = fs::read_to_string(&path).unwrap_or_else(|error| {
        panic!(
            "yaml-test-schema corpus is MISSING at {} ({error}): restore it with \
             `git checkout -- test/yaml-test-schema`",
            path.display()
        )
    });
    serde_json::from_str(&text).expect("schema-core.json is a map of [type, value, dump]")
}

/// `<input as a JSON string> <TAB> <reason>`, ignoring `#` comments and
/// blank lines.
fn ledger() -> BTreeSet<String> {
    let path = common::repo_root()
        .join("test")
        .join("yaml-test-schema-deviations.tsv");
    let text = fs::read_to_string(&path)
        .unwrap_or_else(|error| panic!("cannot read {}: {error}", path.display()));
    text.lines()
        .map(|line| line.trim_end_matches('\r'))
        .filter(|line| !line.trim().is_empty() && !line.starts_with('#'))
        .map(|line| {
            let field = line.split('\t').next().expect("split yields one field");
            serde_json::from_str::<String>(field)
                .unwrap_or_else(|error| panic!("ledger line {line:?}: {error}"))
        })
        .collect()
}

/// Whether `got` is the value the core schema gives for `case`, strictly:
/// same type and value, NaN equal to NaN.
fn reads_as_core(got: &Value, case: &[String; 3]) -> bool {
    let [kind, value, _] = case;
    let number =
        |want: f64| matches!(got, Value::Number(n) if *n == want || (n.is_nan() && want.is_nan()));
    match kind.as_str() {
        "bool" => matches!(got, Value::Bool(flag) if *flag == (value == "true()")),
        "null" => matches!(got, Value::Null | Value::Undefined),
        "inf" => number(if value == "inf()" {
            f64::INFINITY
        } else {
            f64::NEG_INFINITY
        }),
        "nan" => number(f64::NAN),
        "int" | "float" => number(value.parse::<f64>().expect("corpus number parses")),
        "str" => match got {
            Value::String(text) => text == value,
            Value::Text(text) => &text.string == value,
            _ => false,
        },
        other => panic!("unknown type in corpus: {other}"),
    }
}

#[test]
fn census() {
    let corpus = corpus();
    let mut types = BTreeMap::<&str, usize>::new();
    for case in corpus.values() {
        *types.entry(case[0].as_str()).or_default() += 1;
    }
    assert_eq!(corpus.len(), 245);
    assert_eq!(
        types,
        BTreeMap::from([
            ("bool", 12),
            ("float", 32),
            ("inf", 18),
            ("int", 35),
            ("nan", 6),
            ("null", 10),
            ("str", 132),
        ])
    );
    for input in ledger() {
        assert!(
            corpus.contains_key(&input),
            "ledger lists an input the corpus does not have: {input:?}"
        );
    }
}

#[test]
fn every_case_reads_as_the_core_schema_or_is_listed() {
    let corpus = corpus();
    let ledger = ledger();
    let mut failures = Vec::new();
    for (input, case) in &corpus {
        let result = tabnas_yaml::parse(input);
        let matches = matches!(&result, Ok(value) if reads_as_core(value, case));
        let listed = ledger.contains(input);
        if listed && matches {
            failures.push(format!(
                "{input:?} now reads as the core schema says: DELETE its line from \
                 test/yaml-test-schema-deviations.tsv"
            ));
        }
        if !listed && !matches {
            failures.push(format!(
                "{input:?}: want {} {:?}, got {result:?}",
                case[0], case[1]
            ));
        }
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}
