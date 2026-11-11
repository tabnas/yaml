// Copyright (c) 2026 Richard Rodger and other contributors, MIT License

//! A parse from a reader (`Tabnas::parse_reader`, admin ADR-30).
//!
//! `make()` builds the parser whole, as `tabnas_jsonic::make()` with the
//! plugin installed builds it, and declares `lex.window` on it, so that a
//! reader is read a window at a time. The plugin's matcher and text check
//! read the source through the window, ask the engine to cut again over a
//! wider one where a scan reaches its end, and keep the lines before the
//! cursor that their backward scans read (`src/source.rs`). These tests
//! hold it to that: `make()` declares the window and parses as that
//! composition does; every row of the shared fixtures, every case of the
//! YAML Test Suite and every input of the YAML Test Schema parses the same
//! from a reader, in pieces of 1, 3 and 64 bytes, under windows of a few
//! characters and the engine's own, as from its text; and tokens longer
//! than the window, and a long document, parse the same too.

mod common;

use std::fs;
use std::path::Path;

/// A reader that hands its bytes over at most `size` at a time.
struct Pieces<'a> {
    bytes: &'a [u8],
    at: usize,
    size: usize,
}

impl<'a> Pieces<'a> {
    fn new(bytes: &'a [u8], size: usize) -> Self {
        Pieces { bytes, at: 0, size }
    }
}

impl std::io::Read for Pieces<'_> {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        let n = self.size.min(buf.len()).min(self.bytes.len() - self.at);
        buf[..n].copy_from_slice(&self.bytes[self.at..self.at + n]);
        self.at += n;
        Ok(n)
    }
}

/// What a parse shows: its value, every map's keys in order and every
/// number's sign among it, or its error's code, position, row, column and
/// token text. An error's excerpt is left out: a parse from a reader shows
/// a long line only as far back as the window reaches (tabnas/parser
/// `DIVERGENCE.md`), the one difference allowed.
fn outcome(parsed: Result<tabnas::Value, tabnas::TabnasError>) -> String {
    match parsed {
        Ok(value) => format!("ok {value:?}"),
        Err(e) => format!(
            "err {} at {} {}:{} {:?}",
            e.code, e.pos, e.row, e.col, e.src
        ),
    }
}

/// The pieces a reader hands its bytes over in.
const PIECES: [usize; 3] = [1, 3, 64];

/// The windows (margin and block, in characters) a reader is read under
/// besides the engine's own: a few characters, so that every matcher meets
/// the window's edge.
const WINDOWS: [(usize, usize); 3] = [(1, 1), (3, 2), (64, 64)];

/// A failure's text, cut to a length a report can carry.
fn short(text: &str) -> String {
    if text.chars().count() > 300 {
        format!("{}...", text.chars().take(300).collect::<String>())
    } else {
        text.to_string()
    }
}

/// `input` parsed by `parser` whole, and from a reader in each of `sizes`,
/// under the engine's window and, when `windows` says so, under each of
/// [`WINDOWS`]: how many reader parses were compared, and where they
/// differ.
fn compare(
    parser: &tabnas::Tabnas,
    at: &str,
    input: &str,
    sizes: &[usize],
    windows: bool,
) -> (usize, Vec<String>) {
    let whole = outcome(parser.parse(input));
    let mut compared = 0;
    let mut failures = Vec::new();
    for &size in sizes {
        let mut check = |how: String, read: String| {
            compared += 1;
            if read != whole {
                failures.push(format!(
                    "{at}: {:?} from a reader in pieces of {size}{how}\n  whole:  {}\n  reader: {}",
                    short(input),
                    short(&whole),
                    short(&read)
                ));
            }
        };
        check(
            String::new(),
            outcome(parser.parse_reader(Pieces::new(input.as_bytes(), size))),
        );
        if windows {
            for (margin, block) in WINDOWS {
                let window = tabnas::WindowSizes {
                    margin,
                    block,
                    chunk: size,
                };
                check(
                    format!(", margin {margin}, block {block}"),
                    outcome(
                        parser.parse_reader_with_sizes(Pieces::new(input.as_bytes(), size), window),
                    ),
                );
            }
        }
    }
    (compared, failures)
}

/// `input` from a reader in each of [`PIECES`], under the engine's window
/// and each of [`WINDOWS`], against its text.
fn same_from_reader(parser: &tabnas::Tabnas, at: &str, input: &str) -> (usize, Vec<String>) {
    compare(parser, at, input, &PIECES, true)
}

