pub const SUPPORTED_MAJORS: [u16; 2] = [2, 1];
#[derive(Debug, PartialEq, Eq)]
pub enum ProtocolError {
    UnsupportedProtocolMajor { requested: u16 },
    MissingCapability { capability: String },
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
impl<B: AudioBackend> Renderer<B> {
    pub fn new(backend: B) -> Self {
        Self { backend }
    }
    pub fn negotiate(
        local: &[u16],
        remote: &[u16],
        required_capabilities: &[&str],
        remote_capabilities: &[&str],
    ) -> Result<u16, ProtocolError> {
        let major = local
            .iter()
            .filter(|major| remote.contains(major))
            .copied()
            .filter(|major| *major == 1 || *major == 2)
            .max();
        let major = match major {
            Some(value) => value,
            None => {
                let requested = remote.iter().copied().fold(0, u16::max);
                return Err(ProtocolError::UnsupportedProtocolMajor { requested });
            }
        };
        if let Some(capability) = required_capabilities.iter().find(|capability| {
            !remote_capabilities
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
