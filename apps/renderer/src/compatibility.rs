use crate::cli::Cli;
use crate::{
    FakeBackend, Negotiation, ProtocolError, Renderer, SUPPORTED_MAJORS, parse_renderer_command,
};
use serde::{Deserialize, Serialize};
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
    error_code: Option<&'static str>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct CommandKindReport {
    state: &'static str,
    wire_value: String,
}

#[derive(Debug, thiserror::Error)]
pub enum CompatibilityError {
    #[error("MISSING_ARGUMENT: {0}")]
    Missing(&'static str),
    #[error("INVALID_PROTOCOL_MAJOR: {0}")]
    InvalidMajor(String),
    #[error("INVALID_CAPABILITY")]
    InvalidCapability,
    #[error("INVALID_FIXTURE: {0}")]
    InvalidFixture(String),
    #[error(transparent)]
    Protocol(#[from] ProtocolError),
}

pub fn run(arguments: &Cli) -> Result<(), CompatibilityError> {
    let path = arguments
        .compatibility_fixture
        .as_ref()
        .ok_or(CompatibilityError::Missing("--compatibility-fixture"))?;
    let remote = parse_majors(
        arguments
            .remote_majors
            .as_deref()
            .ok_or(CompatibilityError::Missing("--remote-majors"))?,
    )?;
    let capabilities = parse_capabilities(
        arguments
            .remote_capabilities
            .as_deref()
            .ok_or(CompatibilityError::Missing("--remote-capabilities"))?,
    )?;
    let fixture: WireFixture = serde_json::from_str(
        &fs::read_to_string(path)
            .map_err(|error| CompatibilityError::InvalidFixture(error.to_string()))?,
    )
    .map_err(|error| CompatibilityError::InvalidFixture(error.to_string()))?;
    if fixture.command_id.is_empty() {
        return Err(CompatibilityError::InvalidFixture(
            "missing command_id".to_owned(),
        ));
    }
    let remote_capabilities = capabilities.iter().map(String::as_str).collect::<Vec<_>>();
    let major = Renderer::<FakeBackend>::negotiate(Negotiation {
        local_majors: &SUPPORTED_MAJORS,
        remote_majors: &remote,
        required_capabilities: &["render"],
        remote_capabilities: &remote_capabilities,
    })?;
    if fixture.protocol_major != major {
        return Err(CompatibilityError::InvalidFixture(
            "fixture major differs from negotiated major".to_owned(),
        ));
    }
    let (state, error_code) =
        match parse_renderer_command(major, &fixture.command_id, &fixture.command_kind) {
            Ok(_) => ("known", None),
            Err(ProtocolError::UnsupportedCommand { .. }) => {
                ("unsupported", Some("UNSUPPORTED_COMMAND"))
            }
            Err(error) => return Err(error.into()),
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
        error_code,
    };
    println!(
        "{}",
        serde_json::to_string(&report)
            .map_err(|error| CompatibilityError::InvalidFixture(error.to_string()))?
    );
    Ok(())
}

fn parse_majors(value: &str) -> Result<Vec<u16>, CompatibilityError> {
    let majors = value
        .split(',')
        .map(|item| {
            let major = item
                .parse::<u16>()
                .map_err(|_| CompatibilityError::InvalidMajor(item.to_owned()))?;
            if major == 0 {
                return Err(CompatibilityError::InvalidMajor(item.to_owned()));
            }
            Ok(major)
        })
        .collect::<Result<Vec<_>, _>>()?;
    if majors.is_empty() {
        return Err(CompatibilityError::InvalidMajor(value.to_owned()));
    }
    Ok(majors)
}

fn parse_capabilities(value: &str) -> Result<Vec<String>, CompatibilityError> {
    let capabilities = value
        .split(',')
        .map(str::trim)
        .map(str::to_owned)
        .collect::<Vec<_>>();
    if capabilities.is_empty() || capabilities.iter().any(String::is_empty) {
        return Err(CompatibilityError::InvalidCapability);
    }
    Ok(capabilities)
}
