use serde_json::Value;
use std::path::PathBuf;
use std::process::{Command, Output};

fn fixture(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("tests")
        .join("fixtures")
        .join(name)
}

fn run(arguments: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_jstreamer-renderer"))
        .args(arguments)
        .output()
        .expect("renderer CLI starts")
}

#[test]
fn compatibility_fixture_preserves_unknown_command_when_remote_is_v1() {
    let path = fixture("future-command.json");
    let output = run(&[
        "--compatibility-fixture",
        path.to_str().expect("fixture path is UTF-8"),
        "--remote-majors",
        "1",
        "--remote-capabilities",
        "render,future-capability",
    ]);

    assert!(output.status.success());
    let report: Value = serde_json::from_slice(&output.stdout).expect("valid JSON report");
    assert_eq!(report["negotiatedMajor"], 1);
    assert_eq!(report["fixtureMajor"], 1);
    assert_eq!(report["commandKind"]["state"], "unknown");
    assert_eq!(report["commandKind"]["wireValue"], "future-command");
}

#[test]
fn compatibility_fixture_reports_known_command_without_unknown_flag() {
    let path = fixture("known-command.json");
    let output = run(&[
        "--compatibility-fixture",
        path.to_str().expect("fixture path is UTF-8"),
        "--remote-majors",
        "2,1",
        "--remote-capabilities",
        "render",
    ]);

    assert!(output.status.success());
    let report: Value = serde_json::from_slice(&output.stdout).expect("valid JSON report");
    assert_eq!(report["commandKind"]["state"], "known");
    assert_eq!(report["commandKind"]["wireValue"], "play");
}

#[test]
fn malformed_fixture_and_invalid_major_are_typed_failures() {
    let path = fixture("missing-command-id.json");
    let malformed = run(&[
        "--compatibility-fixture",
        path.to_str().expect("fixture path is UTF-8"),
        "--remote-majors",
        "1",
        "--remote-capabilities",
        "render",
    ]);
    let invalid_major = run(&[
        "--compatibility-fixture",
        fixture("future-command.json")
            .to_str()
            .expect("fixture path is UTF-8"),
        "--remote-majors",
        "1,bad",
        "--remote-capabilities",
        "render",
    ]);

    assert_eq!(malformed.status.code(), Some(65));
    assert!(String::from_utf8_lossy(&malformed.stderr).contains("INVALID_FIXTURE"));
    assert_eq!(invalid_major.status.code(), Some(65));
    assert!(String::from_utf8_lossy(&invalid_major.stderr).contains("INVALID_PROTOCOL_MAJOR"));
}

#[test]
fn no_common_major_is_explicit_and_usage_is_distinct() {
    let path = fixture("future-command.json");
    let unsupported = run(&[
        "--compatibility-fixture",
        path.to_str().expect("fixture path is UTF-8"),
        "--remote-majors",
        "99",
        "--remote-capabilities",
        "render",
    ]);
    let usage = run(&["--unknown"]);

    assert_eq!(unsupported.status.code(), Some(78));
    assert!(String::from_utf8_lossy(&unsupported.stderr).contains("UNSUPPORTED_PROTOCOL_MAJOR"));
    assert_eq!(usage.status.code(), Some(64));
}
