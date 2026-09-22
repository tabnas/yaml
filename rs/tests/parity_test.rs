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
// parser for a row's options, and the marker encoding for YAML's
// non-finite numbers.

mod common;

use tabnas_support::{parse_expect, Failure, Runner, Value};

/// The runner every fixture goes through.
///
/// A fresh parser per row, as both other runtimes build one: the `opts`
/// column is per-case, and plugin options must not leak from one row
/// into the next.
fn runner() -> Runner {
    Runner::new_with_row(|input, row| {
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
        tabnas_yaml::make_with(options)
            .parse(input)
            .map(|value| common::canon(&value))
            .map_err(|error| Failure::new(error.code.clone()).with_message(error.to_string()))
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
    .parse_expected(|expected, _row| {
        if expected == "UNDEFINED" {
            Ok(Value::Null)
        } else {
            parse_expect(expected)
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
