use super::Engine;
use crate::audio::{AudioError, PlaybackBackend, PlaybackState};
use crate::media::{MediaError, MediaLoader};
use crate::protocol::Command;

impl<B: PlaybackBackend, M: MediaLoader> Engine<B, M> {
    pub(super) fn play(&mut self, command: &Command) -> Result<(), AudioError> {
        let media = command
            .media
            .as_ref()
            .ok_or_else(|| AudioError::Failed("play command has no media".to_owned()))?;
        let position = command.position_ms.unwrap_or(0);
        let stream = self
            .media
            .open(media, position)
            .map_err(media_audio_error)?;
        self.backend.load(stream)?;
        self.active_play_id = command.play_id.clone();
        self.active_media = Some(media.clone());
        let result = self.backend.play();
        if result.is_ok() {
            self.desired_state = PlaybackState::Playing;
        }
        result
    }

    pub(super) fn seek(&mut self, position_ms: u64) -> Result<(), AudioError> {
        let media = self
            .active_media
            .as_ref()
            .ok_or_else(|| AudioError::Failed("no active media for seek".to_owned()))?;
        let previous = self.desired_state;
        let stream = self
            .media
            .open(media, position_ms)
            .map_err(media_audio_error)?;
        self.backend.stop()?;
        self.backend.load(stream)?;
        match previous {
            PlaybackState::Playing => self.backend.play(),
            PlaybackState::Paused => self.backend.pause(),
            PlaybackState::Idle | PlaybackState::Stopped => Ok(()),
        }
    }

    pub(super) fn recover_output(&mut self) -> Result<(), AudioError> {
        let media = self
            .active_media
            .as_ref()
            .ok_or_else(|| AudioError::Failed("no active media for recovery".to_owned()))?;
        let position_ms = self.backend.position_ms();
        let stream = self
            .media
            .open(media, position_ms)
            .map_err(media_audio_error)?;
        self.backend.stop()?;
        self.backend.load(stream)?;
        match self.desired_state {
            PlaybackState::Playing => self.backend.play()?,
            PlaybackState::Paused => self.backend.pause()?,
            PlaybackState::Idle | PlaybackState::Stopped => {}
        }
        self.backend.recovery_succeeded();
        Ok(())
    }
}

fn media_audio_error(error: MediaError) -> AudioError {
    match error {
        MediaError::Unsupported(message) => {
            AudioError::Failed(format!("UNSUPPORTED_MEDIA: {message}"))
        }
        MediaError::OriginMismatch | MediaError::Authentication(_) => {
            AudioError::Failed(format!("MEDIA_AUTH_FAILED: {error}"))
        }
        MediaError::Truncated | MediaError::Cancelled | MediaError::Decode(_) => {
            AudioError::Failed(error.to_string())
        }
    }
}
