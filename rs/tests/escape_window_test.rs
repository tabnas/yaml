// The fixed-width escape windows, counted the way the canonical counts
// them.
//
// `\x`, `\u` and `\U` each read a FIXED WIDTH window after the escape
// letter, and the canonical reads it with
// `fwd.substring(i + 1, i + 1 + width)` before stepping `i` on by
// `width + 1`. Both numbers are UTF-16 CODE UNITS, because that is what a
// JavaScript string is indexed in. A Rust string is indexed in bytes and
// iterates in scalars, and an astral character is ONE scalar and TWO of
// those units, so a window counted in characters reads too much text and
// leaves the cursor too far along the moment one appears inside it.
//
// The cursor matters as much as the value: a window that overruns eats
// the closing quote and the scan runs on into the rest of the document.
// Half of the rows below are shaped to show that rather than the value.
//
// Every row was measured in all three runtimes on 2026-09-21: the
// TypeScript column by running `ts/src/yaml.ts` under Node 22 against
// `@tabnas/parser` and `@tabnas/jsonic` from `/home/user/csv/ts`, the Go
// column by `tabnasyaml.Parse` in `go/`, the Rust column by
// `tabnas_yaml::parse`. The rows whose answer every runtime can hold are
// ALSO shared fixture rows, in `test/spec/quoted-strings.tsv`; they were
// not, once, because Go did not decode `\x`, `\u` or `\U` at all when
// the window held anything but hexadecimal digits, and the Go answer each
// comment quotes is the one it gave then. The rows whose TypeScript
// answer is a lone surrogate stay here and in `../DIVERGENCE.md`.

mod common;

use common::expect;
use serde_json::json as j;

/// An astral character INSIDE the window fills two of the window's
/// units, so the window ends earlier than a character count would end
/// it, and the cursor lands earlier too.
///
/// Measured. `a: "\x<U+1F600>A"`: TypeScript `"\u0000A"`, Go
/// `"x\u{1f600}A"`, Rust `"\u0000A"`. The emoji fills both units the
/// `\x` window asks for, `parseInt` reads no digit out of it and
/// `fromCharCode(NaN)` is NUL, and the `A` after it is left as text.
#[test]
fn an_astral_character_fills_two_units_of_the_window() {
    // `\x`: a two-unit window the emoji fills on its own.
    expect("a: \"\\x\u{1f600}A\"", j!({"a": "\u{0}A"}));
    // `\u`: a four-unit window, so the emoji plus two more characters.
    expect("a: \"\\u\u{1f600}AB\"", j!({"a": "\u{0}"}));
    // `\U`: an eight-unit window whose digits start after the emoji, so
    // `parseInt` stops at `0041` and the trailing `X` survives.
    expect("a: \"\\U0041\u{1f600}00X\"", j!({"a": "AX"}));

    // The controls, where every character in the window is one unit and
    // a character count and a unit count agree.
    expect("a: \"\\x41A\"", j!({"a": "AA"}));
    expect("a: \"\\u0041\u{1f600}X\"", j!({"a": "A\u{1f600}X"}));
    expect("a: \"\\U00000041X\"", j!({"a": "AX"}));
}

/// The same miscount moved the CURSOR, and a cursor past the closing
/// quote turns the rest of the document into more of the scalar. These
/// rows fail on the cursor even where the value alone would pass.
///
/// Measured. `{a: "\x<U+1F600>", b: 2}`: TypeScript `{"a":"\u0000","b":2}`,
/// Go `{"a":"x\u{1f600}","b":2}`, Rust as TypeScript. A window counted in
/// characters took the closing quote as well and yielded
/// `{"a":"\u0000, b: 2}"}`.
#[test]
fn the_cursor_after_the_window_is_counted_in_units_too() {
    expect("{a: \"\\x\u{1f600}\", b: 2}", j!({"a": "\u{0}", "b": 2}));
    expect("{a: \"\\u\u{1f600}AB\", b: 2}", j!({"a": "\u{0}", "b": 2}));
    expect("{a: \"\\U0041\u{1f600}00\", b: 2}", j!({"a": "A", "b": 2}));

    // The controls: the same shapes with no astral character in the
    // window, where the cursor was never wrong.
    expect("{a: \"\\x41\", b: 2}", j!({"a": "A", "b": 2}));
    expect("{a: \"\\u0041\", b: 2}", j!({"a": "A", "b": 2}));
    expect("{a: \"\\U00000041\", b: 2}", j!({"a": "A", "b": 2}));
}

