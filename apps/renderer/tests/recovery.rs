use jastreamer_renderer::audio::{
    AudioError, AudioTelemetry, PlaybackBackend, PlaybackEvent, PlaybackState,
};
use jastreamer_renderer::engine::{Engine, MediaLoader};
use jastreamer_renderer::harness::DurableJournal;
use jastreamer_renderer::media::{AudioStream, MediaError, PcmFormat, pcm_channel};
use jastreamer_renderer::protocol::{Command, MediaRepresentation};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

struct Loader(Arc<Mutex<Vec<u64>>>);
impl MediaLoader for Loader {
    fn open(
        &self,
        _media: &MediaRepresentation,
        position_ms: u64,
    ) -> Result<AudioStream, MediaError> {
        self.0.lock().expect("positions lock").push(position_ms);
        let (producer, stream) = pcm_channel(
            PcmFormat {
                channels: 2,
                sample_rate_hz: 48_000,
                start_position_ms: position_ms,
            },
            2,
        );
        producer.send(vec![0.0; 8]).expect("PCM queues");
        producer.finish().expect("EOF queues");
        Ok(stream)
    }
}

struct Backend {
    state: PlaybackState,
    invalidation: Arc<AtomicBool>,
    recoveries: Arc<Mutex<u64>>,
}

impl PlaybackBackend for Backend {
    fn load(&mut self, _stream: AudioStream) -> Result<(), AudioError> {
        self.state = PlaybackState::Stopped;
        Ok(())
    }
    fn play(&mut self) -> Result<(), AudioError> {
        self.state = PlaybackState::Playing;
        Ok(())
    }
    fn pause(&mut self) -> Result<(), AudioError> {
        self.state = PlaybackState::Paused;
        Ok(())
    }
    fn resume(&mut self) -> Result<(), AudioError> {
        self.play()
    }
    fn stop(&mut self) -> Result<(), AudioError> {
        self.state = PlaybackState::Stopped;
        Ok(())
    }
    fn seek(&mut self, _position_ms: u64) -> Result<(), AudioError> {
        Ok(())
    }
    fn state(&self) -> PlaybackState {
        self.state
    }
    fn position_ms(&self) -> u64 {
        500
    }
    fn poll_event(&mut self) -> Option<PlaybackEvent> {
        if self.invalidation.swap(false, Ordering::AcqRel) {
            Some(PlaybackEvent::OutputInvalidated)
        } else {
            None
        }
    }
    fn telemetry(&self) -> AudioTelemetry {
        AudioTelemetry::default()
    }
    fn recovery_succeeded(&mut self) {
        *self.recoveries.lock().expect("recoveries lock") += 1;
    }
}

#[test]
fn invalidation_restarts_range_decoder_at_observed_position() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let positions = Arc::new(Mutex::new(Vec::new()));
    let recoveries = Arc::new(Mutex::new(0));
    let invalidation = Arc::new(AtomicBool::new(false));
    let backend = Backend {
        state: PlaybackState::Idle,
        invalidation: Arc::clone(&invalidation),
        recoveries: Arc::clone(&recoveries),
    };
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, backend, Loader(Arc::clone(&positions)));
    engine.set_session_epoch("epoch");
    engine.execute(&play_command()).expect("play succeeds");
    invalidation.store(true, Ordering::Release);

    // When
    let event = engine.poll_playback_event();

    // Then
    assert!(event.is_none());
    assert_eq!(*positions.lock().expect("positions lock"), [0, 500]);
    assert_eq!(*recoveries.lock().expect("recoveries lock"), 1);
}

fn play_command() -> Command {
    Command {
        protocol_major: 3,
        frame_type: "command".to_owned(),
        command_id: "play-command".to_owned(),
        sequence: 1,
        session_epoch: "epoch".to_owned(),
        zone_id: "zone".to_owned(),
        play_id: Some("play".to_owned()),
        kind: "play".to_owned(),
        deadline: "2099-01-01T00:00:00Z".to_owned(),
        position_ms: None,
        media: Some(MediaRepresentation {
            url: "https://server/media".to_owned(),
            mime_type: "audio/flac".to_owned(),
            headers: Default::default(),
            seekable: true,
        }),
    }
}
