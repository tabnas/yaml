//! A derived instance installs its own grammar (tabnas/parser#244).
//!
//! `Tabnas::derive` builds the child from the parent's options, copies
//! the parent's decorations onto it, and re-runs the parent's plugins so
//! the child earns its rules again, as the canonical TypeScript does.
//! This plugin used to guard its install with a decoration, so on the
//! child it found its own mark already set, returned early, and the
//! child parsed `a: 1` to null with no error. The guard now reads the
//! instance's rules.

use tabnas_yaml::YamlOptions;

#[test]
fn a_derived_instance_keeps_the_grammar() {
    let parent = tabnas_yaml::make();
    assert_eq!(parent.parse("a: 1").unwrap().to_string(), r#"{"a":1}"#);
    let child = parent.derive(|_| {}).unwrap();
    assert_eq!(child.parse("a: 1").unwrap().to_string(), r#"{"a":1}"#);
    assert_eq!(child.parse("- a\n- b").unwrap().to_string(), r#"["a","b"]"#);
    let grandchild = child.derive(|_| {}).unwrap();
    assert_eq!(
        grandchild.parse("a:\n  b: 1").unwrap().to_string(),
        r#"{"a":{"b":1}}"#
    );
}

#[test]
fn a_derived_instance_keeps_the_plugin_options() {
    let parent = tabnas_yaml::make_with(YamlOptions { meta: true });
    let child = parent.derive(|_| {}).unwrap();
    assert_eq!(
        child.parse("---\na: 1\n...").unwrap().to_string(),
        r#"{"meta":{"directives":[],"explicit":true,"ended":true},"content":{"a":1}}"#
    );
}

#[test]
fn a_second_install_on_one_instance_is_a_no_op() {
    let mut parser = tabnas_jsonic::make();
    tabnas_yaml::yaml(&mut parser, &YamlOptions::default()).unwrap();
    let rules = parser.rule_names();
    tabnas_yaml::yaml(&mut parser, &YamlOptions::default()).unwrap();
    assert_eq!(
        parser.rule_names(),
        rules,
        "the second call installs nothing"
    );
    assert_eq!(parser.parse("a: 1").unwrap().to_string(), r#"{"a":1}"#);
}
