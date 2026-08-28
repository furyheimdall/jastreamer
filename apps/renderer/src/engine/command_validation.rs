use super::{Engine, EngineError};
use crate::audio::PlaybackBackend;
use crate::media::MediaLoader;
use crate::protocol::Command;

impl<B: PlaybackBackend, M: MediaLoader> Engine<B, M> {
    pub(super) fn validate(&self, command: &Command) -> Result<(), EngineError> {
        if command.protocol_major != 3 || command.frame_type != "command" {
            return Err(EngineError::Invalid(
                "command envelope is invalid".to_owned(),
            ));
        }
        if command.session_epoch != self.session_epoch {
            return Err(EngineError::StaleEpoch);
        }
        if command.media.as_ref().is_some_and(|media| {
            media.headers.keys().any(|name| {
                name.eq_ignore_ascii_case("authorization") || name.eq_ignore_ascii_case("cookie")
            })
        }) {
            return Err(EngineError::Invalid(
                "media contains forbidden credential headers".to_owned(),
            ));
        }
        let cursor = self.journal.last_server_sequence();
        if command.sequence > cursor.saturating_add(1) {
            return Err(EngineError::SequenceGap {
                expected: cursor.saturating_add(1),
                received: command.sequence,
            });
        }
        Ok(())
    }
}
