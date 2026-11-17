// Three properties of the reader, each held over every text this
// repository has: every row of the shared fixtures, every case of the YAML
// Test Suite and every input of the YAML Test Schema. The Rust twin of
// `ts/test/reading.test.ts` and `go/reading_test.go`.
//
// 1. A line break is `\n`, `\r\n`, or `\r` by itself (YAML 1.2.2, 5.4),
//    and which one a text uses changes nothing it reads as. Each text is
//    read as written, with every `\n` made `\r\n`, and with every `\n`
//    made `\r`, and the three must agree: in the value, or in the error's
//    code.
// 2. An error stands where its offset says: its row is one more than the
//    line breaks before it, and its column is how far it is past its
//    line's start, from 1, in CHARACTERS (TypeScript counts UTF-16 units,
//    which ../../DIVERGENCE.md records). This port does not expose its
//    tokens, so the property is held through the diagnostic of every text
//    that is refused.
// 3. `schema: core` changes how a plain scalar resolves and nothing else:
//    under it a text reads as it does without, except that a scalar the
//    plugin's own reading gives a boolean or a number may be the string
//    the core schema gives it.

mod common;

use std::collections::HashMap;
use std::fs;
use std::path::Path;

use tabnas::Value;
use tabnas_yaml::{Schema, YamlOptions};

/// One text: where it is, the text, and the options its row names.
struct Text {
    at: String,
    text: String,
    options: YamlOptions,
}

/// The options a fixture row's `opts` cell names.
fn row_options(raw: &str, at: &str) -> YamlOptions {
    if raw.trim().is_empty() {
        return YamlOptions::default();
    }
    let parsed: serde_json::Value =
        serde_json::from_str(raw).unwrap_or_else(|error| panic!("{at}: opts {raw:?}: {error}"));
    YamlOptions {
        meta: parsed
            .get("meta")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false),
        schema: match parsed.get("schema") {
            None => None,
            Some(serde_json::Value::String(name)) if name == "core" => Some(Schema::Core),
            Some(other) => panic!("{at}: no schema {other}"),
        },
    }
}

/// Every text: the fixture rows with their options, then the suite's
/// cases, sub-cases (`AB12/00`) included, then the schema's inputs.
fn texts() -> Vec<Text> {
    let mut out = Vec::new();
    let mut files: Vec<String> = fs::read_dir(common::spec_dir())
        .expect("test/spec reads")
        .filter_map(Result::ok)
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .filter(|name| name.ends_with(".tsv"))
        .collect();
    files.sort();
    for file in files {
        let spec = tabnas_support::load_spec(
            common::spec_dir().join(&file),
            &tabnas_support::SpecOptions::default(),
        )
        .unwrap_or_else(|error| panic!("{error}"));
        for row in &spec.rows {
            let at = row.location();
            let options = row_options(row.named("opts"), &at);
            out.push(Text {
                at,
                text: row.unesc_named("input"),
                options,
            });
        }
    }

    let suite = common::repo_root().join("test").join("yaml-test-suite");
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
    for name in entries(&suite) {
        let path = suite.join(&name);
        if let Ok(text) = fs::read_to_string(path.join("in.yaml")) {
            out.push(Text {
                at: format!("suite {name}"),
                text,
                options: YamlOptions::default(),
            });
            continue;
        }
        for sub in entries(&path)
            .into_iter()
            .filter(|sub| sub.chars().all(|character| character.is_ascii_digit()))
        {
            if let Ok(text) = fs::read_to_string(path.join(&sub).join("in.yaml")) {
                out.push(Text {
                    at: format!("suite {name}/{sub}"),
                    text,
                    options: YamlOptions::default(),
                });
            }
        }
    }

    let schema = common::repo_root()
        .join("test")
        .join("yaml-test-schema")
        .join("schema-core.json");
    let corpus: serde_json::Map<String, serde_json::Value> =
        serde_json::from_str(&fs::read_to_string(&schema).expect("schema-core.json reads"))
            .expect("schema-core.json is a map");
    for input in corpus.keys() {
        out.push(Text {
            at: format!("schema {input:?}"),
            text: input.clone(),
            options: YamlOptions::default(),
        });
    }
    out
}

/// A parser for each set of options, built once: a parser keeps nothing
/// from one parse to the next.
struct Parsers(HashMap<(bool, bool), tabnas::Tabnas>);

