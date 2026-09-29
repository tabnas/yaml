// Performance shape, not wall-clock speed.
//
// Two things are measured, both of them MACHINE-INDEPENDENT: each
// compares two runs on the same machine in the same process, so a slow
// runner cannot make either flaky. There is deliberately no time budget.
//
//  * `parse` must reuse a cached instance rather than rebuilding the
//    grammar per call. Building this grammar dominates a small parse, so
//    a rebuild-per-call `parse` is an order of magnitude slower.
//  * Parse time must grow about linearly with input size for each input
//    shape, not quadratically.

mod common;

use std::time::{Duration, Instant};

/// One generated input shape, named.
type Shape = (&'static str, fn(usize) -> String);

/// The fastest of `iterations` parses.
fn fastest(parse: impl Fn(), iterations: usize) -> Duration {
    let mut best = Duration::MAX;
    for _ in 0..iterations {
        let start = Instant::now();
        parse();
        best = best.min(start.elapsed());
    }
    best
}

/// `parse` builds its instance once, on first use, and reuses it after
/// that, as the other two runtimes do. A rebuild per call is roughly
/// twenty-five times slower, so a generous ratio still catches it.
#[test]
fn parse_reuses_its_instance() {
    const SRC: &str = "a: 1\nb: 2\nc: 3";
    const RUNS: usize = 300;

    let parser = tabnas_yaml::make();
    // Warm both paths so the comparison is steady-state.
    for _ in 0..50 {
        tabnas_yaml::parse(SRC).expect("parses");
        parser.parse(SRC).expect("parses");
    }

    let start = Instant::now();
    for _ in 0..RUNS {
        tabnas_yaml::parse(SRC).expect("parses");
    }
    let shared = start.elapsed();

    let start = Instant::now();
    for _ in 0..RUNS {
        parser.parse(SRC).expect("parses");
    }
    let reuse = start.elapsed();

    assert!(
        shared <= reuse * 4,
        "parse() appears to rebuild the grammar on every call: {RUNS} calls took \
         {shared:?} against {reuse:?} reusing one instance (ratio {:.1}x, limit 4x). \
         Cache a lazy default instance.",
        shared.as_secs_f64() / reuse.as_secs_f64().max(f64::MIN_POSITIVE)
    );
}

fn block_map(count: usize) -> String {
    let mut src = String::new();
    for index in 0..count {
        src.push_str(&format!("key_{index}: value {index}\n"));
    }
    src
}

fn flow_map(count: usize) -> String {
    let body: Vec<String> = (0..count)
        .map(|index| format!("k{index}: v{index}"))
        .collect();
    format!("doc: {{{}}}\n", body.join(", "))
}

fn literal_block(count: usize) -> String {
    let mut out = String::from("content: |\n");
    for index in 0..count {
        out.push_str(&format!("  line {index}\n"));
    }
    out
}

fn anchor_alias(count: usize) -> String {
    let mut out = String::from("base: &base\n");
    for index in 0..20 {
        out.push_str(&format!("  k{index}: v{index}\n"));
    }
    out.push_str("refs:\n");
    for _ in 0..count {
        out.push_str("  - *base\n");
    }
    out
}

/// A root mapping whose values each start on the next line. These three
/// were quadratic: the value's `val` rule inherited the parent map's node,
/// jsonic's val before-close stashed a clone of it, and every pair insert
/// then copied the whole map through `Arc::make_mut`.
fn block_seq_values(count: usize) -> String {
    (0..count)
        .map(|index| format!("k{index}:\n  - {index}\n"))
        .collect()
}

fn nested_map_values(count: usize) -> String {
    (0..count)
        .map(|index| format!("k{index}:\n  x: {index}\n"))
        .collect()
}

fn empty_values(count: usize) -> String {
    (0..count)
        .map(|index| format!("k{index}:\nj{index}: 1\n"))
        .collect()
}

/// Parse time per byte must not grow with the input. A quadratic parser
/// doubles its per-byte cost every time the input doubles; the bound here
/// is loose enough for a noisy runner and far tighter than that.
#[test]
fn parse_time_grows_about_linearly() {
    let parser = tabnas_yaml::make();
    let shapes: [Shape; 4] = [
        ("blockMap", block_map),
        ("flowMap", flow_map),
        ("literalBlock", literal_block),
        ("anchorAlias", anchor_alias),
    ];
    assert_linear(&parser, &shapes, 2000, 4.0);
}

/// The next-line value shapes, over a wider range and a tighter bound: a
/// debug build's fixed cost per entry dilutes the quadratic term. Measured,
/// the quadratic parse grew 2.3x to 3.2x per byte from 250 to 4000
/// entries and the linear one 0.75x to 0.9x.
#[test]
fn next_line_values_parse_in_linear_time() {
    let parser = tabnas_yaml::make();
    let shapes: [Shape; 3] = [
        ("blockSeqValues", block_seq_values),
        ("nestedMapValues", nested_map_values),
        ("emptyValues", empty_values),
    ];
    assert_linear(&parser, &shapes, 4000, 1.8);
}

fn assert_linear(parser: &tabnas::Tabnas, shapes: &[Shape], large: usize, bound: f64) {
    for &(name, generate) in shapes {
        let mut small_per_byte = 0.0f64;
        for (index, count) in [250usize, large].into_iter().enumerate() {
            let src = generate(count);
            let elapsed = fastest(
                || {
                    parser.parse(&src).expect("the generated source parses");
                },
                3,
            );
            let per_byte = elapsed.as_secs_f64() / src.len() as f64;
            if index == 0 {
                small_per_byte = per_byte;
            } else {
                let growth = per_byte / small_per_byte.max(f64::MIN_POSITIVE);
                assert!(
                    growth < bound,
                    "{name}: per-byte parse cost grew {growth:.1}x between 250 and \
                     {large} entries, which is the shape of a super-linear parse"
                );
            }
        }
    }
}
