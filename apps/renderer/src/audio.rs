use crate::media::AudioStream;

pub use crate::audio_buffer::{BufferEvent, PcmBuffer};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PlaybackState {
    Idle,
    Playing,
    Paused,
    Stopped,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PlaybackEvent {
    Ended { position_ms: u64 },
    OutputInvalidated,
    Failed { message: String },
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct AudioTelemetry {
    pub underruns: u64,
    pub frames_written: u64,
    pub recoveries: u64,
}

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum AudioError {
    #[error("OUTPUT_UNAVAILABLE: no matching output endpoint")]
    NoEndpoint,
    #[error("OUTPUT_UNAVAILABLE: output endpoint is busy")]
    BusyEndpoint,
    #[error("OUTPUT_UNAVAILABLE: output endpoint was removed or invalidated")]
    Invalidated,
    #[error("PLAYBACK_FAILED: decoded stream format is unsupported by endpoint")]
    UnsupportedFormat,
    #[error("PLAYBACK_FAILED: first frame did not enter the running audio clock")]
    StartTimeout,
    #[error("PLAYBACK_FAILED: {0}")]
    Failed(String),
}

pub trait PlaybackBackend {
    fn load(&mut self, stream: AudioStream) -> Result<(), AudioError>;
    fn play(&mut self) -> Result<(), AudioError>;
    fn pause(&mut self) -> Result<(), AudioError>;
    fn resume(&mut self) -> Result<(), AudioError>;
    fn stop(&mut self) -> Result<(), AudioError>;
    fn seek(&mut self, position_ms: u64) -> Result<(), AudioError>;
    fn state(&self) -> PlaybackState;
    fn position_ms(&self) -> u64;
    fn poll_event(&mut self) -> Option<PlaybackEvent>;
    fn telemetry(&self) -> AudioTelemetry;
    fn recovery_succeeded(&mut self) {}
}

#[derive(Default)]
pub struct FakePlaybackBackend {
    stream: Option<AudioStream>,
    state: Option<PlaybackState>,
    position_ms: u64,
    events: std::collections::VecDeque<PlaybackEvent>,
}

impl FakePlaybackBackend {
    pub fn finish(&mut self) {
        self.events.push_back(PlaybackEvent::Ended {
            position_ms: self.position_ms,
        });
        self.state = Some(PlaybackState::Idle);
    }
}

impl PlaybackBackend for FakePlaybackBackend {
    fn load(&mut self, stream: AudioStream) -> Result<(), AudioError> {
        self.stream = Some(stream);
        self.position_ms = 0;
        self.state = Some(PlaybackState::Stopped);
        Ok(())
    }

    fn play(&mut self) -> Result<(), AudioError> {
        if self.stream.is_none() {
            return Err(AudioError::Failed("no decoded audio loaded".to_owned()));
        }
        self.state = Some(PlaybackState::Playing);
        Ok(())
    }

    fn pause(&mut self) -> Result<(), AudioError> {
        if self.state != Some(PlaybackState::Playing) {
            return Err(AudioError::Failed("playback is not running".to_owned()));
        }
        self.state = Some(PlaybackState::Paused);
        Ok(())
    }

    fn resume(&mut self) -> Result<(), AudioError> {
        if self.state != Some(PlaybackState::Paused) {
            return Err(AudioError::Failed("playback is not paused".to_owned()));
        }
        self.state = Some(PlaybackState::Playing);
        Ok(())
    }

    fn stop(&mut self) -> Result<(), AudioError> {
        self.state = Some(PlaybackState::Stopped);
        self.position_ms = 0;
        Ok(())
    }

    fn seek(&mut self, position_ms: u64) -> Result<(), AudioError> {
        if self.stream.is_none() {
            return Err(AudioError::Failed("no decoded audio loaded".to_owned()));
        }
        self.position_ms = position_ms;
        Ok(())
    }

    fn state(&self) -> PlaybackState {
        self.state.unwrap_or(PlaybackState::Idle)
    }

    fn position_ms(&self) -> u64 {
        self.position_ms
    }

    fn poll_event(&mut self) -> Option<PlaybackEvent> {
        self.events.pop_front()
    }

    fn telemetry(&self) -> AudioTelemetry {
        AudioTelemetry::default()
    }
}
