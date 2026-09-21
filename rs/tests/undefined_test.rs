// The UNDEFINED divergence, pinned.
//
// `test/AGENTS.md` describes the bare token UNDEFINED in an expected cell
// as "the parse yielded no document at all", which TypeScript tells apart
// from a document whose value is null and the Go port cannot.
//
// This port tells them apart for ONE input and not for the rest, and the
// reason is the engine rather than the grammar. An empty source returns
// `options.lex.empty_result` directly, which is the engine's `Undefined`.
// Every other source goes through the parse loop, and the value it
// returns passes through `Value::unwrap_undefined`, which replaces every
// `Undefined` in the tree with null before a caller sees it. A document
// stream that accumulated no documents is exactly such a value.
//
// This is a REGISTER row, not a fixture. It asserts the divergence is
// still live, so it fails on REPAIR as loudly as on regression, and the
// repair is the signal to delete this test, the UNDEFINED allowance in
// `parity_test.rs` and the entry in ../../DIVERGENCE.md together.
//
// Measured in all three runtimes, through the parity construction:
//
//  input        TypeScript   Go    Rust
//  ""           undefined    nil   Undefined
//  "   \n "     undefined    nil   Null
//  "..."        undefined    nil   Null
//  "# c\n..."   undefined    nil   Null
//  "null"       null         nil   Null
//
// `...` and `# c\n...` are the two UNDEFINED rows in
// `test/spec/multi-document.tsv`, so they are the inputs the allowance
// exists for.

mod common;

/// The parity construction, which is what the allowance governs.
fn parity_parse(src: &str) -> tabnas::Value {
    tabnas_yaml::make_with(tabnas_yaml::YamlOptions::default())
        .parse(src)
        .unwrap_or_else(|error| panic!("parse {src:?}: {error}"))
}

#[test]
fn a_document_stream_with_no_documents_is_indistinguishable_from_null() {
    // The fixture inputs themselves, not a stand-in.
    for src in ["   \n ", "...", "# c\n..."] {
        let got = parity_parse(src);
        assert!(
            matches!(got, tabnas::Value::Null),
            "a document yielding no value now parses to {got:?} for input {src:?}. \
             This port has grown a real undefined result: DELETE this test, the \
             UNDEFINED allowance in parity_test.rs, and the entry in DIVERGENCE.md \
             that describes them"
        );
    }

    // And it is indistinguishable from an explicit null, which is the
    // divergence, not merely that both are null.
    let null = parity_parse("null");
    assert!(
        matches!(null, tabnas::Value::Null),
        "an explicit null now parses to {null:?}, so it is no longer \
         indistinguishable from a document that yielded nothing; this test and \
         the allowance it guards both need revisiting"
    );

    // The guard that keeps all of the above from being vacuous: a
    // document that IS a value must still come back as that value, or
    // "everything is null" would be satisfied by a parser that returns
    // null for every input.
    let value = parity_parse("a: 1");
    assert!(
        !matches!(value, tabnas::Value::Null | tabnas::Value::Undefined),
        "sanity: a document with a value parsed to nothing through the parity \
         construction, so the null comparisons above prove nothing"
    );
}

/// The one input this port does tell apart: an empty source never reaches
/// the parse loop, so the engine's empty result comes back untouched.
#[test]
fn an_empty_source_keeps_its_undefined() {
    let got = parity_parse("");
    assert!(
        matches!(got, tabnas::Value::Undefined),
        "an empty source now parses to {got:?}; the empty-source result no longer \
         comes back from the engine untouched, so the table in this file's header \
         needs remeasuring"
    );
}
