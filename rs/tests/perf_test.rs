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
    (0..count)
        .map(|index| format!("key_{index}: value {index}\n"))
        .collect()
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
    for (name, generate) in shapes {
        let mut small_per_byte = 0.0f64;
        for (index, count) in [250usize, 2000].into_iter().enumerate() {
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
                    growth < 4.0,
                    "{name}: per-byte parse cost grew {growth:.1}x between 250 and \
                     2000 entries, which is the shape of a super-linear parse"
                );
            }
        }
    }
}
