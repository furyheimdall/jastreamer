use jastreamer_renderer::audio::{
    AudioError, AudioTelemetry, FakePlaybackBackend, PlaybackBackend, PlaybackEvent, PlaybackState,
};
use jastreamer_renderer::engine::{Engine, EngineError, MediaLoader};
use jastreamer_renderer::harness::DurableJournal;
use jastreamer_renderer::media::{AudioStream, MediaError, PcmFormat, pcm_channel};
use jastreamer_renderer::protocol::{Command, MediaRepresentation};

struct Loader;
impl MediaLoader for Loader {
    fn open(
        &self,
        _media: &MediaRepresentation,
        position_ms: u64,
    ) -> Result<AudioStream, MediaError> {
        let (producer, stream) = pcm_channel(
            PcmFormat {
                channels: 2,
                sample_rate_hz: 48_000,
                start_position_ms: position_ms,
            },
            2,
        );
        producer.send(vec![0.0; 16]).expect("samples queue");
        producer.finish().expect("EOF queues");
        Ok(stream)
    }
}

struct RecordingLoader(std::sync::Arc<std::sync::Mutex<Vec<u64>>>);
impl MediaLoader for RecordingLoader {
    fn open(
        &self,
        _media: &MediaRepresentation,
        position_ms: u64,
    ) -> Result<AudioStream, MediaError> {
        self.0.lock().expect("positions lock").push(position_ms);
        Loader.open(_media, position_ms)
    }
}

struct EndpointBackend(AudioError);
impl PlaybackBackend for EndpointBackend {
    fn load(&mut self, _stream: AudioStream) -> Result<(), AudioError> {
        Err(std::mem::replace(&mut self.0, AudioError::NoEndpoint))
    }
    fn play(&mut self) -> Result<(), AudioError> {
        Ok(())
    }
    fn pause(&mut self) -> Result<(), AudioError> {
        Ok(())
    }
    fn resume(&mut self) -> Result<(), AudioError> {
        Ok(())
    }
    fn stop(&mut self) -> Result<(), AudioError> {
        Ok(())
    }
    fn seek(&mut self, _position_ms: u64) -> Result<(), AudioError> {
        Ok(())
    }
    fn state(&self) -> PlaybackState {
        PlaybackState::Idle
    }
    fn position_ms(&self) -> u64 {
        0
    }
    fn poll_event(&mut self) -> Option<PlaybackEvent> {
        None
    }
    fn telemetry(&self) -> AudioTelemetry {
        AudioTelemetry::default()
    }
}

fn command(kind: &str, sequence: u64) -> Command {
    Command {
        protocol_major: 3,
        frame_type: "command".to_owned(),
        command_id: format!("command-{sequence}"),
        sequence,
        session_epoch: "epoch".to_owned(),
        zone_id: "zone".to_owned(),
        play_id: Some("play".to_owned()),
        kind: kind.to_owned(),
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

#[test]
fn seek_restarts_decoder_at_requested_range_position() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let positions = std::sync::Arc::new(std::sync::Mutex::new(Vec::new()));
    let loader = RecordingLoader(std::sync::Arc::clone(&positions));
    let mut engine = Engine::new(journal, FakePlaybackBackend::default(), loader);
    engine.set_session_epoch("epoch");
    engine.execute(&command("play", 1)).expect("play succeeds");
    let mut seek = command("seek", 2);
    seek.position_ms = Some(1_234);

    // When
    engine.execute(&seek).expect("seek succeeds");

    // Then
    assert_eq!(*positions.lock().expect("positions lock"), [0, 1_234]);
}

#[test]
fn durable_accept_returns_before_command_execution() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, EndpointBackend(AudioError::BusyEndpoint), Loader);
    engine.set_session_epoch("epoch");
    let play = command("play", 1);

    // When
    let acceptance = engine.accept_command(&play).expect("command is accepted");

    // Then
    assert_eq!(acceptance.ack_status, "received");
    assert!(acceptance.command.is_some());
    assert!(acceptance.result.is_none());
    assert!(
        engine
            .journal()
            .result_for_command(&play.command_id)
            .is_none()
    );
}

#[test]
fn matching_redelivery_resumes_accepted_command_after_restart() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let play = command("play", 1);
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut first = Engine::new(journal, FakePlaybackBackend::default(), Loader);
    first.set_session_epoch("epoch");
    first.accept_command(&play).expect("command is durable");
    drop(first);
    let journal = DurableJournal::open(directory.path()).expect("journal reopens");
    let mut recovered = Engine::new(journal, FakePlaybackBackend::default(), Loader);
    recovered.set_session_epoch("epoch");

    // When
    let outcome = recovered.execute(&play).expect("redelivery resumes");

    // Then
    assert_eq!(outcome.ack_status, "duplicate");
    assert_eq!(outcome.result.status, "succeeded");
}

#[test]
fn unsupported_command_returns_protocol_failure_without_backend_side_effect() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, EndpointBackend(AudioError::BusyEndpoint), Loader);
    engine.set_session_epoch("epoch");

    // When
    let outcome = engine
        .execute(&command("future-command", 1))
        .expect("terminal result");

    // Then
    assert_eq!(
        outcome.result.error.expect("typed failure").code,
        "UNSUPPORTED_COMMAND"
    );
}

#[test]
fn credential_headers_are_rejected_before_command_is_journaled() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, FakePlaybackBackend::default(), Loader);
    engine.set_session_epoch("epoch");
    let mut play = command("play", 1);
    play.media
        .as_mut()
        .expect("play media")
        .headers
        .insert("Authorization".to_owned(), "secret-sentinel".to_owned());

    // When
    let result = engine.accept_command(&play);

    // Then
    assert!(matches!(result, Err(EngineError::Invalid(_))));
    assert_eq!(engine.journal().last_server_sequence(), 0);
}

#[test]
fn sequence_gap_is_rejected_before_command_is_journaled() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, EndpointBackend(AudioError::NoEndpoint), Loader);
    engine.set_session_epoch("epoch");

    // When
    let result = engine.execute(&command("play", 2));

    // Then
    assert!(matches!(
        result,
        Err(EngineError::SequenceGap {
            expected: 1,
            received: 2
        })
    ));
    assert_eq!(engine.journal().last_server_sequence(), 0);
}

#[test]
fn busy_endpoint_is_a_terminal_output_failure() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, EndpointBackend(AudioError::BusyEndpoint), Loader);
    engine.set_session_epoch("epoch");

    // When
    let outcome = engine
        .execute(&command("play", 1))
        .expect("terminal result");

    // Then
    assert_eq!(
        outcome.result.error.expect("typed failure").code,
        "OUTPUT_UNAVAILABLE"
    );
}

#[test]
fn removed_endpoint_is_a_retryable_output_failure() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, EndpointBackend(AudioError::Invalidated), Loader);
    engine.set_session_epoch("epoch");

    // When
    let outcome = engine
        .execute(&command("play", 1))
        .expect("terminal result");

    // Then
    let error = outcome.result.error.expect("typed failure");
    assert_eq!(error.code, "OUTPUT_UNAVAILABLE");
    assert!(error.retryable);
}

#[test]
fn unavailable_endpoint_is_a_terminal_output_failure() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, EndpointBackend(AudioError::NoEndpoint), Loader);
    engine.set_session_epoch("epoch");

    // When
    let outcome = engine
        .execute(&command("play", 1))
        .expect("terminal result");

    // Then
    assert_eq!(
        outcome.result.error.expect("typed failure").code,
        "OUTPUT_UNAVAILABLE"
    );
}
