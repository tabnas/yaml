// The install section of `README.md` names every sibling checkout the
// crate's dependency closure needs.
//
// None of the tabnas crates is published, so a reader of that page
// clones checkouts by hand and cargo resolves the path dependencies from
// them. The page has twice advertised a manifest that did not resolve:
// once the `tabnas-jsonic` entry was missing, once the transitive `json`
// checkout that `tabnas-jsonic` takes by `../../json/rs`. Both were read
// and not run, because the `toml` fence is not a doctest. This test runs
// the check the reviews did by hand: it walks the path dependencies from
// `Cargo.toml` through every sibling manifest and asserts the page names
// each repository the walk reaches, and each crate a consumer has to
// list, so a new transitive dependency fails here rather than on a
// reader's machine.
//
// Proved 2026-09-22 as well by building a scratch crate from the fence's
// three entries, with `parser`, `jsonic` and `json` the only checkouts
// the resolution reaches, and running every example on the page.

mod common;

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

use regex::Regex;

/// `name = { path = "..." }` entries under one `[section]` of a manifest.
fn path_deps(manifest: &Path, section: &str) -> Vec<(String, PathBuf)> {
    let text = fs::read_to_string(manifest)
        .unwrap_or_else(|error| panic!("{}: {error}", manifest.display()));
    let entry = Regex::new(r#"^([A-Za-z0-9_-]+)\s*=\s*\{[^}]*\bpath\s*=\s*"([^"]+)""#).unwrap();
    let mut in_section = false;
    let mut found = Vec::new();
    for line in text.lines() {
        let line = line.trim();
        if line.starts_with('[') {
            in_section = line == format!("[{section}]");
            continue;
        }
        if !in_section {
            continue;
        }
        if let Some(captures) = entry.captures(line) {
            let dir = manifest.parent().expect("a manifest has a directory");
            found.push((captures[1].to_string(), dir.join(&captures[2])));
        }
    }
    found
}

/// The repository a sibling path names: `../../parser/rs` is `parser`.
fn repository_of(path: &Path) -> String {
    let crate_dir = path.canonicalize().unwrap_or_else(|error| {
        panic!(
            "{}: {error}: is the sibling checkout present?",
            path.display()
        )
    });
    crate_dir
        .parent()
        .and_then(Path::file_name)
        .map(|name| name.to_string_lossy().into_owned())
        .expect("a sibling crate lives at <repository>/rs")
}

/// Every crate and repository the runtime closure reaches, transitively,
/// through `[dependencies]` alone: cargo never resolves a path
/// dependency's own dev-dependencies.
fn runtime_closure(root: &Path) -> (BTreeSet<String>, BTreeSet<String>) {
    let mut crates = BTreeSet::new();
    let mut repositories = BTreeSet::new();
    let mut pending = vec![root.to_path_buf()];
    while let Some(manifest) = pending.pop() {
        for (name, path) in path_deps(&manifest, "dependencies") {
            repositories.insert(repository_of(&path));
            if crates.insert(name) {
                pending.push(path.join("Cargo.toml"));
            }
        }
    }
    (crates, repositories)
}

/// The repositories the section names, as a SET of names rather than a
/// set of substrings. A substring search cannot answer this question:
/// `https://github.com/tabnas/jsonic` contains
/// `https://github.com/tabnas/json`, so a page that names jsonic and
/// forgets json passes one and fails a reader, which is the exact
/// omission this file exists for.
fn named_repositories(install: &str) -> BTreeSet<String> {
    let url = Regex::new(r"https://github\.com/tabnas/([A-Za-z0-9._-]+)").unwrap();
    url.captures_iter(install)
        .map(|captures| captures[1].to_string())
        .collect()
}

fn install_section(readme: &str) -> &str {
    let start = readme
        .find("\n## Install")
        .expect("README.md has an Install section");
    let rest = &readme[start + 1..];
    let end = rest[3..].find("\n## ").map_or(rest.len(), |at| at + 3);
    &rest[..end]
}

#[test]
fn the_install_section_names_every_checkout_the_closure_needs() {
    let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
    let readme = fs::read_to_string(crate_dir.join("README.md")).expect("README.md");
    let install = install_section(&readme);

    let (crates, repositories) = runtime_closure(&crate_dir.join("Cargo.toml"));
    assert!(
        repositories.contains("json"),
        "the closure no longer reaches json; this test's premise has moved: {repositories:?}"
    );
    let named = named_repositories(install);
    for repository in &repositories {
        assert!(
            named.contains(repository),
            "rs/README.md's Install section does not name \
             `https://github.com/tabnas/{repository}`, which the path dependency closure \
             reaches ({repositories:?}); it names {named:?}, and a reader following the \
             page has no checkout for the rest, so cargo fails during resolution"
        );
    }

    // The crates a consumer's manifest has to list: this one, and every
    // direct dependency the page's examples reach into (a dependency's
    // dependencies are not passed on to its dependents).
    let fence_start = install.find("```toml").expect("a toml fence in Install");
    let fence_end = install[fence_start + 7..]
        .find("```")
        .map(|at| fence_start + 7 + at)
        .expect("the fence closes");
    let fence = &install[fence_start..fence_end];
    for name in path_deps(&crate_dir.join("Cargo.toml"), "dependencies")
        .into_iter()
        .map(|(name, _)| name)
        .chain(std::iter::once("tabnas-yaml".to_string()))
    {
        assert!(
            fence
                .lines()
                .any(|line| line.trim_start().starts_with(&format!("{name} "))),
            "the toml fence in rs/README.md has no `{name}` entry"
        );
    }
    assert!(
        crates.contains("tabnas-json"),
        "sanity: tabnas-json is in the closure, so it is the entry a reader is owed a \
         word about: {crates:?}"
    );
}

/// The test suite needs one more checkout than the closure, and the page
/// says which. Dev-dependencies are the one place a fresh reader meets a
/// repository the runtime closure never names.
#[test]
fn the_install_section_names_the_test_suites_extra_checkout() {
    let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
    let readme = fs::read_to_string(crate_dir.join("README.md")).expect("README.md");
    let install = install_section(&readme);
    let dev = path_deps(&crate_dir.join("Cargo.toml"), "dev-dependencies");
    assert!(
        !dev.is_empty(),
        "the crate has a path dev-dependency to check"
    );
    let named = named_repositories(install);
    for (_, path) in dev {
        let repository = repository_of(&path);
        assert!(
            named.contains(&repository),
            "rs/README.md's Install section does not name \
             `https://github.com/tabnas/{repository}`, which the test suite needs; it \
             names {named:?}"
        );
    }
    let _ = common::repo_root();
}
