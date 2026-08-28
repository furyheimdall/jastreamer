use crate::audio::PlaybackEvent;
use crate::engine::EngineError;
use crate::harness::JournalResult;
use crate::protocol::{Capabilities, Command, Hello, PlaybackEventFrame, failure};

pub fn hello<'a>(
    renderer_id: &'a str,
    last_server_sequence: u64,
    pending_results: &'a [JournalResult],
) -> Hello<'a> {
    Hello {
        protocol_major: 3,
        frame_type: "hello",
        renderer_id,
        supported_majors: [3, 2],
        capabilities: Capabilities::default(),
        last_server_sequence,
        pending_results,
    }
}

pub fn playback_frame(
    epoch: &str,
    play_id: &str,
    event: PlaybackEvent,
) -> Result<PlaybackEventFrame, time::error::Format> {
    let (kind, position_ms) = match event {
        PlaybackEvent::Ended { position_ms } => ("ended", Some(position_ms)),
        PlaybackEvent::OutputInvalidated | PlaybackEvent::Failed { .. } => ("failed", None),
    };
    let observed_at =
        time::OffsetDateTime::now_utc().format(&time::format_description::well_known::Rfc3339)?;
    let identity = format!("{play_id}:{kind}:{position_ms:?}");
    Ok(PlaybackEventFrame {
        protocol_major: 3,
        frame_type: "playback.event",
        event_id: format!(
            "renderer-event-{}",
            crate::harness::stable_result_id(&identity)
        ),
        session_epoch: epoch.to_owned(),
        play_id: play_id.to_owned(),
        kind,
        position_ms,
        observed_at,
    })
}

pub const fn rejection_code(error: &EngineError) -> &'static str {
    match error {
        EngineError::Journal(crate::harness::JournalError::CommandConflict { .. }) => {
            "COMMAND_ID_CONFLICT"
        }
        EngineError::SequenceGap { .. } => "COMMAND_SEQUENCE_GAP",
        EngineError::StaleEpoch => "STALE_SESSION_EPOCH",
        EngineError::Journal(_) | EngineError::Invalid(_) => "INVALID_MESSAGE",
    }
}

pub fn command_fingerprint(command: &Command) -> Result<String, serde_json::Error> {
    crate::harness::payload_digest(command)
}

pub fn command_error(code: &str, message: &str) -> crate::harness::ProtocolFailure {
    failure(code, message, false)
}
