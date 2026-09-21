// Official YAML Test Suite conformance: the Rust half of the repository's
// conformance dial, and a faithful port of `ts/test/yaml-test-suite.test.ts`
// and `go/yaml_test_suite_test.go`. Same case gathering, same three groups,
// same STRICT comparison, same two shared ledger files.
//
// Corpus: https://github.com/yaml/yaml-test-suite (`data` branch), vendored
// at `test/yaml-test-suite` relative to the repository root. Nothing is
// fetched: the corpus is in the repository, and a missing one fails the
// run rather than skipping it.
//
// EVERY case is asserted. There is no skip list and no group that is merely
// gathered: a conformance suite that quietly does not run reports green
// while measuring nothing.
//
//  valid-parse          in.json present: must parse AND deep-equal it,
//                       strictly, across EVERY document in the stream.
//  expected-errors      `error` present: must be REJECTED, unless listed in
//                       test/yaml-test-suite-lenient.tsv.
//  valid-parse-novalue  neither file: must PARSE, unless listed in
//                       test/yaml-test-suite-unparsed.tsv.

mod common;

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

/// One vendored case.
struct Case {
    id: String,
    dir: PathBuf,
    name: String,
    has_json: bool,
    has_error: bool,
}

fn suite_dir() -> PathBuf {
    common::repo_root().join("test").join("yaml-test-suite")
}

fn ledger_path(name: &str) -> PathBuf {
    common::repo_root().join("test").join(name)
}

/// `<case id> <TAB> <description>`, ignoring `#` comments and blank lines.
/// A ledger that cannot be read is fatal: a check that silently does not
/// run is the failure mode these files exist to prevent.
fn ledger(path: &Path) -> BTreeSet<String> {
    let text = fs::read_to_string(path)
        .unwrap_or_else(|error| panic!("cannot read {}: {error}", path.display()));
    text.lines()
        .map(|line| line.trim_end_matches('\r').trim())
        .filter(|line| !line.is_empty() && !line.starts_with('#'))
        .map(|line| {
            line.split('\t')
                .next()
                .expect("split always yields one field")
                .to_string()
        })
        .collect()
}

/// Gather every case directory, including numbered sub-tests
/// (`AB12/00`, `AB12/01`, ...), skipping non-test directories.
fn gather() -> Vec<Case> {
    let dir = suite_dir();
    let mut names: Vec<String> = fs::read_dir(&dir)
        .unwrap_or_else(|error| panic!("cannot read {}: {error}", dir.display()))
        .filter_map(Result::ok)
        .filter(|entry| entry.path().is_dir() && entry.file_name() != ".git")
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .collect();
    names.sort();

    let make = |id: String, path: &Path| Case {
        name: fs::read_to_string(path.join("==="))
            .map(|text| text.trim().to_string())
            .unwrap_or_else(|_| id.clone()),
        has_json: path.join("in.json").is_file(),
        has_error: path.join("error").is_file(),
        id,
        dir: path.to_path_buf(),
    };

    let mut cases = Vec::new();
    for name in names {
        let path = dir.join(&name);
        let mut subs: Vec<String> = fs::read_dir(&path)
            .unwrap_or_else(|error| panic!("cannot read {}: {error}", path.display()))
            .filter_map(Result::ok)
            .filter(|entry| {
                entry.path().is_dir()
                    && entry
                        .file_name()
                        .to_string_lossy()
                        .chars()
                        .all(|character| character.is_ascii_digit())
            })
            .map(|entry| entry.file_name().to_string_lossy().into_owned())
            .collect();
        subs.sort();

        if !path.join("in.yaml").is_file() && !path.join("===").is_file() {
            let numbered = subs
                .first()
                .is_some_and(|sub| path.join(sub).join("in.yaml").is_file());
            if !numbered {
                continue;
            }
        }

        if subs.is_empty() {
            cases.push(make(name.clone(), &path));
        } else {
            for sub in subs {
                let sub_dir = path.join(&sub);
                if !sub_dir.join("in.yaml").is_file() {
                    continue;
                }
                cases.push(make(format!("{name}/{sub}"), &sub_dir));
            }
        }
    }
    cases
}

