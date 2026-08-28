use super::{accept, recover};
use crate::audio::FakePlaybackBackend;
use crate::engine::Engine;
use crate::harness::DurableJournal;
use crate::media::{AudioStream, MediaError, MediaLoader, PcmFormat, pcm_channel};
use crate::protocol::{Command, MediaRepresentation};
use futures_util::Sink;
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use tokio_tungstenite::tungstenite::{Error as WebSocketError, protocol::Message};

struct Loader;

impl MediaLoader for Loader {
    fn open(
        &self,
        _media: &MediaRepresentation,
        position_ms: u64,
    ) -> Result<AudioStream, MediaError> {
        let (_, stream) = pcm_channel(
            PcmFormat {
                channels: 2,
                sample_rate_hz: 48_000,
                start_position_ms: position_ms,
            },
            1,
        );
        Ok(stream)
    }
}

#[derive(Default)]
struct RecordingSink(Vec<Message>);

impl Sink<Message> for RecordingSink {
    type Error = WebSocketError;

    fn poll_ready(
        self: Pin<&mut Self>,
        _context: &mut Context<'_>,
    ) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn start_send(self: Pin<&mut Self>, item: Message) -> Result<(), Self::Error> {
        self.get_mut().0.push(item);
        Ok(())
    }

    fn poll_flush(
        self: Pin<&mut Self>,
        _context: &mut Context<'_>,
    ) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn poll_close(
        self: Pin<&mut Self>,
        _context: &mut Context<'_>,
    ) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }
}

#[tokio::test]
async fn durable_accept_emits_ack_before_work_is_taken_for_execution() {
    let directory = tempfile::tempdir().expect("state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut value = Engine::new(journal, FakePlaybackBackend::default(), Loader);
    value.set_session_epoch("epoch");
    let engine = Arc::new(Mutex::new(value));
    let (sender, mut receiver) = tokio::sync::mpsc::channel(1);
    let mut sink = RecordingSink::default();
    let command = command();

    accept(&mut sink, &engine, &sender, command.clone())
        .await
        .expect("command handling succeeds");

    assert_eq!(frame_type(&sink.0[0]), "command.ack");
    assert_eq!(frame_status(&sink.0[0]), "received");
    assert!(
        engine
            .lock()
            .expect("engine lock")
            .journal()
            .result_for_command(&command.command_id)
            .is_none()
    );
    assert_eq!(
        receiver
            .try_recv()
            .expect("accepted work queued")
            .command_id,
        command.command_id
    );
}

#[tokio::test]
async fn reconnect_acknowledges_and_claims_unfinished_work_once() {
    let directory = tempfile::tempdir().expect("state directory");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut value = Engine::new(journal, FakePlaybackBackend::default(), Loader);
    value.set_session_epoch("epoch");
    let command = command();
    value
        .accept_command(&command)
        .expect("command is durable before reconnect");
    let engine = Arc::new(Mutex::new(value));
    let (sender, mut receiver) = tokio::sync::mpsc::channel(2);
    let mut sink = RecordingSink::default();

    recover(&mut sink, &engine, &sender)
        .await
        .expect("recovery queues work");
    recover(&mut sink, &engine, &sender)
        .await
        .expect("second recovery is idempotent");

    assert_eq!(sink.0.len(), 1);
    assert_eq!(frame_status(&sink.0[0]), "duplicate");
    assert_eq!(
        receiver
            .try_recv()
            .expect("recovered work queued")
            .command_id,
        command.command_id
    );
    assert!(receiver.try_recv().is_err());
}

fn frame_type(message: &Message) -> String {
    frame_value(message, "type")
}

fn frame_status(message: &Message) -> String {
    frame_value(message, "status")
}

fn frame_value(message: &Message, key: &str) -> String {
    let Message::Text(text) = message else {
        panic!("expected text frame");
    };
    let value: serde_json::Value = serde_json::from_str(text).expect("frame parses");
    value[key].as_str().expect("frame field").to_owned()
}

fn command() -> Command {
    Command {
        protocol_major: 3,
        frame_type: "command".to_owned(),
        command_id: "command-1".to_owned(),
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