impl Parsers {
    fn new() -> Self {
        Parsers(HashMap::new())
    }

    fn with(&mut self, options: YamlOptions) -> &tabnas::Tabnas {
        self.0
            .entry((options.meta, options.schema.is_some()))
            .or_insert_with(|| tabnas_yaml::make_with(options))
    }
}

/// What a parse shows: its value, every map's keys in order and every
/// number as it is, or its error's code.
fn seen(parsed: Result<Value, tabnas::TabnasError>) -> String {
    match parsed {
        Ok(value) => format!("value {value:?}"),
        Err(error) => format!("error {}", error.code),
    }
}

/// A failure's text, cut to a length a report can carry.
fn short(text: &str) -> String {
    if text.chars().count() > 200 {
        format!("{}...", text.chars().take(200).collect::<String>())
    } else {
        text.to_string()
    }
}

/// A progress line every so many texts, so that a slow run is not silent.
fn progress(what: &str, done: usize, total: usize) {
    if done % 500 == 0 || done == total {
        println!("{what}: {done} of {total} ({}%)", done * 100 / total.max(1));
    }
}

#[test]
fn census() {
    // The texts are all there: a property held over a truncated set is
    // held over less than it says.
    let all = texts();
    assert!(all.len() > 1400, "{}", all.len());
}

/// Every text with every `\n` made `brk` reads as it does as written. A
/// text with a carriage return of its own is about one line break or
/// another already, and is left out.
fn line_breaks(name: &str, brk: &str) {
    let all = texts();
    let mut parsers = Parsers::new();
    let mut compared = 0;
    let mut own = 0;
    let mut failures = Vec::new();
    for (index, one) in all.iter().enumerate() {
        progress(&format!("line breaks {name}"), index + 1, all.len());
        if one.text.contains('\r') {
            own += 1;
            continue;
        }
        let parser = parsers.with(one.options);
        let written = seen(parser.parse(&one.text));
        let rewritten = seen(parser.parse(&one.text.replace('\n', brk)));
        compared += 1;
        if written != rewritten {
            failures.push(format!(
                "{}: {:?}\n  as written: {}\n  with {name}: {}",
                one.at,
                short(&one.text),
                short(&written),
                short(&rewritten)
            ));
        }
    }
    assert!(compared > 1300, "{compared} compared, {own} left out");
    assert!(
        failures.is_empty(),
        "{} texts differ:\n{}",
        failures.len(),
        failures[..failures.len().min(10)].join("\n")
    );
}

#[test]
fn line_breaks_crlf() {
    line_breaks("crlf", "\r\n");
}

#[test]
fn line_breaks_cr() {
    line_breaks("cr", "\r");
}

/// The row and column of character offset `pos` in `text`: a line break
/// is `\n`, a `\r` no `\n` follows, or `\r\n`, and a byte order mark at the
/// start takes no column. Between the halves of a `\r\n` the line has
/// ended.
fn place_of(text: &str, pos: usize) -> (usize, usize) {
    let chars: Vec<char> = text.chars().collect();
    let mut row = 1;
    let mut line = usize::from(chars.first() == Some(&'\u{feff}'));
    for index in 0..pos.min(chars.len()) {
        let c = chars[index];
        if c == '\n' || c == '\r' {
            if !(c == '\n' && index > 0 && chars[index - 1] == '\r') {
                row += 1;
            }
            line = index + 1;
        }
    }
    (row, if pos < line { 1 } else { pos - line + 1 })
}

#[test]
fn an_error_stands_where_its_offset_says() {
    let all = texts();
    let mut parsers = Parsers::new();
    let mut errors = 0;
    let mut failures = Vec::new();
    for (index, one) in all.iter().enumerate() {
        progress("error places", index + 1, all.len());
        let Err(error) = parsers.with(one.options).parse(&one.text) else {
            continue;
        };
        errors += 1;
        let want = place_of(&one.text, error.pos);
        if want != (error.row, error.col) {
            failures.push(format!(
                "{}: {} at {} has {}:{}, its offset gives {}:{} in {:?}",
                one.at,
                error.code,
                error.pos,
                error.row,
                error.col,
                want.0,
                want.1,
                short(&one.text)
            ));
        }
    }
    assert!(errors > 80, "{errors}");
    assert!(
        failures.is_empty(),
        "{} errors misplaced:\n{}",
        failures.len(),
        failures[..failures.len().min(10)].join("\n")
    );
}

