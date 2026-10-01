// Cross-runtime conformance, driven by the shared `test/spec/*.tsv`
// fixtures at the repository root (see ../../test/AGENTS.md).
//
// The fixture loader, the escape codec, the `ERROR:<code>` contract and
// the row loop all come from tabnas-support, whose TypeScript and Go
// halves `ts/test/parity.test.ts` and `go/parity_test.go` use to run the
// SAME files, so the three implementations cannot drift without one of
// them going red, and neither can the three loaders.
//
// What is left here is only what is specific to yaml: how to build the
// parser for a row's options, the marker encoding for YAML's non-finite
// numbers, and the opt-in key-order check.

mod common;

use tabnas_support::{is_error_expect, parse_expect, Error, Failure, Row, Runner, Value};

/// The member [`with_key_order`] adds to every mapping.
const KEYS: &str = "@@keys";

/// A row's `keys` cell. KEY ORDER is opt-in, row by row: the runner's
/// comparison ignores it, because ADR-15 keeps key order out of the
/// value contract (a JavaScript object puts an integer-like key first).
/// Where the order IS the behaviour under test, such as where a merge
/// key puts the keys it brings in, the row writes `ordered` there, and
/// [`with_key_order`] then runs on BOTH sides. The TypeScript and Go
/// runners do the same, so the row pins the order in every runtime. See
/// ../../test/AGENTS.md.
fn keys_ordered(row: &Row) -> Result<bool, String> {
    match row.named("keys") {
        "" => Ok(false),
        "ordered" => Ok(true),
        other => Err(format!(
            "keys cell {other:?}: write \"ordered\" or leave it empty"
        )),
    }
}

/// A key JavaScript enumerates first, in numeric order, whatever order
/// the source wrote it in.
fn integer_like(key: &str) -> bool {
    !key.is_empty()
        && key.bytes().all(|byte| byte.is_ascii_digit())
        && (key == "0" || !key.starts_with('0'))
}

/// A copy of `value` in which every mapping lists its keys, in order,
/// under [`KEYS`]. The comparison sees that list as an array, whose order
/// counts. A key that cannot be ordered the same way in every runtime is
/// refused rather than compared: an integer-like key, which JavaScript
/// reorders, and [`KEYS`] itself.
fn with_key_order(value: Value) -> Result<Value, String> {
    match value {
        Value::Array(items) => items
            .into_iter()
            .map(with_key_order)
            .collect::<Result<Vec<_>, _>>()
            .map(Value::Array),
        Value::Object(entries) => {
            let mut keys = Vec::with_capacity(entries.len());
            let mut out = Vec::with_capacity(entries.len() + 1);
            for (key, item) in entries {
                if key == KEYS || integer_like(&key) {
                    return Err(format!(
                        "the key {key:?} cannot be ordered the same way in every \
                         runtime, so a `keys: ordered` row cannot hold it (ADR-15)"
                    ));
                }
                keys.push(Value::String(key.clone()));
                out.push((key, with_key_order(item)?));
            }
            out.push((KEYS.to_string(), Value::Array(keys)));
            Ok(Value::Object(out))
        }
        other => Ok(other),
    }
}

/// The runner every fixture goes through.
///
/// A fresh parser per row, as both other runtimes build one: the `opts`
/// column is per-case, and plugin options must not leak from one row
/// into the next.
fn runner() -> Runner {
    runner_with(|_| {})
}

