use super::payload_digest;
use crate::protocol::Command;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JournalCommand {
    pub command_id: String,
    pub sequence: u64,
    pub session_epoch: String,
    pub kind: String,
    pub payload_digest: String,
    pub command: Command,
}

impl JournalCommand {
    pub fn from_command(command: Command) -> Result<Self, serde_json::Error> {
        Ok(Self {
            command_id: command.command_id.clone(),
            sequence: command.sequence,
            session_epoch: command.session_epoch.clone(),
            kind: command.kind.clone(),
            payload_digest: payload_digest(&command)?,
            command,
        })
    }
}

#[derive(Clone, Debug, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JournalResult {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: String,
    pub command_id: String,
    pub result_id: String,
    pub status: String,
    pub observed_state: Option<String>,
    pub position_ms: Option<u64>,
    pub error: Option<ProtocolFailure>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProtocolFailure {
    pub protocol_major: u16,
    #[serde(rename = "type")]
    pub frame_type: String,
    pub code: String,
    pub message: String,
    pub retryable: bool,
}

#[derive(Debug, PartialEq, Eq)]
pub enum CommandDecision {
    Execute,
    Duplicate,
}

#[derive(Debug, thiserror::Error)]
pub enum JournalError {
    #[error("STATE_DIRECTORY_BUSY: another renderer owns this state directory")]
    StateDirectoryBusy,
    #[error("COMMAND_ID_CONFLICT: {command_id}")]
    CommandConflict { command_id: String },
    #[error("JOURNAL_IO: {0}")]
    Io(#[from] std::io::Error),
    #[error("JOURNAL_INVALID: {0}")]
    Invalid(#[from] serde_json::Error),
}
