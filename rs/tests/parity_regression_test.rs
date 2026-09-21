// Regression tests for the TypeScript/Go parity bugs that produced
// "unexpected character(s)" errors in real-world OpenAPI and Swagger
// YAML files. Each case is a TypeScript-valid snippet that the Go parser
// once rejected, ported from `go/parity_regression_test.go`.
//
// These live here rather than in `parity_test.rs`, which holds the runner
// for the shared fixtures: a fixture pins a whole parse result, while
// these pin the specific shapes each historical bug corrupted.

mod common;

use common::json;
use serde_json::json as j;

/// Captured from the GitHub OpenAPI spec (`example: id: 12,` lines under
/// `examples:` blocks).
///
/// Trigger: a value that starts with a digit and ends with a comma in
/// block context, not in a flow collection. The number matcher grabs `12`
/// and leaves `,` as a stray fixed token, and the next line then fails.
///
/// The digit branch of the YAML matcher detects the trailing comma and
/// emits the whole `12,` as text before the number matcher fires.
#[test]
fn trailing_comma_after_a_digit_in_block_context() {
    assert_eq!(
        json("value:\n  id: 12,\n  public_repo: false,\n  title: Intro\n"),
        j!({"value": {"id": "12,", "public_repo": "false,", "title": "Intro"}})
    );
}

/// Captured from the GitLab Swagger spec (path keys with the
/// `? long-key\n: get:\n  ... put: ...` pattern).
///
/// Trigger: an explicit key whose value is on the next line starting with
/// `: <key>:`, so the value is a block mapping that begins on the same
/// line as the explicit-key colon. Emitting only a colon leaves no indent
/// token to establish the inner mapping's context, and the second method
/// is rejected.
///
/// The explicit-key handler detects inline content that itself opens a
/// block mapping or sequence and queues a colon AND an indent token.
///
/// The canonical implementation keeps the surrounding quotes as part of
/// the explicit-key text, so the key here is `"/api/foo"` with quotes.
#[test]
fn an_explicit_key_whose_value_is_an_inline_block_mapping() {
    assert_eq!(
        json(concat!(
            "paths:\n",
            "  ? \"/api/foo\"\n",
            "  : get:\n",
            "      summary: get foo\n",
            "    put:\n",
            "      summary: put foo\n",
        )),
        j!({"paths": {"\"/api/foo\"": {
            "get": {"summary": "get foo"},
            "put": {"summary": "put foo"}
        }}})
    );
}

/// Captured from the Codat OpenAPI spec (description strings containing
/// `[Codat's ...]`).
///
/// Trigger: an apostrophe inside a double-quoted YAML string reaches the
/// incremental flow-context scan. Without telling an apostrophe inside a
/// word from a single-quote opener, the scan enters "inside a quoted
/// scalar" and miscounts the flow depth for the rest of the file,
/// producing an unexpected-token error far away.
#[test]
fn an_apostrophe_inside_a_double_quoted_string() {
    assert_eq!(
        json(concat!(
            "paths:\n",
            "  /a:\n",
            "    get:\n",
            "      description: \"See [Codat's docs](https://example.com/x) for more info.\"\n",
            "      tags:\n",
            "        - api\n",
        )),
        j!({"paths": {"/a": {"get": {
            "description": "See [Codat's docs](https://example.com/x) for more info.",
            "tags": ["api"]
        }}}})
    );
}

/// The exported source-order guarantee. The shared-fixture runner compares
/// values without regard to key order, so a regression that reordered keys
/// would slip past it. These keys are deliberately not alphabetical.
#[test]
fn mapping_key_source_order() {
    let value = common::parse("b: 1\na: 2\nc: 3\n");
    assert_eq!(common::keys(&value), vec!["b", "a", "c"]);
    assert_eq!(value.to_string(), r#"{"b":1,"a":2,"c":3}"#);
}
