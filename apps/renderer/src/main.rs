use jastreamer_renderer::{FakeBackend, ProtocolError, Renderer, SUPPORTED_MAJORS};
use serde::{Deserialize, Serialize};
use std::fmt;
use std::fs;

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WireFixture {
    protocol_major: u16,
    command_id: String,
    position_ms: u64,
    capabilities: Vec<String>,
    command_kind: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct CompatibilityReport {
    negotiated_major: u16,
    fixture_major: u16,
    position_ms: u64,
    capabilities: Vec<String>,
    command_kind: CommandKindReport,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct CommandKindReport {
    state: &'static str,
    wire_value: String,
}

#[derive(Debug)]
enum CliError {
    Usage,
    MissingArgument(&'static str),
    InvalidMajor(String),
    InvalidCapability,
    InvalidFixture(String),
    Protocol(ProtocolError),
}

impl fmt::Display for CliError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Usage => write!(f, "USAGE"),
            Self::MissingArgument(name) => write!(f, "MISSING_ARGUMENT: {name}"),
            Self::InvalidMajor(value) => write!(f, "INVALID_PROTOCOL_MAJOR: {value}"),
            Self::InvalidCapability => write!(f, "INVALID_CAPABILITY"),
            Self::InvalidFixture(message) => write!(f, "INVALID_FIXTURE: {message}"),
            Self::Protocol(error) => write!(f, "{error}"),
        }
    }
}

impl std::error::Error for CliError {}

impl CliError {
    const fn exit_code(&self) -> i32 {
        match self {
            Self::Usage | Self::MissingArgument(_) => 64,
            Self::Protocol(ProtocolError::UnsupportedProtocolMajor { .. }) => 78,
            Self::InvalidMajor(_)
            | Self::InvalidCapability
            | Self::InvalidFixture(_)
            | Self::Protocol(ProtocolError::MissingCapability { .. }) => 65,
        }
    }
}

fn main() {
    let arguments: Vec<String> = std::env::args().collect();
    let result = match arguments.get(1).map(String::as_str) {
        Some("--help") => {
            println!(
                "jastreamer-renderer [--version|--revision|--protocol|--compatibility-fixture FILE --remote-majors LIST --remote-capabilities LIST]"
            );
            Ok(())
        }
        Some("--version") => {
            println!("jastreamer-renderer 0.1.0");
            Ok(())
        }
        Some("--revision") => {
            println!("unknown");
            Ok(())
        }
        Some("--protocol") => {
            println!("2 (compatible with 1)");
            Ok(())
        }
        Some("--compatibility-fixture") => compatibility(&arguments),
        _ => Err(CliError::Usage),
    };
    if let Err(error) = result {
        eprintln!("{error}");
        std::process::exit(error.exit_code());
    }
}

fn argument<'a>(arguments: &'a [String], name: &'static str) -> Result<&'a str, CliError> {
    arguments
        .iter()
        .position(|argument| argument == name)
        .and_then(|index| arguments.get(index + 1))
        .map(String::as_str)
        .ok_or(CliError::MissingArgument(name))
}
fn parse_majors(value: &str) -> Result<Vec<u16>, CliError> {
    let majors = value
        .split(',')
        .map(|item| {
            let major = item
                .parse::<u16>()
                .map_err(|_| CliError::InvalidMajor(item.to_owned()))?;
            if major == 0 {
                return Err(CliError::InvalidMajor(item.to_owned()));
            }
            Ok(major)
        })
        .collect::<Result<Vec<_>, _>>()?;
    if majors.is_empty() {
        return Err(CliError::InvalidMajor(value.to_owned()));
    }
    Ok(majors)
}

fn parse_capabilities(value: &str) -> Result<Vec<String>, CliError> {
    let capabilities = value
        .split(',')
        .map(str::trim)
        .map(str::to_owned)
        .collect::<Vec<_>>();
    if capabilities.is_empty() || capabilities.iter().any(String::is_empty) {
        return Err(CliError::InvalidCapability);
    }
    Ok(capabilities)
}

fn compatibility(arguments: &[String]) -> Result<(), CliError> {
    let path = argument(arguments, "--compatibility-fixture")?;
    let remote = parse_majors(argument(arguments, "--remote-majors")?)?;
    let capabilities = parse_capabilities(argument(arguments, "--remote-capabilities")?)?;
    let fixture: WireFixture = serde_json::from_str(
        &fs::read_to_string(path).map_err(|error| CliError::InvalidFixture(error.to_string()))?,
    )
    .map_err(|error| CliError::InvalidFixture(error.to_string()))?;
    if fixture.command_id.is_empty() {
        return Err(CliError::InvalidFixture("missing command_id".to_owned()));
    }
    let remote_capabilities = capabilities.iter().map(String::as_str).collect::<Vec<_>>();
    let major = Renderer::<FakeBackend>::negotiate(
        &SUPPORTED_MAJORS,
        &remote,
        &["render"],
        &remote_capabilities,
    )
    .map_err(CliError::Protocol)?;
    if fixture.protocol_major != major {
        return Err(CliError::InvalidFixture(
            "fixture major differs from negotiated major".to_owned(),
        ));
    }
    let state = if matches!(fixture.command_kind.as_str(), "play" | "stop" | "seek") {
        "known"
    } else {
        "unknown"
    };
    let report = CompatibilityReport {
        negotiated_major: major,
        fixture_major: fixture.protocol_major,
        position_ms: fixture.position_ms,
        capabilities: fixture.capabilities,
        command_kind: CommandKindReport {
            state,
            wire_value: fixture.command_kind,
        },
    };
    println!(
        "{}",
        serde_json::to_string(&report)
            .map_err(|error| CliError::InvalidFixture(error.to_string()))?
    );
    Ok(())
}
