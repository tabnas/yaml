// The baked-in VERSION must equal the package's declared version.
//
// This is the CI check for version drift. It exists because the constant
// HAS drifted in practice: jsonic-cli shipped 0.4.1 and 0.4.2 while its
// constant sat at 0.4.0, and @tabnas/json shipped a TypeScript `Version`
// export reading 1.0.0 for several releases because nothing ever rewrote
// it. Both were invisible until someone read the file. A release that
// bumps package.json and forgets a constant now fails here instead of
// shipping a lie.
//
// Deliberately fatal, never skipped: a version check that silently does
// not run is the failure mode this test exists to prevent.

mod common;

use std::fs;

#[test]
fn version_matches_package_json() {
    let path = common::repo_root().join("ts").join("package.json");
    let raw = fs::read_to_string(&path).unwrap_or_else(|error| {
        panic!(
            "cannot read {}, so VERSION cannot be checked: {error}",
            path.display()
        )
    });
    let package: serde_json::Value =
        serde_json::from_str(&raw).expect("ts/package.json is readable JSON");
    let declared = package
        .get("version")
        .and_then(serde_json::Value::as_str)
        .expect("ts/package.json has a version field");
    assert_eq!(
        tabnas_yaml::VERSION,
        declared,
        "VERSION drift: the crate says {} but ts/package.json says {declared}. \
         All three constants are rewritten together at release; if you bumped one \
         by hand, bump the others.",
        tabnas_yaml::VERSION
    );
}

#[test]
fn version_matches_cargo_toml() {
    assert_eq!(
        tabnas_yaml::VERSION,
        env!("CARGO_PKG_VERSION"),
        "VERSION and the Cargo package version have drifted"
    );
}

/// The Go port carries the same constant, and the release orchestrator
/// rewrites it alongside the other two.
#[test]
fn version_matches_the_go_port() {
    let path = common::repo_root().join("go").join("yaml.go");
    let source = fs::read_to_string(&path)
        .unwrap_or_else(|error| panic!("cannot read {}: {error}", path.display()));
    let declared = source
        .lines()
        .find_map(|line| line.strip_prefix("const VERSION = \""))
        .and_then(|rest| rest.split('"').next())
        .expect("go/yaml.go declares const VERSION");
    assert_eq!(tabnas_yaml::VERSION, declared, "VERSION drift against Go");
}