/// `in.json` is a STREAM of JSON documents, one per YAML document, not a
/// single JSON value. An empty file is a stream of zero documents, which
/// is a real expectation ("this input yields no document").
fn json_stream(raw: &str) -> Result<Vec<serde_json::Value>, String> {
    let mut docs = Vec::new();
    let mut rest = raw.trim();
    while !rest.is_empty() {
        let bytes = rest.as_bytes();
        let mut depth = 0i32;
        let mut in_string = false;
        let mut escape = false;
        let mut cut = None;
        for index in 0..bytes.len() {
            let character = bytes[index];
            if escape {
                escape = false;
            } else if in_string {
                if character == b'\\' {
                    escape = true;
                } else if character == b'"' {
                    in_string = false;
                }
            } else if character == b'"' {
                in_string = true;
            } else if character == b'{' || character == b'[' {
                depth += 1;
            } else if character == b'}' || character == b']' {
                depth -= 1;
            }
            if in_string || escape || depth != 0 {
                continue;
            }
            // A document boundary is end of input or whitespace, or the
            // bare number 123 would be cut after its first digit.
            if bytes
                .get(index + 1)
                .is_some_and(|next| !next.is_ascii_whitespace())
            {
                continue;
            }
            if let Ok(value) = serde_json::from_str::<serde_json::Value>(&rest[..=index]) {
                docs.push(value);
                cut = Some(index + 1);
                break;
            }
        }
        let Some(cut) = cut else {
            let head: String = rest.chars().take(80).collect();
            return Err(format!("unparseable in.json remainder: {head:?}"));
        };
        rest = rest[cut..].trim();
    }
    Ok(docs)
}

/// Canonicalise a parse result for STRICT structural comparison: nothing
/// here coerces between types, so the string "1" stays a string and will
/// NOT match the number 1. Key order is not part of the expectation,
/// because `in.json` is JSON.
fn canon(value: &tabnas::Value) -> serde_json::Value {
    match value {
        tabnas::Value::Number(number) if number.is_nan() => "@@NaN".into(),
        tabnas::Value::Number(number) if *number == f64::INFINITY => "@@Infinity".into(),
        tabnas::Value::Number(number) if *number == f64::NEG_INFINITY => "@@-Infinity".into(),
        tabnas::Value::Array(items) => serde_json::Value::Array(items.iter().map(canon).collect()),
        tabnas::Value::ListRef(list) => {
            serde_json::Value::Array(list.value.iter().map(canon).collect())
        }
        tabnas::Value::Object(entries) => serde_json::Value::Object(
            entries
                .iter()
                .map(|(key, value)| (key.clone(), canon(value)))
                .collect(),
        ),
        tabnas::Value::MapRef(map) => serde_json::Value::Object(
            map.value
                .iter()
                .map(|(key, value)| (key.clone(), canon(value)))
                .collect(),
        ),
        other => common::whole(other.to_json()),
    }
}

fn read_input(dir: &Path) -> String {
    fs::read_to_string(dir.join("in.yaml"))
        .unwrap_or_else(|error| panic!("cannot read in.yaml in {}: {error}", dir.display()))
        .replace("\r\n", "\n")
}

fn show(value: &serde_json::Value) -> String {
    let text = value.to_string();
    if text.len() > 200 {
        format!("{}...", &text[..200])
    } else {
        text
    }
}

/// Hold a ledger to its group: an id that no longer names a case there
/// would silently excuse a case that does not exist.
fn ledger_ids_are_known(path: &Path, listed: &BTreeSet<String>, cases: &[&Case]) -> Vec<String> {
    let known: BTreeSet<&str> = cases.iter().map(|case| case.id.as_str()).collect();
    let unknown: Vec<String> = listed
        .iter()
        .filter(|id| !known.contains(id.as_str()))
        .cloned()
        .collect();
    if unknown.is_empty() {
        Vec::new()
    } else {
        vec![format!(
            "{} lists ids that are not cases in its group: {}",
            path.display(),
            unknown.join(" ")
        )]
    }
}

