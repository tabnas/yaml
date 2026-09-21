// Where the canonical spells a delimiter out and a TAB is not among
// them.
//
// Most of this plugin's scans stop on "a blank", meaning a space or a
// tab, and `crate::lex::blank` is that pair. The canonical has no such
// helper: it writes each stop out, site by site, and the sites do NOT
// agree with each other. Three of them take a space and a line end and
// no tab, and a port that reaches for the pair at those three stops one
// character early and changes what the document means.
//
// The three, with the canonical expression each stands for:
//
//  * a typed tag's ANCHOR NAME, `ts/src/yaml.ts` line 1418:
//    `fwd[anchorEnd] !== ' ' && !== '\n' && !== '\r'`
//  * a typed tag's unquoted VALUE at a colon, line 1471:
//    `fwd[valEnd+1] === ' ' || '\n' || '\r' || undefined`
//  * the document marker that stops a BLOCK SCALAR, line 576:
//    `fwd[pos+3] === '\n' || '\r' || ' ' || undefined`
//
// The marker test is the one to be careful with, because the canonical
// writes it FOUR times and only that one leaves the tab out. The other
// three, at lines 855, 1735 and 2187, take a tab, and `is_doc_marker`
// is right for them.
//
// Every row was measured in all three runtimes on 2026-09-21: the
// TypeScript column by running `ts/src/yaml.ts` under Node 22, the Go
// column by `tabnasyaml.Parse` in `go/`, the Rust column by
// `tabnas_yaml::parse`. The first two groups agree in all three and are
// ALSO shared fixture rows, in `test/spec/tags.tsv`; they are repeated
// here because a fixture row records the answer and this file records
// which canonical expression the answer comes from. The third group is
// Rust and TypeScript against Go, and `../DIVERGENCE.md` carries it.

mod common;

use common::expect;
use serde_json::json as j;

/// A tab does not end a typed tag's anchor name, so `&n<TAB>foo` names
/// the anchor `n<TAB>foo` and leaves the tagged value empty. Stopping at
/// the tab instead both makes `<TAB>foo` the value and files the anchor
/// under a different name, so a later alias misses it too.
///
/// Measured. `a: !!str &n<TAB>foo`: TypeScript `{"a":""}`, Go the same,
/// Rust the same. Before the repair Rust said `{"a":"\tfoo"}`.
#[test]
fn a_tab_does_not_end_a_typed_tags_anchor_name() {
    expect("a: !!str &n\tfoo", j!({"a": ""}));
    // The tab may sit anywhere in the name, including first.
    expect("a: !!str &\tn foo", j!({"a": "foo"}));
    expect("a: !!str &n\t foo", j!({"a": "foo"}));

    // The controls: a SPACE does end the name, and so does a line end.
    expect("a: !!str &n foo", j!({"a": "foo"}));
    expect("a: !!str &n\nb: 1", j!({"a": {"b": 1}}));

    // The lexer's own `&name` handler is a DIFFERENT scan, and a tab
    // does end a name there. Without this row the two could be merged.
    expect("a: &n\tfoo\nb: *n", j!({"a": "foo", "b": "foo"}));
}

/// A typed tag's unquoted value ends at a colon only when a literal
/// SPACE, a line end or the end of the source follows it. A tab after
/// the colon leaves the colon inside the value, where stopping there
/// would hand the grammar a key separator instead.
///
/// Measured. `a: !!str x:<TAB>y`: TypeScript `{"a":"x:\ty"}`, Go the
/// same, Rust the same. Before the repair Rust said `{"a":{"x":"y"}}`.
#[test]
fn a_tab_after_a_colon_does_not_end_a_typed_tags_value() {
    expect("a: !!str x:\ty", j!({"a": "x:\ty"}));
    expect("a: !!str x:\t y", j!({"a": "x:\t y"}));

    // The controls: a space, a line end and the end of source all end
    // the value at the colon, and there the colon IS a separator.
    expect("a: !!str x: y", j!({"a": {"x": "y"}}));
    expect("a: !!str x:\nb: 1", j!({"a": {"x": null, "b": 1}}));
}

/// The document marker that ends a BLOCK SCALAR takes a space and not a
/// tab, alone among the four places the canonical writes that test. A
/// `---<TAB>` line therefore stays inside the scalar.
///
/// This is a REGISTER test rather than a parity one, because Go stops
/// the scalar there: see `../DIVERGENCE.md`, "A tab after a document
/// marker inside a block scalar".
///
/// Measured. `|<NL>x<NL>---<TAB>y<NL>`: TypeScript `"x\n---\ty\n"`, Go
/// `["x\n","y"]`, Rust `"x\n---\ty\n"`.
#[test]
fn a_tab_after_a_document_marker_stays_inside_a_block_scalar() {
    expect("|\nx\n---\ty\n", j!("x\n---\ty\n"));
    expect("|\n---\ty\n", j!("---\ty\n"));
    expect("|\n...\ty\n", j!("...\ty\n"));

    // The controls: a SPACE after the marker ends the scalar, and so
    // does a bare marker line.
    expect("|\n--- y\n", j!(["", "y"]));
    expect("|\n---\n", j!(["", null]));
    expect("|\n... y\n", j!(["", "y"]));

    // The marker test everywhere ELSE takes a tab, so these must not
    // move with the one above.
    expect("a: 1\n---\tb: 2\n", j!([{"a": 1}, {"b": 2}]));
    expect("---\tb: 2\n", j!({"b": 2}));
}