/// A value as text that tells apart everything a parse can give.
fn canon(value: &Value) -> String {
    format!("{value:?}")
}

/// The entries of a mapping, if `value` is one.
fn entries(value: &Value) -> Option<Vec<(&String, &Value)>> {
    match value {
        Value::Object(map) => Some(map.iter().collect()),
        Value::MapRef(map) => Some(map.value.iter().collect()),
        _ => None,
    }
}

/// The items of a sequence, if `value` is one.
fn items(value: &Value) -> Option<&[Value]> {
    match value {
        Value::Array(items) => Some(items),
        Value::ListRef(list) => Some(&list.value),
        _ => None,
    }
}

/// `under` is `plain` except where a boolean or a number of `plain` is a
/// string in `under`: None when it is, and otherwise where they part.
fn parts(plain: &Value, under: &Value, path: &str) -> Option<String> {
    if items(plain).is_some() || items(under).is_some() {
        let (Some(plain), Some(under)) = (items(plain), items(under)) else {
            return Some(format!("{path} (a sequence)"));
        };
        if plain.len() != under.len() {
            return Some(format!("{path} (a sequence)"));
        }
        return plain
            .iter()
            .zip(under)
            .enumerate()
            .find_map(|(index, (p, u))| parts(p, u, &format!("{path}/{index}")));
    }
    if let Some(plain) = entries(plain) {
        let Some(under) = entries(under) else {
            return Some(format!("{path} (a mapping)"));
        };
        let mut plain_keys: Vec<&String> = plain.iter().map(|(key, _)| *key).collect();
        let mut under_keys: Vec<&String> = under.iter().map(|(key, _)| *key).collect();
        plain_keys.sort();
        under_keys.sort();
        if plain_keys != under_keys {
            return Some(format!("{path} (its keys)"));
        }
        let under: HashMap<&String, &Value> = under.into_iter().collect();
        return plain
            .iter()
            .find_map(|(key, p)| parts(p, under[key], &format!("{path}/{key}")));
    }
    if canon(plain) == canon(under) {
        return None;
    }
    if matches!(plain, Value::Bool(_) | Value::Number(_))
        && matches!(under, Value::String(_) | Value::Text(_))
    {
        return None;
    }
    Some(format!("{path} ({} and {})", canon(plain), canon(under)))
}

#[test]
fn the_core_schema_changes_plain_scalars_alone() {
    let all = texts();
    let mut parsers = Parsers::new();
    let mut compared = 0;
    let mut changed = 0;
    let mut failures = Vec::new();
    for (index, one) in all.iter().enumerate() {
        progress("core schema", index + 1, all.len());
        if one.options.schema.is_some() {
            continue;
        }
        let plain = parsers.with(one.options).parse(&one.text);
        let core = YamlOptions {
            schema: Some(Schema::Core),
            ..one.options
        };
        let under = parsers.with(core).parse(&one.text);
        compared += 1;
        match (&plain, &under) {
            (Ok(plain), Ok(under)) => {
                if canon(plain) != canon(under) {
                    changed += 1;
                }
                if let Some(at) = parts(plain, under, "") {
                    failures.push(format!("{}: {:?} parts at {at}", one.at, short(&one.text)));
                }
            }
            _ => {
                let say = |result: &Result<Value, tabnas::TabnasError>| match result {
                    Ok(_) => "a value".to_string(),
                    Err(error) => format!("error {}", error.code),
                };
                if say(&plain) != say(&under) {
                    failures.push(format!(
                        "{}: {:?} gives {}, and under the core schema {}",
                        one.at,
                        short(&one.text),
                        say(&plain),
                        say(&under)
                    ));
                }
            }
        }
    }
    assert!(compared > 1300, "{compared}");
    // The option does something: the fixtures hold the YAML 1.1 words and
    // jsonic's numbers, which it reads as strings.
    assert!(changed > 20, "{changed}");
    assert!(
        failures.is_empty(),
        "{} texts part elsewhere:\n{}",
        failures.len(),
        failures[..failures.len().min(10)].join("\n")
    );
}