#[test]
#[allow(clippy::too_many_lines)]
fn yaml_test_suite() {
    let all = gather();
    let parser = tabnas_yaml::make();
    let mut failures: Vec<String> = Vec::new();

    let valid: Vec<&Case> = all
        .iter()
        .filter(|case| case.has_json && !case.has_error)
        .collect();
    let errors: Vec<&Case> = all.iter().filter(|case| case.has_error).collect();
    let novalue: Vec<&Case> = all
        .iter()
        .filter(|case| !case.has_json && !case.has_error)
        .collect();

    // --- valid-parse ---------------------------------------------------
    for case in &valid {
        let input = read_input(&case.dir);
        let raw = fs::read_to_string(case.dir.join("in.json"))
            .unwrap_or_else(|error| panic!("cannot read in.json for {}: {error}", case.id));
        let docs = match json_stream(&raw) {
            Ok(docs) => docs,
            Err(reason) => {
                failures.push(format!("{} ({}): {reason}", case.id, case.name));
                continue;
            }
        };
        match parser.parse(&input) {
            Err(error) => failures.push(format!(
                "{} ({}): valid document REJECTED: {} {}\n  input: {input:?}",
                case.id, case.name, error.code, error
            )),
            Ok(value) => {
                let got = canon(&value);
                if docs.is_empty() {
                    if !matches!(value, tabnas::Value::Null | tabnas::Value::Undefined) {
                        failures.push(format!(
                            "{} ({}): expected NO document (in.json is empty), got {}\n  input: {input:?}",
                            case.id, case.name, show(&got)
                        ));
                    }
                    continue;
                }
                let want = if docs.len() == 1 {
                    docs[0].clone()
                } else {
                    serde_json::Value::Array(docs)
                };
                let want = common::whole(want);
                if got != want {
                    failures.push(format!(
                        "{} ({}): wrong value\n  input:    {input:?}\n  expected: {}\n  actual:   {}",
                        case.id,
                        case.name,
                        show(&want),
                        show(&got)
                    ));
                }
            }
        }
    }

    // --- expected-errors -----------------------------------------------
    let lenient_file = ledger_path("yaml-test-suite-lenient.tsv");
    let lenient = ledger(&lenient_file);
    failures.extend(ledger_ids_are_known(&lenient_file, &lenient, &errors));
    for case in &errors {
        let input = read_input(&case.dir);
        let accepted = parser.parse(&input);
        if lenient.contains(&case.id) {
            // A documented leniency. If it is now rejected, that is
            // progress: delete the line so the strict expectation applies.
            if let Err(error) = accepted {
                failures.push(format!(
                    "{} ({}) is now REJECTED ({}). Remove its line from {} so the \
                     strict expectation applies from here on.",
                    case.id,
                    case.name,
                    error.code,
                    lenient_file.display()
                ));
            }
            continue;
        }
        if let Ok(value) = accepted {
            failures.push(format!(
                "{} ({}) parsed without error, but the suite marks it invalid and it \
                 is not listed in {}.\n  input:  {input:?}\n  parsed: {}",
                case.id,
                case.name,
                lenient_file.display(),
                show(&canon(&value))
            ));
        }
    }

    // --- valid-parse-novalue -------------------------------------------
    let unparsed_file = ledger_path("yaml-test-suite-unparsed.tsv");
    let unparsed = ledger(&unparsed_file);
    failures.extend(ledger_ids_are_known(&unparsed_file, &unparsed, &novalue));
    for case in &novalue {
        let input = read_input(&case.dir);
        let parsed = parser.parse(&input);
        if unparsed.contains(&case.id) {
            if parsed.is_ok() {
                failures.push(format!(
                    "{} ({}) now PARSES. Remove its line from {} so the expectation \
                     applies from here on.",
                    case.id,
                    case.name,
                    unparsed_file.display()
                ));
            }
            continue;
        }
        if let Err(error) = parsed {
            failures.push(format!(
                "{} ({}): the suite says this is valid YAML but it was REJECTED ({}), \
                 and it is not listed in {}.\n  input: {input:?}",
                case.id,
                case.name,
                error.code,
                unparsed_file.display()
            ));
        }
    }

    // --- census ---------------------------------------------------------
    // Not a conformance score: a guard that the corpus scored against is
    // the whole, expected one. Without it a truncated checkout silently
    // shrinks the denominator and every ratio above it improves for free.
    for (label, got, want) in [
        ("total cases", all.len(), 402),
        ("value-checked cases", valid.len(), 279),
        ("must-fail cases", errors.len(), 94),
        ("parse-only cases", novalue.len(), 29),
    ] {
        if got != want {
            failures.push(format!(
                "{label}: got {got}, want {want} — the corpus at {} is not the expected \
                 one; refusing to report a conformance number against a different \
                 denominator",
                suite_dir().display()
            ));
        }
    }

    assert!(
        failures.is_empty(),
        "{} yaml-test-suite failure(s):\n{}",
        failures.len(),
        failures.join("\n")
    );
}
