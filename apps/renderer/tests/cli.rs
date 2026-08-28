use serde_json::Value;
use std::io::Write;
use std::path::PathBuf;
use std::process::{Command, Output, Stdio};

fn fixture(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("tests")
        .join("fixtures")
        .join(name)
}

fn run(arguments: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_jastreamer-renderer"))
        .args(arguments)
        .output()
        .expect("renderer CLI starts")
}

#[test]
fn foreground_help_exposes_explicit_config_and_no_token_value_option() {
    // Given / When
    let output = run(&["--help"]);

    // Then
    assert!(output.status.success());
    let help = String::from_utf8_lossy(&output.stdout);
    for option in [
        "--server-origin",
        "--server-fingerprint",
        "--renderer-id",
        "--output-device",
        "--share-mode",
        "--state-directory",
        "--token-stdin",
    ] {
        assert!(help.contains(option), "missing option {option}");
    }
    assert!(!help.contains("--token <"));
}

#[test]
fn invalid_stdin_token_is_rejected_without_echoing_secret() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let mut child = Command::new(env!("CARGO_BIN_EXE_jastreamer-renderer"))
        .args([
            "--server-origin",
            "https://127.0.0.1:1",
            "--server-fingerprint",
            &"0".repeat(64),
            "--renderer-id",
            "renderer",
            "--output-device",
            "fixture",
            "--share-mode",
            "shared",
            "--state-directory",
            directory.path().to_str().expect("UTF-8 path"),
            "--token-stdin",
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("renderer starts");

    // When
    child
        .stdin
        .as_mut()
        .expect("stdin pipe")
        .write_all(b"secret\0sentinel\n")
        .expect("token sent");
    let output = child.wait_with_output().expect("renderer exits");

    // Then
    assert!(!output.status.success());
    assert!(!String::from_utf8_lossy(&output.stderr).contains("secret"));
}

#[test]
fn compatibility_fixture_returns_unsupported_command_when_remote_is_v3() {
    let path = fixture("future-command.json");
    let output = run(&[
        "--compatibility-fixture",
        path.to_str().expect("fixture path is UTF-8"),
        "--remote-majors",
        "3,2",
        "--remote-capabilities",
        "render,renderer-session,future-capability",
    ]);

    assert!(output.status.success());
    let report: Value = serde_json::from_slice(&output.stdout).expect("valid JSON report");
    assert_eq!(report["negotiatedMajor"], 3);
    assert_eq!(report["fixtureMajor"], 3);
    assert_eq!(report["commandKind"]["state"], "unsupported");
    assert_eq!(report["errorCode"], "UNSUPPORTED_COMMAND");
    assert_eq!(report["commandKind"]["wireValue"], "future-command");
}

#[test]
fn compatibility_fixture_reports_known_command_without_unknown_flag() {
    let path = fixture("known-command.json");
    let output = run(&[
        "--compatibility-fixture",
        path.to_str().expect("fixture path is UTF-8"),
        "--remote-majors",
        "2",
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
        "3",
        "--remote-capabilities",
        "render,renderer-session",
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