/// Every row of every fixture file: where it is, and its input.
fn fixture_rows() -> Vec<(String, String)> {
    let mut files: Vec<String> = fs::read_dir(common::spec_dir())
        .expect("test/spec reads")
        .filter_map(Result::ok)
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .filter(|name| name.ends_with(".tsv"))
        .collect();
    files.sort();
    let mut rows = Vec::new();
    for file in files {
        let spec = tabnas_support::load_spec(
            common::spec_dir().join(&file),
            &tabnas_support::SpecOptions::default(),
        )
        .unwrap_or_else(|error| panic!("{error}"));
        for row in &spec.rows {
            rows.push((row.location(), row.unesc_named("input")));
        }
    }
    rows
}

/// The parser `make()` stands for: jsonic's base with the plugin
/// installed, which dmog composed before `make()` declared the window.
/// jsonic's base declares the window itself (tabnas/jsonic#121), so it is
/// turned off here.
fn composed(options: tabnas_yaml::YamlOptions) -> tabnas::Tabnas {
    let mut bag = indexmap::IndexMap::new();
    bag.insert("meta".to_string(), tabnas::Value::Bool(options.meta));
    let mut parser = tabnas_jsonic::make_with(|options| options.lex.window = false);
    parser
        .use_plugin(tabnas_yaml::plugin(), Some(tabnas::Value::object(bag)))
        .expect("the plugin installs");
    parser
}

#[test]
fn make_declares_the_window() {
    assert!(tabnas_yaml::make().options.lex.window);
    assert!(
        tabnas_yaml::make_with(tabnas_yaml::YamlOptions { meta: true })
            .options
            .lex
            .window
    );
    // The plugin alone declares nothing: a host may install it beneath
    // matchers of its own.
    assert!(
        !composed(tabnas_yaml::YamlOptions::default())
            .options
            .lex
            .window
    );
}

