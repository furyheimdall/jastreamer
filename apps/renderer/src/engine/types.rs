use crate::harness::{JournalError, JournalResult};
use crate::protocol::Command;

#[derive(Debug)]
pub struct CommandOutcome {
    pub ack_status: &'static str,
    pub result: JournalResult,
}

#[derive(Debug)]
pub struct CommandAcceptance {
    pub ack_status: &'static str,
    pub command: Option<Command>,
    pub result: Option<JournalResult>,
}

#[derive(Debug, thiserror::Error)]
pub enum EngineError {
    #[error(transparent)]
    Journal(#[from] JournalError),
    #[error("STALE_SESSION_EPOCH: command belongs to another session")]
    StaleEpoch,
    #[error("COMMAND_SEQUENCE_GAP: expected {expected}, received {received}")]
    SequenceGap { expected: u64, received: u64 },
    #[error("INVALID_MESSAGE: {0}")]
    Invalid(String),
}
