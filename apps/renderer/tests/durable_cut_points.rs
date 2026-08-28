use jastreamer_renderer::audio::{
    AudioError, AudioTelemetry, PlaybackBackend, PlaybackEvent, PlaybackState,
};
use jastreamer_renderer::engine::{Engine, MediaLoader};
use jastreamer_renderer::harness::{DurableJournal, JournalResult};
use jastreamer_renderer::media::{AudioStream, MediaError, PcmFormat, pcm_channel};
use jastreamer_renderer::protocol::{Command, MediaRepresentation};
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

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

struct CountingBackend {
    starts: Arc<AtomicUsize>,
    state: PlaybackState,
}

impl PlaybackBackend for CountingBackend {
    fn load(&mut self, _stream: AudioStream) -> Result<(), AudioError> {
        self.starts.fetch_add(1, Ordering::SeqCst);
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
        self.state = PlaybackState::Playing;
        Ok(())
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
        0
    }
    fn poll_event(&mut self) -> Option<PlaybackEvent> {
        None
    }
    fn telemetry(&self) -> AudioTelemetry {
        AudioTelemetry::default()
    }
}

#[test]
fn crash_before_accept_has_no_recoverable_work() {
    let directory = tempfile::tempdir().expect("state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");

    assert!(journal.unfinished_commands().is_empty());
    assert!(journal.pending_results().is_empty());
}

#[test]
fn crash_after_accept_before_ack_resumes_persisted_payload() {
    let directory = tempfile::tempdir().expect("state directory");
    let command = command();
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut first = engine(journal, Arc::new(AtomicUsize::new(0)));
    first.set_session_epoch("epoch");
    first
        .accept_command(&command)
        .expect("durable accept succeeds");
    drop(first);

    let starts = Arc::new(AtomicUsize::new(0));
    let journal = DurableJournal::open(directory.path()).expect("journal reopens");
    let recovered = journal.unfinished_commands();
    let mut engine = engine(journal, Arc::clone(&starts));
    engine.set_session_epoch("epoch");
    let result = engine
        .execute_accepted(&recovered[0])
        .expect("recovered execution succeeds");

    assert_eq!(recovered, [command]);
    assert_eq!(result.status, "succeeded");
    assert_eq!(starts.load(Ordering::SeqCst), 1);
}

#[test]
fn crash_after_ack_before_execute_resumes_without_redelivery() {
    let directory = tempfile::tempdir().expect("state directory");
    let command = command();
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut first = engine(journal, Arc::new(AtomicUsize::new(0)));
    first.set_session_epoch("epoch");
    let acknowledgement = first
        .accept_command(&command)
        .expect("ack-ready acceptance");
    assert_eq!(acknowledgement.ack_status, "received");
    drop(first);

    let starts = Arc::new(AtomicUsize::new(0));
    let journal = DurableJournal::open(directory.path()).expect("journal reopens");
    let recovered = journal.unfinished_commands();
    let mut second = engine(journal, Arc::clone(&starts));
    second.set_session_epoch("epoch");
    second
        .execute_accepted(&recovered[0])
        .expect("recovered execution succeeds");

    assert_eq!(starts.load(Ordering::SeqCst), 1);
}

#[test]
fn crash_after_result_persist_before_send_replays_result() {
    let directory = tempfile::tempdir().expect("state directory");
    let starts = Arc::new(AtomicUsize::new(0));
    let result = persist_result(directory.path(), Arc::clone(&starts));

    let reopened = DurableJournal::open(directory.path()).expect("journal reopens");

    assert_eq!(reopened.pending_results(), [result]);
    assert_eq!(starts.load(Ordering::SeqCst), 1);
}

#[test]
fn crash_after_send_before_result_ack_replays_same_result() {
    let directory = tempfile::tempdir().expect("state directory");
    let starts = Arc::new(AtomicUsize::new(0));
    let sent = persist_result(directory.path(), Arc::clone(&starts));
    let reopened = DurableJournal::open(directory.path()).expect("journal reopens");

    assert_eq!(reopened.pending_results(), [sent]);
    assert_eq!(starts.load(Ordering::SeqCst), 1);
}

#[test]
fn durable_result_ack_stops_replay_without_reexecuting_duplicate() {
    let directory = tempfile::tempdir().expect("state directory");
    let starts = Arc::new(AtomicUsize::new(0));
    let result = persist_result(directory.path(), Arc::clone(&starts));
    let mut journal = DurableJournal::open(directory.path()).expect("journal reopens");
    journal
        .acknowledge_result(&result.result_id)
        .expect("result ack persists");
    drop(journal);

    let journal = DurableJournal::open(directory.path()).expect("journal reopens after ack");
    assert!(journal.pending_results().is_empty());
    assert!(journal.unfinished_commands().is_empty());
    let mut final_engine = engine(journal, Arc::clone(&starts));
    final_engine.set_session_epoch("epoch");
    let duplicate = final_engine
        .execute(&command())
        .expect("acknowledged duplicate reconciles");

    assert_eq!(duplicate.result, result);
    assert_eq!(starts.load(Ordering::SeqCst), 1);
}

fn persist_result(path: &std::path::Path, starts: Arc<AtomicUsize>) -> JournalResult {
    let journal = DurableJournal::open(path).expect("journal opens");
    let mut value = engine(journal, starts);
    value.set_session_epoch("epoch");
    value
        .execute(&command())
        .expect("execution succeeds")
        .result
}

fn engine(journal: DurableJournal, starts: Arc<AtomicUsize>) -> Engine<CountingBackend, Loader> {
    Engine::new(
        journal,
        CountingBackend {
            starts,
            state: PlaybackState::Idle,
        },
        Loader,
    )
}

fn command() -> Command {
    Command {
        protocol_major: 3,
        frame_type: "command".to_owned(),
        command_id: "durable-command".to_owned(),
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