#[test]
fn make_parses_as_the_composition_does() {
    let made = tabnas_yaml::make();
    let composed_parser = composed(tabnas_yaml::YamlOptions::default());
    let mut compared = 0;
    let mut failures = Vec::new();
    for (at, input) in fixture_rows() {
        let a = outcome(made.parse(&input));
        let b = outcome(composed_parser.parse(&input));
        compared += 1;
        if a != b {
            failures.push(format!("{at}\n  make:     {a}\n  composed: {b}"));
        }
    }
    // With options, as the plugin takes them.
    let meta = "a: 1\n---\nb: 2\n";
    let options = tabnas_yaml::YamlOptions { meta: true };
    let made_meta = outcome(tabnas_yaml::make_with(options).parse(meta));
    assert_eq!(made_meta, outcome(composed(options).parse(meta)));
    assert!(made_meta.contains("\"meta\""), "{made_meta}");
    assert!(compared > 600, "{compared}");
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

#[test]
fn every_row_parses_the_same_from_a_reader() {
    let rows = fixture_rows();
    let parser = tabnas_yaml::make();
    let mut compared = 0;
    let mut failures = Vec::new();
    for (index, (at, input)) in rows.iter().enumerate() {
        let (n, found) = same_from_reader(&parser, at, input);
        compared += n;
        failures.extend(found);
        if (index + 1) % 100 == 0 || index + 1 == rows.len() {
            println!(
                "window fixtures: {} of {} rows ({}%), {compared} reader parses",
                index + 1,
                rows.len(),
                (index + 1) * 100 / rows.len()
            );
        }
    }
    assert!(compared > 7000, "{compared}");
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

/// Every case of the YAML Test Suite: its id and its input, sub-cases
/// (`AB12/00`) included, as `yaml_test_suite_test.rs` gathers them.
fn suite_cases() -> Vec<(String, String)> {
    let dir = common::repo_root().join("test").join("yaml-test-suite");
    let entries = |path: &Path| -> Vec<String> {
        let mut names: Vec<String> = fs::read_dir(path)
            .unwrap_or_else(|error| panic!("cannot read {}: {error}", path.display()))
            .filter_map(Result::ok)
            .filter(|entry| entry.path().is_dir())
            .map(|entry| entry.file_name().to_string_lossy().into_owned())
            .collect();
        names.sort();
        names
    };
    let mut cases = Vec::new();
    for name in entries(&dir) {
        let path = dir.join(&name);
        if let Ok(text) = fs::read_to_string(path.join("in.yaml")) {
            cases.push((name, text));
            continue;
        }
        for sub in entries(&path)
            .into_iter()
            .filter(|sub| sub.chars().all(|character| character.is_ascii_digit()))
        {
            if let Ok(text) = fs::read_to_string(path.join(&sub).join("in.yaml")) {
                cases.push((format!("{name}/{sub}"), text));
            }
        }
    }
    cases
}

#[test]
fn the_yaml_test_suite_parses_the_same_from_a_reader() {
    let cases = suite_cases();
    let parser = tabnas_yaml::make();
    let mut compared = 0;
    let mut failures = Vec::new();
    for (index, (id, input)) in cases.iter().enumerate() {
        let (n, found) = same_from_reader(&parser, id, input);
        compared += n;
        failures.extend(found);
        if (index + 1) % 100 == 0 || index + 1 == cases.len() {
            println!(
                "window suite: {} of {} cases ({}%), {compared} reader parses",
                index + 1,
                cases.len(),
                (index + 1) * 100 / cases.len()
            );
        }
    }
    assert!(cases.len() > 400, "{}", cases.len());
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

#[test]
fn the_yaml_test_schema_parses_the_same_from_a_reader() {
    let path = common::repo_root()
        .join("test")
        .join("yaml-test-schema")
        .join("schema-core.json");
    let text = fs::read_to_string(&path).expect("schema-core.json reads");
    let corpus: serde_json::Map<String, serde_json::Value> =
        serde_json::from_str(&text).expect("schema-core.json is a map");
    let parser = tabnas_yaml::make();
    let mut compared = 0;
    let mut failures = Vec::new();
    for input in corpus.keys() {
        let (n, found) = same_from_reader(&parser, input, input);
        compared += n;
        failures.extend(found);
    }
    println!(
        "window schema: {} inputs, {compared} reader parses (100%)",
        corpus.len()
    );
    assert!(corpus.len() > 200, "{}", corpus.len());
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

/// The engine's window reaches 64 KiB past the cursor before each token: a
/// token longer than that meets its edge under the engine's own sizes.
const LONG: usize = (64 << 10) + 2048;

/// Tokens longer than the window, of each kind the plugin cuts itself, and
/// lines the scans read back over from past a block of them.
fn long_tokens() -> Vec<String> {
    let long = LONG;
    let lines = |n: usize, line: &str| line.repeat(n);
    vec![
        // Plain scalars: one line, folded over lines, in a flow sequence.
        format!("a: {}\nb: 1\n", "p".repeat(long)),
        format!("a: x\n{}b: 2\n", lines(long / 8, "  more t\n")),
        format!("- {}end\n- next\n", "w ".repeat(long / 2)),
        format!("[{}last]\n", "item, ".repeat(long / 6)),
        // Quoted scalars, with escapes and folded lines.
        format!(
            "a: \"{}\\u00e9\\x41\\U0001F600\\n\"\nb: 1\n",
            "d".repeat(long)
        ),
        format!("a: \"{}\"\n", "é".repeat(long / 2)),
        format!("a: '{}'\nb: 1\n", "s''".repeat(long / 3)),
        format!("a: \"line\n{}  end\"\n", lines(long / 6, "  word\n")),
        // Block scalars, literal and folded, kept and stripped, explicit.
        format!("a: |\n{}b: 1\n", lines(long / 8, "  literal\n")),
        format!("a: >-\n{}\n  para\nb: 1\n", lines(long / 8, "  folded\n")),
        format!("a: |+\n{}\n\n", lines(long / 8, "  kept\n")),
        format!("- a: |2\n{}- b\n", lines(long / 8, "     deep\n")),
        // Comments, blank lines and anchors between lines.
        format!("# {}\na: 1\n", "c".repeat(long)),
        format!("a: 1\n{}b: 2\n", lines(long / 4, "# c\n")),
        format!("a: 1\n{}b: 2\n", "\n".repeat(long)),
        format!("a: 1\n{}b: 2\n", lines(long / 4, " \t \n")),
        // Anchors, aliases and tags with long names and values.
        format!("&{} a: 1\nb: *{}\n", "n".repeat(long), "n".repeat(long)),
        format!("a: &x {}\nb: *x\n", "v".repeat(long)),
        format!("a: !!str {}\nb: !!int \"12\"\n", "s".repeat(long)),
        format!("a: !{} value\n", "t".repeat(long)),
        // Explicit keys, directives, documents.
        format!("? {}\n: v\n", "k".repeat(long)),
        format!("? |\n{}: v\n", lines(long / 8, "  keyline\n")),
        format!(
            "%YAML 1.2\n%TAG !e! tag:{}:\n---\na: 1\n...\n---\nb: 2\n",
            "e".repeat(long)
        ),
        // Lines whose start the scans read back to from far along them.
        format!("k{}y: |\n  text\n", "e".repeat(long)),
        format!("- - {}a: |1\n   x\n", "- ".repeat(long / 2)),
        format!("[{}y]: z\n", "x, ".repeat(long / 3)),
        format!("{{{}\"q\":r}}\n", "\"k\": v, ".repeat(long / 8)),
        // A flow colon after a quote far behind it.
        format!("{{\"a\"{}:b}}\n", "\n".repeat(long)),
        format!("{{\"a\" # c\n{}:b}}\n", lines(long / 8, "  # more\n")),
        // Numbers longer than the window, which the engine's number
        // matcher reads and the text check would read on.
        format!("a: 1{}\nb: 2\n", "_1".repeat(long / 2)),
        format!("a: .{}x\n", "5".repeat(long)),
        // Cut short by the source's end.
        format!("a: \"{}", "u".repeat(long)),
        format!("a: '{}", lines(long / 2, "u\n")),
        format!("? {}", "k".repeat(long)),
        format!("a: *{}", "m".repeat(long)),
    ]
}

#[test]
fn tokens_longer_than_the_window_parse_the_same_from_a_reader() {
    let parser = tabnas_yaml::make();
    let docs = long_tokens();
    let mut failures = Vec::new();
    for (n, doc) in docs.iter().enumerate() {
        let (_, found) = compare(
            &parser,
            &format!("long token {n}"),
            doc,
            &[4096, (64 << 10) + 3],
            false,
        );
        failures.extend(found);
        println!(
            "window long tokens: {} of {} ({}%)",
            n + 1,
            docs.len(),
            (n + 1) * 100 / docs.len()
        );
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

/// A document of records, some 320 KB, every kind of token the plugin
/// reads among them, so that the window's edge and the text it drops
/// behind the cursor fall across each, a block (64 KiB) at a time.
fn long_document(tail: &str) -> String {
    let mut text =
        String::from("# records\ntitle: records\nbase: &base\n  kind: record\n  tags: [a, b]\n");
    for i in 0..1000 {
        text.push_str(&format!(
            "\nrecord{i}:\n  <<: *base\n  id: {i}\n  name: \"ñ{i}\\u00e9\" # note {i}\n  \
             path: 'C:\\dir\\{i}'\n  when: 2001-12-14t21:59:43.{i}-05:00\n  \
             list:\n    - one\n    - &i{i} two {i}\n    - {{ k: {i}, v: [1, 2] }}\n  \
             note: |\n    line {i}\n    more\n  folded: >-\n    a\n    b {i}\n  \
             ? explicit {i}\n  : value\n  again: *i{i}\n  plain: word word\n    word\n"
        ));
    }
    text.push_str(tail);
    text
}

#[test]
fn a_long_document_parses_the_same_from_a_reader() {
    let parser = tabnas_yaml::make();
    for (n, tail) in ["\nend: true\n", "\nend: *missing\n"].iter().enumerate() {
        let doc = long_document(tail);
        assert!(doc.len() > 256 << 10, "{}", doc.len());
        let whole = outcome(parser.parse(&doc));
        assert_eq!(whole.starts_with("ok "), n == 0, "{}", short(&whole));
        let (_, mut failures) =
            compare(&parser, &format!("long document {n}"), &doc, &[4096], false);
        // Under a window of 256 characters, which moves on thousands of
        // times.
        let window = tabnas::WindowSizes {
            margin: 256,
            block: 256,
            chunk: 4096,
        };
        let read =
            outcome(parser.parse_reader_with_sizes(Pieces::new(doc.as_bytes(), 4096), window));
        if read != whole {
            failures.push(format!(
                "long document {n} under a window of 256\n  whole:  {}\n  reader: {}",
                short(&whole),
                short(&read)
            ));
        }
        assert!(failures.is_empty(), "{}", failures.join("\n"));
        println!("window long document: {} of 2 ({}%)", n + 1, (n + 1) * 50);
    }
}

/// A reader that hands its bytes over at most `size` at a time and counts
/// them.
struct Counted<'a> {
    pieces: Pieces<'a>,
    read: &'a std::cell::Cell<usize>,
}

impl std::io::Read for Counted<'_> {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        let n = self.pieces.read(buf)?;
        self.read.set(self.read.get() + n);
        Ok(n)
    }
}

#[test]
fn a_reader_is_read_no_further_than_the_window_needs() {
    // An alias to no anchor near the start of a document of some 4 MB:
    // from a reader the parse fails there having read a window or so past
    // it, not the document, which a scan that read to the source's end, or
    // asked for a wider window at every token, would have read.
    let doc = format!(
        "a: 1\nb: *missing\n{}",
        "c: [1, 'two', \"3\"] # c\n".repeat(160_000)
    );
    assert!(doc.len() > 3 << 20, "{}", doc.len());
    let parser = tabnas_yaml::make();
    let whole = outcome(parser.parse(&doc));
    assert!(whole.contains("yaml_undefined_alias"), "{whole}");
    let read = std::cell::Cell::new(0);
    let reader = Counted {
        pieces: Pieces::new(doc.as_bytes(), 4096),
        read: &read,
    };
    assert_eq!(outcome(parser.parse_reader(reader)), whole);
    assert!(read.get() < 512 << 10, "{} bytes read", read.get());
}