/// A window whose LAST unit falls inside an astral character cuts it in
/// half. The canonical keeps the high surrogate inside the window, where
/// it is no hexadecimal digit and so ends `parseInt`'s prefix, and the
/// cursor then lands on the LOW surrogate, which the scan appends as a
/// character of its own. A Rust string has no place for an unpaired
/// surrogate, so this port writes the replacement character there, which
/// is the standing trade recorded in `../DIVERGENCE.md` under "UTF-16
/// escapes in a double quoted scalar".
///
/// This is a REGISTER test, not a parity one: it asserts the divergence
/// is still exactly one replacement character wide, so it fails on
/// repair as loudly as on regression.
///
/// Measured. `a: "\xA<U+1F600>Z"`: TypeScript U+000A, a lone U+DE00 and
/// `Z`; Go `"xA\u{1f600}Z"`; Rust U+000A, U+FFFD and `Z`.
#[test]
fn a_window_cut_through_an_astral_character_folds_the_half_it_leaves() {
    expect("a: \"\\xA\u{1f600}Z\"", j!({"a": "\n\u{fffd}Z"}));
    expect("a: \"\\u004\u{1f600}Z\"", j!({"a": "\u{4}\u{fffd}Z"}));
    expect("a: \"\\U0000004\u{1f600}Z\"", j!({"a": "\u{4}\u{fffd}Z"}));

    // The COUNT is the point: exactly one character stands in for the
    // half unit, and the text after it is untouched. A port that simply
    // dropped the cut character would lose a character here.
    expect("a: \"\\xA\u{1f600}\"", j!({"a": "\n\u{fffd}"}));
    expect(
        "{a: \"\\xA\u{1f600}\", b: 2}",
        j!({"a": "\n\u{fffd}", "b": 2}),
    );

    // The control that is NOT a trade: where the escape itself names a
    // high surrogate, the low half the cut leaves completes the pair,
    // and both runtimes reach the whole astral character.
    expect("a: \"\\U000D83D\u{1f600}Z\"", j!({"a": "\u{1f600}Z"}));

    // The control with no cut: a character inside the Basic Multilingual
    // Plane is one unit and one scalar, so the window's second unit is
    // the whole of it. It is consumed entirely, `parseInt` still reads
    // `A`, and no replacement character is left behind.
    expect("a: \"\\xA\u{4e2d}Z\"", j!({"a": "\nZ"}));
}

/// A backslash as the very last character of the source escapes
/// nothing, and the value ends where the source does. The canonical
/// port once read `fwd[i]` past the end and appended the nine letters of
/// `undefined`, text the input never held; that was a TypeScript defect,
/// and under ADR-13 it was repaired there rather than copied here. The
/// rows are shared fixtures in `test/spec/quoted-strings.tsv` as well.
///
/// Measured before the repair. `a: "\`: TypeScript `{"a":"undefined"}`,
/// Go `{"a":""}`, Rust `{"a":"undefined"}`. Go was right.
#[test]
fn a_backslash_at_the_end_of_the_source_appends_nothing() {
    expect("a: \"\\", j!({"a": ""}));
    expect("a: \"x\\", j!({"a": "x"}));

    // The controls: a backslash with anything at all after it takes the
    // ordinary escape path instead.
    expect("a: \"x\\\"", j!({"a": "x\""}));
    expect("a: \"x\\n\"", j!({"a": "x\n"}));
}
