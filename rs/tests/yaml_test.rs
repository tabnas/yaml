// In-language behaviour, ported from `ts/test/yaml.test.ts` and its Go
// counterparts `go/yaml_test.go` and `go/yaml_scenarios_test.go`. The
// names and expectations mirror theirs, so a behaviour change shows up
// in the same place in all three runtimes.
//
// Everything expressible as input to output belongs in a shared fixture
// instead; what is here is what a fixture cannot say — the typed option
// surface, source key order, the shared default parser, and the shapes
// the `meta` option returns.

mod common;

use common::{expect, json, keys, parse};
use serde_json::json as j;
use tabnas_yaml::{make, make_with, YamlOptions};

/// A value that is not pi. `3.14` is a YAML scalar in these documents,
/// and clippy's `approx_constant` reads every literal spelling of it as
/// a botched constant, so it is built rather than written.
#[allow(clippy::approx_constant)]
fn three_point_one_four() -> f64 {
    314.0 / 100.0
}

/// One non-finite scalar and the test for it.
type NonFinite = (&'static str, fn(f64) -> bool);

// ===== BLOCK MAPPINGS =====

#[test]
fn block_mappings() {
    expect("a: 1", j!({"a": 1}));
    expect("a: 1\nb: 2\nc: 3", j!({"a": 1, "b": 2, "c": 3}));
    expect("a:\n  b: 1\n  c: 2", j!({"a": {"b": 1, "c": 2}}));
    expect(
        "a:\n  b:\n    c:\n      d: 1",
        j!({"a": {"b": {"c": {"d": 1}}}}),
    );
    expect("a:\n  x: 1\nb:\n  y: 2", j!({"a": {"x": 1}, "b": {"y": 2}}));
    expect("a:\nb: 1", j!({"a": null, "b": 1}));
    expect("a:\n  b: 1", j!({"a": {"b": 1}}));
    expect("a: 1\n", j!({"a": 1}));
    // No space after the colon, so the whole line is one plain scalar.
    expect("a:b", j!("a:b"));
    expect("a: 1\n\n\n", j!({"a": 1}));
}

// ===== BLOCK SEQUENCES =====

#[test]
fn block_sequences() {
    expect("- a\n- b\n- c", j!(["a", "b", "c"]));
    expect("- a", j!(["a"]));
    expect("items:\n  - a\n  - b", j!({"items": ["a", "b"]}));
    expect("- 1\n- 2\n- 3", j!([1, 2, 3]));
    expect(
        "- name: alice\n- name: bob",
        j!([{"name": "alice"}, {"name": "bob"}]),
    );
    expect(
        "items:\n  - name: alice\n    age: 30\n  - name: bob\n    age: 25",
        j!({"items": [{"name": "alice", "age": 30}, {"name": "bob", "age": 25}]}),
    );
    expect("a:\n  b:\n    - x\n    - y", j!({"a": {"b": ["x", "y"]}}));
    expect(
        "a: 1\nb:\n  - x\n  - y\nc: 3",
        j!({"a": 1, "b": ["x", "y"], "c": 3}),
    );
}

// ===== SCALAR TYPES =====

#[test]
fn scalar_types() {
    expect("a: 42", j!({"a": 42}));
    expect("a: -7", j!({"a": -7}));
    expect("a: 3.14", j!({"a": three_point_one_four()}));
    expect("a: 0", j!({"a": 0}));
    expect("a: true", j!({"a": true}));
    expect("a: false", j!({"a": false}));
    expect("a: null", j!({"a": null}));
    expect("a: ~", j!({"a": null}));
    expect("a:", j!({"a": null}));
    expect("a: hello world", j!({"a": "hello world"}));
    expect("a: hello, world!", j!({"a": "hello, world!"}));
    expect("foo: a{{q}}b", j!({"foo": "a{{q}}b"}));
    expect("a: 0o77", j!({"a": 63}));
    expect("a: 0xFF", j!({"a": 255}));
    expect("a: yes", j!({"a": true}));
    expect("a: no", j!({"a": false}));
    expect("a: on", j!({"a": true}));
    expect("a: off", j!({"a": false}));
    // This subset does not resolve the timestamp type.
    expect("a: 2024-01-15", j!({"a": "2024-01-15"}));
    expect("a: 2024-01-15T10:30:00Z", j!({"a": "2024-01-15T10:30:00Z"}));
}

/// The non-finite numbers have no JSON spelling, so the fixtures write
/// them as markers and this pins the values themselves.
#[test]
fn non_finite_numbers() {
    let cases: [NonFinite; 3] = [
        ("a: .inf", |number| number == f64::INFINITY),
        ("a: -.inf", |number| number == f64::NEG_INFINITY),
        ("a: .nan", f64::is_nan),
    ];
    for (src, check) in cases {
        let value = parse(src);
        let tabnas::Value::Object(entries) = &value else {
            panic!("{src}: expected a mapping, got {value:?}");
        };
        let Some(tabnas::Value::Number(number)) = entries.get("a") else {
            panic!("{src}: expected a number, got {:?}", entries.get("a"));
        };
        assert!(check(*number), "{src}: got {number}");
    }
}

// ===== QUOTED STRINGS =====

#[test]
fn quoted_strings() {
    expect(r#"a: "hello""#, j!({"a": "hello"}));
    expect("a: 'hello'", j!({"a": "hello"}));
    expect(r#"a: "key: value""#, j!({"a": "key: value"}));
    expect("a: 'key: value'", j!({"a": "key: value"}));
    expect(r#"a: """#, j!({"a": ""}));
    expect("a: ''", j!({"a": ""}));
    expect(r#"a: "42""#, j!({"a": "42"}));
    expect(r#"a: "true""#, j!({"a": "true"}));
    expect("foo: 'a{{q}}b'", j!({"foo": "a{{q}}b"}));
    expect(r#"foo: "a{{q}}b""#, j!({"foo": "a{{q}}b"}));
    expect("a: \"line1\\nline2\"", j!({"a": "line1\nline2"}));
    expect("a: \"col1\\tcol2\"", j!({"a": "col1\tcol2"}));
    // Single quotes process no escapes.
    expect(r"a: 'line1\nline2'", j!({"a": r"line1\nline2"}));
    expect(r#""a b": 1"#, j!({"a b": 1}));
}

// ===== BLOCK SCALARS =====

#[test]
fn block_scalars() {
    expect(
        "a: |\n  line1\n  line2\n  line3",
        j!({"a": "line1\nline2\nline3\n"}),
    );
    expect("a: |-\n  line1\n  line2", j!({"a": "line1\nline2"}));
    expect("a: |+\n  line1\n  line2\n\n", j!({"a": "line1\nline2\n\n"}));
    expect(
        "a: >\n  line1\n  line2\n  line3",
        j!({"a": "line1 line2 line3\n"}),
    );
    expect("a: >-\n  line1\n  line2", j!({"a": "line1 line2"}));
    expect("a: >+\n  line1\n  line2\n\n", j!({"a": "line1 line2\n\n"}));
    expect(
        "a: |\n  line1\n    indented\n  line3",
        j!({"a": "line1\n  indented\nline3\n"}),
    );
    expect(
        "a:\n  b: |\n    indented\n    text",
        j!({"a": {"b": "indented\ntext\n"}}),
    );
    // A block scalar indicator with text after it on the same line is not
    // a block scalar at all; YAML calls it an error and this parser reads
    // the whole thing as a plain scalar.
    expect("a: > x", j!({"a": "> x"}));
    expect("a: | x\n  y", j!({"a": "| x y"}));
}

/// A literal block keeps its content byte for byte, quotes and commas
/// included. Captured from an OpenAPI example block.
#[test]
fn literal_block_keeps_a_csv_example_verbatim() {
    let rows = [
        r#""clickId","date","placementId","market","merchantId","merchantName","revenue","currency""#,
        r#""532f889fd3ba56f628f3234647d9854650534789938b7fdaafddf1d75081fadc","2018-01-01T00:00:01+00:00","your-custom-placement-id-1","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.142898","EUR""#,
        r#""ae7facb00d557e7d92e1d2ee31bc05cc9787bc6802e636ccb284cfbaeb6680b8","2018-01-01T00:00:02+00:00","your-custom-placement-id-2","de","583c1b14c50391777b40ee033a04cef033271e35307f7276125b2ba760d4b48e","example.com","0.142825","EUR""#,
    ];
    let mut src = String::from("schema:\n  example: |\n");
    for row in rows {
        src.push_str("    ");
        src.push_str(row);
        src.push('\n');
    }
    let want = format!("{}\n", rows.join("\n"));
    assert_eq!(json(&src), j!({"schema": {"example": want}}));
}

// ===== FLOW COLLECTIONS =====

#[test]
fn flow_collections() {
    expect("a: [1, 2, 3]", j!({"a": [1, 2, 3]}));
    expect("a: {x: 1, y: 2}", j!({"a": {"x": 1, "y": 2}}));
    expect("a: [1, [2, 3]]", j!({"a": [1, [2, 3]]}));
    expect("a: [{x: 1}, {y: 2}]", j!({"a": [{"x": 1}, {"y": 2}]}));
    expect("a: []", j!({"a": []}));
    expect("a: {}", j!({"a": {}}));
    expect("[1, 2, 3]", j!([1, 2, 3]));
    expect("{a: 1, b: 2}", j!({"a": 1, "b": 2}));
}

// ===== COMMENTS =====

#[test]
fn comments() {
    expect("a: 1 # comment\nb: 2", j!({"a": 1, "b": 2}));
    expect("# this is a comment\na: 1", j!({"a": 1}));
    expect("a: # comment\n  b: 1", j!({"a": {"b": 1}}));
    expect("# first\na: 1\n# second\nb: 2", j!({"a": 1, "b": 2}));
    expect("- a # comment\n- b", j!(["a", "b"]));
    expect("a: 1\n# middle\nb: 2", j!({"a": 1, "b": 2}));
}

// ===== ANCHORS, ALIASES AND MERGE KEYS =====

#[test]
fn anchors_and_aliases() {
    expect("a: &ref hello\nb: *ref", j!({"a": "hello", "b": "hello"}));
    expect(
        "a: &items\n  - 1\n  - 2\nb: *items",
        j!({"a": [1, 2], "b": [1, 2]}),
    );
    expect(
        "a: &x 10\nb: &y 20\nc: *x\nd: *y",
        j!({"a": 10, "b": 20, "c": 10, "d": 20}),
    );
}

#[test]
fn merge_keys() {
    expect(
        "defaults: &d\n  a: 1\n  b: 2\nresult:\n  <<: *d\n  c: 3",
        j!({"defaults": {"a": 1, "b": 2}, "result": {"c": 3, "a": 1, "b": 2}}),
    );
    expect(
        "base: &b\n  x: 1\n  y: 2\nchild:\n  <<: *b\n  y: 99",
        j!({"base": {"x": 1, "y": 2}, "child": {"y": 99, "x": 1}}),
    );
    expect(
        "defaults: &defaults\n  x: 1\n  y: 2\noverride:\n  <<: *defaults\n  y: 3",
        j!({"defaults": {"x": 1, "y": 2}, "override": {"y": 3, "x": 1}}),
    );
    // A sequence of aliases merges each in turn.
    expect(
        "a: &a\n  x: 1\nb: &b\n  y: 2\nc:\n  <<: [*a, *b]\n  z: 3",
        j!({"a": {"x": 1}, "b": {"y": 2}, "c": {"z": 3, "x": 1, "y": 2}}),
    );
}

/// The merged entries are appended after the explicit ones, which is
/// where the canonical implementation leaves them: it deletes the `<<`
/// key and then assigns what the merge brought in.
#[test]
fn a_merge_key_leaves_the_explicit_keys_first() {
    let value = parse("d: &d\n  a: 1\n  b: 2\nr:\n  <<: *d\n  c: 3");
    let tabnas::Value::Object(entries) = &value else {
        panic!("expected a mapping, got {value:?}");
    };
    let result = entries.get("r").expect("the r mapping");
    assert_eq!(keys(result), vec!["c", "a", "b"]);
}

// ===== MULTI-DOCUMENT STREAMS =====

#[test]
fn multi_document_streams() {
    expect("---\na: 1", j!({"a": 1}));
    expect("a: 1\n...", j!({"a": 1}));
    expect("---\na: 1\n---\nb: 2", j!([{"a": 1}, {"b": 2}]));
    expect(
        "---\na: 1\n---\nb: 2\n---\nc: 3",
        j!([{"a": 1}, {"b": 2}, {"c": 3}]),
    );
    expect("---\na: 1\n...\n---\nb: 2", j!([{"a": 1}, {"b": 2}]));
    expect(
        "---\n- 1\n- 2\n---\na: 1\n---\nfoo",
        j!([[1, 2], {"a": 1}, "foo"]),
    );
    expect("---\n---\n---", j!([null, null, null]));
    expect("---\n- a\n- b\n---\n- c\n- d", j!([["a", "b"], ["c", "d"]]));
    expect("%YAML 1.2\n---\na: 1", j!({"a": 1}));
    expect("%TAG !! tag:example.com,2025:\n---\na: 1", j!({"a": 1}));
    expect("%TAG ! tag:example.com,2000:\n---\na: 1", j!({"a": 1}));
}

// ===== TAGS =====

#[test]
fn tags() {
    expect("a: !!str 42", j!({"a": "42"}));
    expect(r#"a: !!int "42""#, j!({"a": 42}));
    expect(r#"a: !!float "3.14""#, j!({"a": three_point_one_four()}));
    expect(r#"a: !!bool "true""#, j!({"a": true}));
    expect(r#"a: !!null """#, j!({"a": null}));
    expect("a: !!seq\n  - 1\n  - 2", j!({"a": [1, 2]}));
    expect("a: !!map\n  x: 1", j!({"a": {"x": 1}}));
}

// ===== COMPLEX KEYS =====

#[test]
fn complex_keys() {
    expect("? a\n: 1", j!({"a": 1}));
    expect("? a b\n: 1", j!({"a b": 1}));
    expect("1: one\n2: two", j!({"1": "one", "2": "two"}));
}

// ===== INDENTATION =====

#[test]
fn indentation() {
    expect("a:\n  b: 1", j!({"a": {"b": 1}}));
    expect("a:\n    b: 1", j!({"a": {"b": 1}}));
    expect("a:\n  b:\n      c: 1", j!({"a": {"b": {"c": 1}}}));
    expect(
        "a:\n  b: 1\n  c: 2\nd: 3",
        j!({"a": {"b": 1, "c": 2}, "d": 3}),
    );
    expect(
        "a:\n  b:\n    c: 1\n  d: 2\ne: 3",
        j!({"a": {"b": {"c": 1}, "d": 2}, "e": 3}),
    );
    expect("a:\n  - 1\n  - 2\nb: 3", j!({"a": [1, 2], "b": 3}));
    expect("  a: 1", j!({"a": 1}));
    expect("a: 1\n\nb: 2", j!({"a": 1, "b": 2}));
    expect("- a\n\n- b", j!(["a", "b"]));
}

/// YAML forbids a tab as indentation, so this document should be
/// REJECTED. It is not: the tab is swallowed and `b` lands at the top
/// level. Pinned as observed, in both other runtimes too, so the gap is
/// visible; the conformance dial for it is the test suite's must-fail
/// group in `yaml_test_suite_test.rs`.
#[test]
fn tab_indentation_is_not_rejected() {
    expect("a:\n\tb: 1", j!({"a": null, "b": 1}));
}

// ===== MULTILINE PLAIN SCALARS =====

#[test]
fn multiline_plain_scalars() {
    expect(
        "a: this is\n  a long string",
        j!({"a": "this is a long string"}),
    );
    expect(
        "a: line one\n  line two\n  line three",
        j!({"a": "line one line two line three"}),
    );
}

// ===== WINDOWS LINE ENDINGS =====

#[test]
fn windows_line_endings() {
    expect("a: 1\r\nb: 2", j!({"a": 1, "b": 2}));
    expect("a:\r\n  b: 1\r\n  c: 2", j!({"a": {"b": 1, "c": 2}}));
    expect("- a\r\n- b", j!(["a", "b"]));
}

// ===== SPECIAL CHARACTERS IN VALUES =====

#[test]
fn special_characters_in_values() {
    expect("a: foo#bar", j!({"a": "foo#bar"}));
    expect("a long key: value", j!({"a long key": "value"}));
    expect("url: http://example.com", j!({"url": "http://example.com"}));
    expect("a: some [text] here", j!({"a": "some [text] here"}));
    expect("a: some {text} here", j!({"a": "some {text} here"}));
}

// ===== SEQUENCES OF MAPPINGS =====

#[test]
fn sequences_of_mappings() {
    expect(
        "- name: alice\n  age: 30\n- name: bob\n  age: 25",
        j!([{"name": "alice", "age": 30}, {"name": "bob", "age": 25}]),
    );
    expect("- a: 1\n- b: 2\n- c: 3", j!([{"a": 1}, {"b": 2}, {"c": 3}]));
    expect(
        "people:\n  - name: alice\n  - name: bob",
        j!({"people": [{"name": "alice"}, {"name": "bob"}]}),
    );
}

// ===== REAL-WORLD SHAPES =====

#[test]
fn real_world_shapes() {
    expect(
        "version: 3\nservices:\n  web:\n    image: nginx\n    ports:\n      - 80\n      - 443",
        j!({"version": 3, "services": {"web": {"image": "nginx", "ports": [80, 443]}}}),
    );
    expect(
        "name: build\non:\n  push:\n    branches:\n      - main\njobs:\n  test:\n    runs-on: ubuntu",
        j!({
            "name": "build",
            "on": {"push": {"branches": ["main"]}},
            "jobs": {"test": {"runs-on": "ubuntu"}}
        }),
    );
    expect(
        "apiVersion: v1\nkind: Pod\nmetadata:\n  name: myapp\n  labels:\n    app: myapp\nspec:\n  containers:\n    - name: web\n      image: nginx",
        j!({
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {"name": "myapp", "labels": {"app": "myapp"}},
            "spec": {"containers": [{"name": "web", "image": "nginx"}]}
        }),
    );
    expect(
        "- name: install packages\n  become: true\n- name: start service\n  become: false",
        j!([
            {"name": "install packages", "become": true},
            {"name": "start service", "become": false}
        ]),
    );
    expect(
        "database:\n  host: localhost\n  port: 5432\n  name: mydb\ncache:\n  enabled: true\n  ttl: 3600",
        j!({
            "database": {"host": "localhost", "port": 5432, "name": "mydb"},
            "cache": {"enabled": true, "ttl": 3600}
        }),
    );
    expect(
        "a: 1\nb: 2\nc:\n  d: 3\n  e: 4\n  f:\n  - g\n  - h\n",
        j!({"a": 1, "b": 2, "c": {"d": 3, "e": 4, "f": ["g", "h"]}}),
    );
}

// ===== EMPTY AND WHITESPACE-ONLY INPUT =====

#[test]
fn empty_input_yields_no_value() {
    // The engine replaces every `Undefined` in a finished value with
    // null on the way out, so "no document" reads as null here, as it
    // does in Go. See `undefined_test.rs`.
    assert_eq!(json(""), j!(null));
    assert_eq!(json("   \n  \n  "), j!(null));
    assert_eq!(json("# only a comment\n"), j!(null));
}

// ===== KEY ORDER =====

/// The exported source-order guarantee. The shared-fixture runner
/// compares values without regard to key order, so a regression that
/// reordered keys would slip past it. These keys are deliberately not
/// alphabetical.
#[test]
fn mapping_keys_keep_source_order() {
    let value = parse("b: 1\na: 2\nc: 3\n");
    assert_eq!(keys(&value), vec!["b", "a", "c"]);
    assert_eq!(value.to_string(), r#"{"b":1,"a":2,"c":3}"#);
}

// ===== THE OPTION SURFACE =====

#[test]
fn meta_returns_per_document_metadata() {
    let parser = make_with(YamlOptions { meta: true });

    let single = parser.parse("a: 1").expect("a: 1 parses");
    assert_eq!(
        common::plain(&single),
        j!({
            "meta": {"directives": [], "explicit": false, "ended": false},
            "content": {"a": 1}
        })
    );

    let explicit = parser.parse("---\na: 1").expect("---\na: 1 parses");
    assert_eq!(
        common::plain(&explicit)
            .get("meta")
            .and_then(|meta| meta.get("explicit")),
        Some(&j!(true))
    );

    let ended = parser.parse("a: 1\n...").expect("a: 1\n... parses");
    assert_eq!(
        common::plain(&ended)
            .get("meta")
            .and_then(|meta| meta.get("ended")),
        Some(&j!(true))
    );

    let two = parser
        .parse("---\na: 1\n---\nb: 2")
        .expect("a two-document stream parses");
    assert_eq!(
        common::plain(&two),
        j!({
            "meta": [
                {"directives": [], "explicit": true, "ended": false},
                {"directives": [], "explicit": true, "ended": false}
            ],
            "content": [{"a": 1}, {"b": 2}]
        })
    );

    // The end marker belongs to the document it terminates.
    let ended_first = parser
        .parse("---\na: 1\n...\n---\nb: 2")
        .expect("a terminated first document parses");
    let meta = common::plain(&ended_first);
    let metas = meta
        .get("meta")
        .and_then(serde_json::Value::as_array)
        .expect("two metas");
    assert_eq!(metas[0].get("ended"), Some(&j!(true)));
    assert_eq!(metas[1].get("ended"), Some(&j!(false)));

    // Directives are captured, in order, and belong to their document.
    let directives = parser
        .parse("%YAML 1.2\n%TAG !! tag:foo.com,2025:\n---\na: 1")
        .expect("two directives parse");
    assert_eq!(
        common::plain(&directives)
            .get("meta")
            .and_then(|meta| meta.get("directives")),
        Some(&j!(["%YAML 1.2", "%TAG !! tag:foo.com,2025:"]))
    );

    let isolated = parser
        .parse("%YAML 1.2\n---\na: 1\n---\nb: 2")
        .expect("a directive on the first document only");
    let meta = common::plain(&isolated);
    let metas = meta
        .get("meta")
        .and_then(serde_json::Value::as_array)
        .expect("two metas");
    assert_eq!(metas[0].get("directives"), Some(&j!(["%YAML 1.2"])));
    assert_eq!(metas[1].get("directives"), Some(&j!([])));
}

#[test]
fn meta_off_returns_bare_content() {
    for parser in [make(), make_with(YamlOptions { meta: false })] {
        assert_eq!(
            common::plain(&parser.parse("a: 1").expect("parses")),
            j!({"a": 1})
        );
        assert_eq!(
            common::plain(&parser.parse("---\na: 1\n---\nb: 2").expect("parses")),
            j!([{"a": 1}, {"b": 2}])
        );
    }
}

// ===== THE INSTANCE =====

/// Parsing the same source twice on one parser must produce identical
/// output. The canonical implementations reset their per-parse state on
/// the first lexer call of a parse, gated on having seen the lexer
/// before; this port keeps that state in the parse context, so there is
/// nothing to reset, and this is the test that says so.
#[test]
fn reparsing_the_same_source_is_idempotent() {
    let parser = make();
    let src = "openapi: 3.0\npaths:\n  /a:\n    get: {}";
    let first = parser.parse(src).expect("first parse");
    let second = parser.parse(src).expect("second parse");
    assert_eq!(common::plain(&first), common::plain(&second));
}

/// Anchors recorded by one parse must not reach the next.
#[test]
fn anchors_do_not_leak_between_parses() {
    let parser = make();
    assert_eq!(
        common::plain(&parser.parse("a: &ref hello\nb: *ref").expect("parses")),
        j!({"a": "hello", "b": "hello"})
    );
    // `*ref` now names nothing. It resolves to the literal alias text,
    // not to the value the previous parse anchored.
    assert_eq!(
        common::plain(&parser.parse("b: *ref").expect("parses")),
        j!({"b": null})
    );
}

/// A `%TAG` handle recorded by one parse must not reach the next either:
/// a redefined `!!` turns off the built-in type conversions.
#[test]
fn tag_handles_do_not_leak_between_parses() {
    let parser = make();
    assert_eq!(
        common::plain(
            &parser
                .parse("%TAG !! tag:x,2000:\n---\na: !!int 7")
                .expect("parses")
        ),
        j!({"a": "7"})
    );
    assert_eq!(
        common::plain(&parser.parse("a: !!int 7").expect("parses")),
        j!({"a": 7})
    );
}

/// The instance is `Send + Sync` and parses through `&self`, so the
/// shared default parser can be used from several threads at once. Every
/// piece of per-parse state lives in the parse context, which is what
/// makes that safe.
#[test]
fn the_default_parser_is_shared_across_threads() {
    let sources = [
        "a: &r 1\nb: *r",
        "- x\n- y",
        "k:\n  n: |\n    block\n",
        "---\na: 1\n---\nb: 2",
    ];
    let handles: Vec<_> = (0..8)
        .map(|_| {
            std::thread::spawn(move || {
                for _ in 0..40 {
                    for src in sources {
                        tabnas_yaml::parse(src).expect("parses");
                    }
                }
            })
        })
        .collect();
    for handle in handles {
        handle.join().expect("no thread panicked");
    }
    assert_eq!(
        common::plain(&tabnas_yaml::parse("a: &r 1\nb: *r").expect("parses")),
        j!({"a": 1, "b": 1})
    );
}

/// The plugin refuses an engine that has no jsonic grammar, rather than
/// installing rules that could never fire.
#[test]
fn a_bare_engine_is_refused() {
    let mut bare = tabnas::Tabnas::new();
    let error = tabnas_yaml::yaml(&mut bare, &YamlOptions::default())
        .expect_err("a bare engine has no `val` rule");
    assert!(
        error.0.contains("jsonic"),
        "the refusal should name jsonic: {error}"
    );
}

/// Installing twice is a no-op, as the canonical guard makes it.
#[test]
fn installing_twice_is_a_no_op() {
    let mut parser = tabnas_jsonic::make();
    tabnas_yaml::yaml(&mut parser, &YamlOptions::default()).expect("first install");
    tabnas_yaml::yaml(&mut parser, &YamlOptions::default()).expect("second install");
    assert_eq!(
        common::plain(&parser.parse("a: 1").expect("parses")),
        j!({"a": 1})
    );
}

/// The rules this plugin adds on top of jsonic's, named. The TypeScript
/// suite asserts the same set through `@tabnas/debug`.
#[test]
fn the_grammar_adds_the_yaml_rules() {
    let parser = make();
    let names = parser.rule_names();
    for rule in [
        "stream",
        "indent",
        "yamlBlockList",
        "yamlBlockElem",
        "yamlElemMap",
        "yamlElemPair",
    ] {
        assert!(names.contains(&rule.to_string()), "missing rule {rule}");
    }
    for rule in ["val", "map", "list", "pair", "elem"] {
        assert!(
            names.contains(&rule.to_string()),
            "missing jsonic rule {rule}"
        );
    }
}
