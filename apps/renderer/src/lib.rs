pub mod audio;
mod audio_buffer;
pub mod cli;
pub mod compatibility;
mod decoder;
pub mod endpoint;
#[cfg(windows)]
mod endpoint_windows;
pub mod engine;
pub mod harness;
pub mod media;
mod media_http;
mod media_stream;
mod opus;
pub mod protocol;
pub mod security;
pub mod session;
mod session_executor;
mod session_messages;
mod session_transport;
pub mod wasapi;

pub const SUPPORTED_MAJORS: [u16; 2] = [3, 2];
pub const SUPPORTED_MAJORS_HEADER: &str = "X-Jake-Supported-Protocol-Majors";
pub const SELECTED_MAJOR_HEADER: &str = "X-Jake-Selected-Protocol-Major";
const V2_CAPABILITIES: [&str; 1] = ["render"];
const V3_CAPABILITIES: [&str; 4] = [
    "render",
    "renderer-session",
    "media-representations",
    "durable-results",
];

#[derive(Debug, PartialEq, Eq)]
pub enum ProtocolError {
    UnsupportedProtocolMajor { requested: u16 },
    MissingCapability { capability: String },
    UnsupportedCommand { command_id: String, kind: String },
}
impl std::fmt::Display for ProtocolError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::UnsupportedProtocolMajor { requested } => {
                write!(f, "UNSUPPORTED_PROTOCOL_MAJOR: {requested}")
            }
            Self::MissingCapability { capability } => {
                write!(f, "MISSING_CAPABILITY: {capability}")
            }
            Self::UnsupportedCommand { command_id, kind } => {
                write!(f, "UNSUPPORTED_COMMAND: {command_id} kind={kind}")
            }
        }
    }
}
impl std::error::Error for ProtocolError {}
#[derive(Debug, PartialEq, Eq)]
pub enum BackendError {
    InitializationFailed,
}
impl std::fmt::Display for BackendError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InitializationFailed => write!(formatter, "backend initialization failed"),
        }
    }
}
impl std::error::Error for BackendError {}
#[derive(Debug, PartialEq, Eq)]
pub enum RendererCommandKind {
    Play,
    Pause,
    Resume,
    Stop,
    Seek,
}

pub const fn capabilities_for_major(major: u16) -> &'static [&'static str] {
    match major {
        2 => &V2_CAPABILITIES,
        3 => &V3_CAPABILITIES,
        _ => &[],
    }
}

pub fn parse_renderer_command(
    selected_major: u16,
    command_id: &str,
    kind: &str,
) -> Result<RendererCommandKind, ProtocolError> {
    match (selected_major, kind) {
        (2 | 3, "play") => Ok(RendererCommandKind::Play),
        (3, "pause") => Ok(RendererCommandKind::Pause),
        (3, "resume") => Ok(RendererCommandKind::Resume),
        (2 | 3, "stop") => Ok(RendererCommandKind::Stop),
        (2 | 3, "seek") => Ok(RendererCommandKind::Seek),
        (_, _) => Err(ProtocolError::UnsupportedCommand {
            command_id: command_id.to_owned(),
            kind: kind.to_owned(),
        }),
    }
}

pub trait AudioBackend {
    fn start(&mut self) -> Result<(), BackendError>;
    fn diagnostics(&self) -> String;
}
#[derive(Default)]
pub struct FakeBackend {
    started: bool,
}
impl AudioBackend for FakeBackend {
    fn start(&mut self) -> Result<(), BackendError> {
        self.started = true;
        Ok(())
    }
    fn diagnostics(&self) -> String {
        format!("fake backend started={}", self.started)
    }
}
pub struct Renderer<B: AudioBackend> {
    backend: B,
}

pub struct Negotiation<'a> {
    pub local_majors: &'a [u16],
    pub remote_majors: &'a [u16],
    pub required_capabilities: &'a [&'a str],
    pub remote_capabilities: &'a [&'a str],
}

impl<B: AudioBackend> Renderer<B> {
    pub fn new(backend: B) -> Self {
        Self { backend }
    }
    pub fn negotiate(input: Negotiation<'_>) -> Result<u16, ProtocolError> {
        let major = input
            .local_majors
            .iter()
            .filter(|major| input.remote_majors.contains(major))
            .copied()
            .filter(|major| *major == 2 || *major == 3)
            .max();
        let major = match major {
            Some(value) => value,
            None => {
                let requested = input.remote_majors.iter().copied().fold(0, u16::max);
                return Err(ProtocolError::UnsupportedProtocolMajor { requested });
            }
        };
        if let Some(capability) = input.required_capabilities.iter().find(|capability| {
            !input
                .remote_capabilities
                .iter()
                .any(|remote| remote == *capability)
        }) {
            return Err(ProtocolError::MissingCapability {
                capability: (*capability).to_owned(),
            });
        }
        Ok(major)
    }
    pub fn start(&mut self) -> Result<(), BackendError> {
        self.backend.start()
    }
    pub fn diagnostics(&self) -> String {
        self.backend.diagnostics()
    }
}
#[cfg(windows)]
pub mod wasapi {
    use super::{AudioBackend, BackendError};
    pub struct WasapiBackend;
    impl AudioBackend for WasapiBackend {
        fn start(&mut self) -> Result<(), BackendError> {
            Ok(())
        }
        fn diagnostics(&self) -> String {
            "wasapi boundary".to_owned()
        }
    }
}
