use crate::harness::{JournalResult, ProtocolFailure};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Capabilities {
    pub commands: Vec<String>,
    pub media_types: Vec<String>,
    pub supports_range: bool,
    pub max_channels: u16,
    pub max_sample_rate_hz: u32,
}

impl Default for Capabilities {
    fn default() -> Self {
        Self {
            commands: ["play", "pause", "resume", "stop", "seek"]
                .map(str::to_owned)
                .to_vec(),
            media_types: [
                "audio/flac",
                "audio/mpeg",
                "audio/ogg",
                "audio/opus",
                "audio/vorbis",
                "audio/wav",
                "audio/x-wav",
            ]
            .map(str::to_owned)
            .to_vec(),
            supports_range: true,
            max_channels: 2,
            max_sample_rate_hz: 192_000,
        }
    }
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Hello<'a> {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: &'static str,
    pub renderer_id: &'a str,
    pub supported_majors: [u16; 2],
    pub capabilities: Capabilities,
    pub last_server_sequence: u64,
    pub pending_results: &'a [JournalResult],
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Welcome {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: String,
    pub selected_major: u16,
    pub session_epoch: String,
    pub next_sequence: u64,
    pub capabilities: Vec<String>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaRepresentation {
    #[serde(alias = "uri", alias = "url")]
    pub url: String,
    pub mime_type: String,
    #[serde(default)]
    pub headers: std::collections::BTreeMap<String, String>,
    #[serde(default)]
    pub seekable: bool,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Command {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: String,
    pub command_id: String,
    pub sequence: u64,
    pub session_epoch: String,
    pub zone_id: String,
    pub play_id: Option<String>,
    pub kind: String,
    pub deadline: String,
    pub position_ms: Option<u64>,
    pub media: Option<MediaRepresentation>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ResultAck {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: String,
    pub result_id: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ServerError {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: String,
    pub code: String,
    pub message: String,
    pub retryable: bool,
}

#[derive(Debug)]
pub enum ServerFrame {
    Welcome(Welcome),
    Command(Command),
    ResultAck(ResultAck),
    Error(ServerError),
}

#[derive(Deserialize)]
struct Envelope {
    #[serde(rename = "protocolMajor")]
    protocol_major: u16,
    #[serde(rename = "type")]
    frame_type: String,
}

#[derive(Debug, thiserror::Error)]
pub enum FrameError {
    #[error("INVALID_MESSAGE: {0}")]
    Invalid(#[from] serde_json::Error),
    #[error("UNSUPPORTED_PROTOCOL_MAJOR: {0}")]
    UnsupportedMajor(u16),
    #[error("INVALID_MESSAGE: unsupported frame type {0}")]
    UnsupportedType(String),
}

pub fn decode_server_frame(payload: &[u8]) -> Result<ServerFrame, FrameError> {
    let envelope: Envelope = serde_json::from_slice(payload)?;
    if envelope.protocol_major != 3 {
        return Err(FrameError::UnsupportedMajor(envelope.protocol_major));
    }
    match envelope.frame_type.as_str() {
        "welcome" => Ok(ServerFrame::Welcome(serde_json::from_slice(payload)?)),
        "command" => Ok(ServerFrame::Command(serde_json::from_slice(payload)?)),
        "result.ack" => Ok(ServerFrame::ResultAck(serde_json::from_slice(payload)?)),
        "error" => Ok(ServerFrame::Error(serde_json::from_slice(payload)?)),
        value => Err(FrameError::UnsupportedType(value.to_owned())),
    }
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackEventFrame {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: &'static str,
    pub event_id: String,
    pub session_epoch: String,
    pub play_id: String,
    pub kind: &'static str,
    pub position_ms: Option<u64>,
    pub observed_at: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CommandAck<'a> {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: &'static str,
    pub command_id: &'a str,
    pub sequence: u64,
    pub status: &'static str,
    pub error: Option<&'a ProtocolFailure>,
}

pub fn failure(code: &str, message: &str, retryable: bool) -> ProtocolFailure {
    ProtocolFailure {
        protocol_major: 3,
        frame_type: "error".to_owned(),
        code: code.to_owned(),
        message: message.to_owned(),
        retryable,
    }
}
