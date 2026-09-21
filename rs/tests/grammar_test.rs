// The embedded grammar is generated, and this is what keeps it honest.
//
// `yaml-grammar.jsonic` at the repository root is the single source of
// the grammar. `ts/embed-grammar.js` copies it verbatim into
// `ts/src/yaml.ts`, `go/yaml.go` and `rs/src/lib.rs`, so the three
// runtimes do not each have a grammar: they have THE grammar. A
// hand-edit between the markers, or a change to the `.jsonic` with no
// re-embed, would silently give this port a grammar of its own.

mod common;

use std::fs;

const BEGIN: &str = "// --- BEGIN EMBEDDED yaml-grammar.jsonic ---";
const END: &str = "// --- END EMBEDDED yaml-grammar.jsonic ---";

/// The text between the markers in a source file.
fn embedded(source: &str, open: &str, close: &str) -> String {
    let start = source
        .find(BEGIN)
        .unwrap_or_else(|| panic!("no BEGIN marker"))
        + BEGIN.len();
    let end = source.find(END).unwrap_or_else(|| panic!("no END marker"));
    let block = &source[start..end];
    let body = block
        .trim_start_matches('\n')
        .strip_prefix(open)
        .unwrap_or_else(|| panic!("the embedded block does not open with {open:?}"));
    let body = body
        .trim_end()
        .strip_suffix(close)
        .unwrap_or_else(|| panic!("the embedded block does not close with {close:?}"));
    // The generator writes a newline straight after the opener.
    body.strip_prefix('\n').unwrap_or(body).to_string()
}

#[test]
fn the_embedded_grammar_is_the_file_on_disk() {
    let root = common::repo_root();
    let source = fs::read_to_string(root.join("yaml-grammar.jsonic"))
        .expect("yaml-grammar.jsonic sits at the repository root");
    let here = fs::read_to_string(root.join("rs").join("src").join("lib.rs"))
        .expect("this crate's lib.rs is readable");
    assert_eq!(
        embedded(&here, "const GRAMMAR_TEXT: &str = r##\"", "\"##;"),
        source,
        "rs/src/lib.rs holds a grammar that is not yaml-grammar.jsonic. \
         Edit the .jsonic and run `node embed-grammar.js` in ts/; never \
         hand-edit between the markers."
    );
}

/// Undo what the embedder does to fit the grammar into a TypeScript
/// template literal: it doubles every backslash, and puts one in front
/// of a backtick and of a `${`. A backslash in the embedded text
/// therefore always escapes the character after it, so one pass left to
/// right is the exact inverse. Without this the comparison below holds
/// escaped text against unescaped text, and would fail the moment the
/// grammar carried a real backslash.
fn decode_template_literal(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut characters = text.chars();
    while let Some(character) = characters.next() {
        if character == '\\' {
            if let Some(escaped) = characters.next() {
                out.push(escaped);
                continue;
            }
        }
        out.push(character);
    }
    out
}

/// And the same text is in the other two runtimes, so a re-embed that
/// reached only one of them is caught here too. The Go copy is a raw
/// string and needs no decoding; the TypeScript one is a template
/// literal and does.
#[test]
fn every_runtime_embeds_the_same_grammar() {
    let root = common::repo_root();
    let source = fs::read_to_string(root.join("yaml-grammar.jsonic"))
        .expect("yaml-grammar.jsonic sits at the repository root");
    let typescript = fs::read_to_string(root.join("ts").join("src").join("yaml.ts"))
        .expect("ts/src/yaml.ts is readable");
    let go = fs::read_to_string(root.join("go").join("yaml.go")).expect("go/yaml.go is readable");
    assert_eq!(
        decode_template_literal(&embedded(&typescript, "const grammarText = `", "`")),
        source,
        "ts/src/yaml.ts is out of step with yaml-grammar.jsonic"
    );
    assert_eq!(
        embedded(&go, "const grammarText = `", "`"),
        source,
        "go/yaml.go is out of step with yaml-grammar.jsonic"
    );
}

/// The grammar installs, and the rules it names are the rules that end
/// up on an instance. A grammar text that parsed but named nothing would
/// otherwise leave a parser that accepts only what jsonic accepts.
#[test]
fn the_embedded_grammar_names_the_rules_it_amends() {
    let source = fs::read_to_string(common::repo_root().join("yaml-grammar.jsonic"))
        .expect("yaml-grammar.jsonic sits at the repository root");
    let names = tabnas_yaml::make().rule_names();
    for rule in [
        "val",
        "indent",
        "yamlBlockList",
        "yamlBlockElem",
        "list",
        "map",
        "pair",
        "yamlElemMap",
        "yamlElemPair",
        "elem",
    ] {
        assert!(
            source.contains(&format!("rule: {rule}:")),
            "the grammar file no longer names {rule}"
        );
        assert!(
            names.contains(&rule.to_string()),
            "no {rule} rule installed"
        );
    }
}