/// The runner, with `prepare` run on each row's parser before the parse.
fn runner_with(prepare: fn(&mut tabnas::Tabnas)) -> Runner {
    Runner::new_with_row(move |input, row| {
        let raw = row.named("opts");
        let options = if raw.trim().is_empty() {
            tabnas_yaml::YamlOptions::default()
        } else {
            let parsed: serde_json::Value = serde_json::from_str(raw)
                .unwrap_or_else(|error| panic!("{}: opts {raw:?}: {error}", row.location()));
            let meta = parsed
                .get("meta")
                .and_then(serde_json::Value::as_bool)
                .unwrap_or(false);
            for key in parsed
                .as_object()
                .map(|map| map.keys())
                .into_iter()
                .flatten()
            {
                assert!(
                    key == "meta",
                    "{}: tabnas-yaml has no `{key}` option",
                    row.location()
                );
            }
            tabnas_yaml::YamlOptions { meta }
        };
        let mut parser = tabnas_yaml::make_with(options);
        prepare(&mut parser);
        let got = parser
            .parse(input)
            .map(|value| common::canon(&value))
            .map_err(|error| Failure::new(error.code.clone()).with_message(error.to_string()))?;
        // An ERROR row is the runner's business: a parse that should have
        // failed is reported as the value it returned.
        if is_error_expect(row.named("expected")) {
            return Ok(got);
        }
        match keys_ordered(row) {
            Ok(false) => Ok(got),
            Ok(true) => with_key_order(got),
            Err(message) => Err(message),
        }
        .map_err(|message| Failure::new("keys").with_message(message))
    })
    // Input that yields no value at all cannot be spelled in JSON, so
    // the fixtures write the bare token UNDEFINED.
    //
    // In TypeScript that is `undefined`, and distinct from a document
    // whose value is null. This port cannot tell them apart, because the
    // engine replaces every `Undefined` in a finished value with null on
    // the way out, so an UNDEFINED row is satisfied here by null, as it
    // is in Go. The divergence is recorded, not hidden: see
    // `undefined_test.rs` and ../../DIVERGENCE.md.
    .parse_expected(|expected, row| {
        if expected == "UNDEFINED" {
            return Ok(Value::Null);
        }
        let want = parse_expect(expected)?;
        if keys_ordered(row).map_err(Error)? {
            with_key_order(want).map_err(Error)
        } else {
            Ok(want)
        }
    })
}

/// Every fixture in the spec directory. `find_spec_dir` walks up from the
/// crate directory, and `dir` discovers the files by listing, so adding a
/// .tsv runs it in every runtime without touching any runner. An empty
/// directory, and an empty fixture, both fail inside the runner.
#[test]
fn spec() {
    runner().dir(common::spec_dir());
}

/// The same fixtures again, each through a parser that has had a
/// `set_options` call changing nothing. Twin of the "after an unrelated
/// SetOptions" pass in `go/parity_test.go` and `ts/test/parity.test.ts`:
/// the Go port lost its number and text checks to exactly such a call
/// (#81).
#[test]
fn spec_after_an_unrelated_set_options() {
    runner_with(|parser| {
        parser
            .set_options(|_| {})
            .expect("an empty set_options applies");
    })
    .dir(common::spec_dir());
}

/// The census this suite is expected to cover. A fixture renamed or
/// removed would otherwise be a silent loss of coverage: `dir` runs what
/// it finds, and finding less is not a failure to it.
#[test]
fn every_fixture_is_present() {
    let dir = common::spec_dir();
    let expected = [
        "anchors-aliases.tsv",
        "block-mappings.tsv",
        "block-scalars.tsv",
        "block-sequences.tsv",
        "comments.tsv",
        "complex-keys.tsv",
        "directives.tsv",
        "flow-collections.tsv",
        "happy.tsv",
        "indentation.tsv",
        "issue-regressions.tsv",
        "line-endings.tsv",
        "merge-key.tsv",
        "meta.tsv",
        "multi-document.tsv",
        "multiline-plain-scalars.tsv",
        "quoted-strings.tsv",
        "real-world-regressions.tsv",
        "real-world.tsv",
        "scalar-types.tsv",
        "sequence-of-mappings.tsv",
        "special-chars-in-values.tsv",
        "suite-basic.tsv",
        "suite-flow.tsv",
        "suite-realworld.tsv",
        "suite-scalars.tsv",
        "suite-structure.tsv",
        "tags.tsv",
    ];
    for name in expected {
        assert!(dir.join(name).is_file(), "missing fixture {name}");
    }

    let mut found: Vec<String> = std::fs::read_dir(&dir)
        .expect("the spec directory is readable")
        .filter_map(Result::ok)
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .filter(|name| name.ends_with(".tsv"))
        .collect();
    found.sort();
    assert_eq!(
        found.len(),
        expected.len(),
        "the spec directory holds {found:?}, but this suite lists {} files; \
         add the new fixture to the list above",
        expected.len()
    );
}
