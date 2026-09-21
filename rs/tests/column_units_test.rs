// Error COLUMNS after a non-ASCII character.
//
// This plugin brings its own matchers, and a plugin that does owns the
// arithmetic the engine's matchers do for it: a source offset advances in
// BYTES and a column in CHARACTERS. The Go port once advanced forty column
// sites by a byte quantity, so a two-byte `é` charged two columns and an
// astral character four, and every diagnostic after a non-ASCII character
// reported a column past where the problem was.
//
// `go/column_units_test.go` and `ts/test/column-units.test.ts` assert the
// same sixteen cases. Fourteen of them agree here exactly. The flow-newline
// rows agree too, after the constant the Go port used was repaired. The
// astral row is the recorded engine divergence: TypeScript counts UTF-16
// code units, so an astral character is two columns there and one here.
// See parser/DIVERGENCE.md, "Column positions for astral characters", and
// ../../DIVERGENCE.md.

mod common;

/// The column a refusal reports.
fn column(src: &str) -> usize {
    tabnas_yaml::make()
        .parse(src)
        .map(|value| panic!("{src:?} parsed to {value}, expected a diagnostic"))
        .unwrap_err()
        .col
}

#[test]
fn error_columns_count_characters_not_bytes() {
    for (label, src, want, typescript) in [
        // Controls: ASCII only, where bytes and characters coincide.
        // Without them, "columns count characters" is also satisfied by
        // never counting.
        ("inline-ascii", "{a: xx, ]", 10, 10),
        ("block-key-ascii", "a: 1\n]", 1, 1),
        // The plain-scalar path.
        ("inline-latin1", "{a: é, ]", 9, 9),
        ("inline-bmp", "{a: €, ]", 9, 9),
        // A non-ASCII KEY, which reaches a different site again.
        ("key-latin1", "{é: 1, ]", 9, 9),
        ("key-bmp", "{€€: 1, ]", 10, 10),
        // Quoted scalars: their own two handlers, each with its own
        // column arithmetic.
        ("dq-latin1", r#"{a: "é", ]"#, 11, 11),
        ("dq-bmp", r#"{a: "€€", ]"#, 12, 12),
        ("sq-latin1", "{a: 'é', ]", 11, 11),
        // Anchors and tags: two more handlers.
        ("anchor-latin1", "{a: &x é, ]", 12, 12),
        ("tag-latin1", "{a: !!str é, ]", 15, 15),
        // The flow-collection newline skip, where the column is the
        // number of characters since the last newline. A newline followed
        // by three spaces puts the next token at column 4.
        ("flow-nl-1sp", "[a,\n }", 2, 2),
        ("flow-nl-3sp", "[a,\n   }", 4, 4),
        ("flow-nl-latin1", "[é,\n }", 2, 2),
        // The recorded divergence, and the ONLY row where this port and
        // the canonical one differ.
        ("inline-astral", "{a: \u{1F600}, ]", 9, 10),
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
