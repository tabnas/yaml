// Error COLUMNS after a non-ASCII character.
//
// This plugin brings its own matchers, and a plugin that does owns the
// arithmetic the engine's matchers do for it: a source offset advances in
// BYTES and a column in CHARACTERS. The Go port once advanced forty column
// sites by a byte quantity, so a two-byte `é` charged two columns and an
// astral character four, and every diagnostic after a non-ASCII character
// reported a column past where the problem was.
//
// A token's column is now set from its offset: how far it stands past its
// line's start, in characters, from 1. It was once kept up as each token
// was taken, and drifted: every `: ` on a line put the columns after it
// one too far, and the columns below were each one more than they are
// now.
//
// `go/column_units_test.go` and `ts/test/column-units.test.ts` assert the
// same fifteen cases. Fourteen agree exactly. The astral row is the recorded
// engine divergence: TypeScript counts UTF-16 code units, so an astral
// character is two columns there and one here. See parser/DIVERGENCE.md,
// "Column positions for astral characters", and ../../DIVERGENCE.md.

mod common;

/// The column a refusal reports, which must be the one its offset gives,
/// in characters: so a row below cannot pin a drift.
fn column(src: &str) -> usize {
    let error = tabnas_yaml::make()
        .parse(src)
        .map(|value| panic!("{src:?} parsed to {value}, expected a diagnostic"))
        .unwrap_err();
    let before: Vec<char> = src.chars().take(error.pos).collect();
    let line = before
        .iter()
        .rposition(|c| *c == '\n')
        .map_or(0, |index| index + 1);
    assert_eq!(
        error.col,
        error.pos - line + 1,
        "{src:?}: the column is not its offset's"
    );
    error.col
}

#[test]
fn error_columns_count_characters_not_bytes() {
    for (label, src, want, typescript) in [
        // Controls: ASCII only, where bytes and characters coincide.
        // Without them, "columns count characters" is also satisfied by
        // never counting.
        ("inline-ascii", "{a: xx, ]", 9, 9),
        ("block-key-ascii", "a: 1\n]", 1, 1),
        // The plain-scalar path.
        ("inline-latin1", "{a: é, ]", 8, 8),
        ("inline-bmp", "{a: €, ]", 8, 8),
        // A non-ASCII KEY, which reaches a different site again.
        ("key-latin1", "{é: 1, ]", 8, 8),
        ("key-bmp", "{€€: 1, ]", 9, 9),
        // Quoted scalars: their own two handlers, each with its own
        // column arithmetic.
        ("dq-latin1", r#"{a: "é", ]"#, 10, 10),
        ("dq-bmp", r#"{a: "€€", ]"#, 11, 11),
        ("sq-latin1", "{a: 'é', ]", 10, 10),
        // Anchors and tags: two more handlers.
        ("anchor-latin1", "{a: &x é, ]", 11, 11),
        ("tag-latin1", "{a: !!str é, ]", 14, 14),
        // The flow-collection newline skip, where the column is the
        // number of characters since the last newline. A newline followed
        // by three spaces puts the next token at column 4.
        ("flow-nl-1sp", "[a,\n }", 2, 2),
        ("flow-nl-3sp", "[a,\n   }", 4, 4),
        ("flow-nl-latin1", "[é,\n }", 2, 2),
        // The recorded divergence, and the ONLY row where this port and
        // the canonical one differ.
        ("inline-astral", "{a: \u{1F600}, ]", 8, 9),
    ] {
        let got = column(src);
        assert_eq!(
            got, want,
            "{label}: {src:?} col = {got}, want {want} (TypeScript says {typescript}). \
             A column ahead of the want by the character's extra BYTES means a column \
             site is counting bytes again."
        );
    }
}

/// The row a refusal reports, so the column rows above cannot pass while
/// pointing at the wrong line.
#[test]
fn error_rows_count_lines() {
    for (src, want) in [("{a: xx, ]", 1), ("a: 1\n]", 2), ("[a,\n }", 2)] {
        let row = tabnas_yaml::make()
            .parse(src)
            .map(|value| panic!("{src:?} parsed to {value}"))
            .unwrap_err()
            .row;
        assert_eq!(row, want, "{src:?} row");
    }
}
