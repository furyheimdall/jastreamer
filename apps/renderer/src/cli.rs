use crate::harness::SecretToken;
use crate::security::CertificateFingerprint;
use clap::{Parser, ValueEnum};
use std::io::{self, IsTerminal, Read};
use std::path::PathBuf;
use url::Url;

#[derive(Clone, Copy, Debug, ValueEnum)]
pub enum ShareMode {
    Shared,
}

#[derive(Debug, Parser)]
#[command(name = "jastreamer-renderer", disable_version_flag = true)]
pub struct Cli {
    #[arg(long, action = clap::ArgAction::SetTrue)]
    pub version: bool,
    #[arg(long, action = clap::ArgAction::SetTrue)]
    pub revision: bool,
    #[arg(long, action = clap::ArgAction::SetTrue)]
    pub protocol: bool,
    #[arg(long, value_name = "HTTPS_ORIGIN")]
    pub server_origin: Option<String>,
    #[arg(long, value_name = "SHA256")]
    pub server_fingerprint: Option<String>,
    #[arg(long, value_name = "ID")]
    pub renderer_id: Option<String>,
    #[arg(long, value_name = "DEVICE")]
    pub output_device: Option<String>,
    #[arg(long, value_enum)]
    pub share_mode: Option<ShareMode>,
    #[arg(long, value_name = "DIRECTORY")]
    pub state_directory: Option<PathBuf>,
    #[arg(long, action = clap::ArgAction::SetTrue)]
    pub token_stdin: bool,
    #[arg(long, value_name = "FILE")]
    pub compatibility_fixture: Option<PathBuf>,
    #[arg(long, value_name = "LIST")]
    pub remote_majors: Option<String>,
    #[arg(long, value_name = "LIST")]
    pub remote_capabilities: Option<String>,
}

pub struct RunConfig {
    pub server_origin: Url,
    pub server_fingerprint: CertificateFingerprint,
    pub renderer_id: String,
    pub output_device: String,
    pub state_directory: PathBuf,
    pub token: SecretToken,
}

#[derive(Debug, thiserror::Error)]
pub enum CliError {
    #[error("MISSING_ARGUMENT: {0}")]
    Missing(&'static str),
    #[error("INVALID_SERVER_ORIGIN: HTTPS origin without path, query, or credentials is required")]
    Origin,
    #[error(transparent)]
    Fingerprint(#[from] crate::security::FingerprintError),
    #[error(transparent)]
    Token(#[from] crate::harness::TokenError),
    #[error("TOKEN_INPUT_FAILED: {0}")]
    TokenInput(#[from] io::Error),
    #[error("INVALID_ARGUMENT: renderer ID and output device must be non-empty")]
    Invalid,
}

impl Cli {
    pub fn run_config(&self) -> Result<RunConfig, CliError> {
        let origin = self
            .server_origin
            .as_deref()
            .ok_or(CliError::Missing("--server-origin"))?
            .parse::<Url>()
            .map_err(|_| CliError::Origin)?;
        if origin.scheme() != "https"
            || origin.host_str().is_none()
            || origin.username() != ""
            || origin.password().is_some()
            || origin.query().is_some()
            || origin.fragment().is_some()
            || origin.path() != "/"
        {
            return Err(CliError::Origin);
        }
        let fingerprint = CertificateFingerprint::parse(
            self.server_fingerprint
                .as_deref()
                .ok_or(CliError::Missing("--server-fingerprint"))?,
        )?;
        let renderer_id = required_nonempty(self.renderer_id.as_deref(), "--renderer-id")?;
        let output_device = required_nonempty(self.output_device.as_deref(), "--output-device")?;
        self.share_mode.ok_or(CliError::Missing("--share-mode"))?;
        let state_directory = self
            .state_directory
            .clone()
            .ok_or(CliError::Missing("--state-directory"))?;
        if !self.token_stdin {
            return Err(CliError::Missing("--token-stdin"));
        }
        let token = if io::stdin().is_terminal() {
            SecretToken::parse(&rpassword::prompt_password("Renderer bearer: ")?)?
        } else {
            let mut input = String::new();
            io::stdin().take(16 * 1024).read_to_string(&mut input)?;
            SecretToken::parse(&input)?
        };
        Ok(RunConfig {
            server_origin: origin,
            server_fingerprint: fingerprint,
            renderer_id,
            output_device,
            state_directory,
            token,
        })
    }
}

fn required_nonempty(value: Option<&str>, name: &'static str) -> Result<String, CliError> {
    let value = value.ok_or(CliError::Missing(name))?.trim();
    if value.is_empty() || value.len() > 256 || value.chars().any(char::is_control) {
        return Err(CliError::Invalid);
    }
    Ok(value.to_owned())
}
