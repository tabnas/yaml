// The translation parts: what the manifest says and what the crate
// embeds are the same files.
//
// `manifest_text()` embeds `tabnas.plugin.json` and `render_text()`
// embeds `alchemy/render.alc`, so a renamed file fails the build. What
// the build cannot see is the manifest's `translate.render` naming a
// different file, or the manifest edited alone. A host reads the shapes
// and the loss from the embedded manifest and the render from the
// accessor, so the two must name one text: this reads the path the
// embedded manifest names, from the repository, and compares it with the
// accessor's.

mod common;

use std::fs;

use serde_json::Value;

fn translate() -> Value {
    let manifest: Value =
        serde_json::from_str(tabnas_yaml::manifest_text()).expect("the manifest is JSON");
    manifest
        .get("translate")
        .cloned()
        .expect("the manifest carries a translate object")
}

#[test]
fn the_render_the_manifest_names_is_the_one_the_crate_embeds() {
    let translate = translate();
    let path = translate["render"]
        .as_str()
        .expect("translate.render names a file");
    let on_disk = fs::read_to_string(common::repo_root().join(path))
        .unwrap_or_else(|e| panic!("translate.render names {path}, which cannot be read: {e}"));
    assert_eq!(
        on_disk,
        tabnas_yaml::render_text(),
        "translate.render names {path}, and render_text() embeds another text"
    );
}

/// YAML is read as a tree and written from one. Its events carry the
/// tree already, so there is no lift, and no accessor for one.
#[test]
fn yaml_reads_and_writes_a_tree_with_no_lift() {
    let translate = translate();
    assert_eq!(translate["reads"], "tree");
    assert_eq!(translate["writes"], "tree");
    assert_eq!(translate.get("lift"), None);
}

/// The host prints the loss lines verbatim, so each is a sentence.
#[test]
fn the_loss_is_a_list_of_sentences() {
    let translate = translate();
    let loss = translate["loss"]
        .as_array()
        .expect("translate.loss is a list");
    assert!(!loss.is_empty());
    for line in loss {
        let line = line.as_str().expect("each loss line is a string");
        assert!(
            line.starts_with(char::is_uppercase) && line.ends_with('.'),
            "{line:?} is not a sentence"
        );
    }
}

/// A host links the render with its own program and other formats'
/// parts, so every definition is named for YAML, the entry point is
/// `yaml-render`, and the file defines no `export` of its own.
#[test]
fn the_render_is_a_library_named_for_yaml() {
    let names: Vec<&str> = tabnas_yaml::render_text()
        .lines()
        .filter_map(|line| line.strip_prefix("def "))
        .filter_map(|rest| rest.split_whitespace().next())
        .collect();
    assert!(names.contains(&"yaml-render"), "{names:?}");
    for name in &names {
        assert!(name.starts_with("yaml-"), "{name} is not named for YAML");
    }
}
