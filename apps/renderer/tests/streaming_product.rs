use jastreamer_renderer::audio::{
    AudioError, AudioTelemetry, BufferEvent, PcmBuffer, PlaybackBackend, PlaybackEvent,
    PlaybackState,
};
use jastreamer_renderer::engine::Engine;
use jastreamer_renderer::harness::{DurableJournal, SecretToken};
use jastreamer_renderer::media::{AudioStream, PinnedHttpMediaLoader};
use jastreamer_renderer::protocol::{Command, MediaRepresentation};
use rustls::pki_types::{PrivateKeyDer, PrivatePkcs8KeyDer};
use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::mpsc::{SyncSender, TryRecvError, sync_channel};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

struct ProbeState {
    queue_was_full: bool,
}

struct CallbackBackend {
    stream: Option<AudioStream>,
    buffer: Option<PcmBuffer>,
    state: PlaybackState,
    callback_ready: SyncSender<()>,
    callback_release: std::sync::mpsc::Receiver<()>,
    started: SyncSender<()>,
    probe: Arc<Mutex<ProbeState>>,
}

impl PlaybackBackend for CallbackBackend {
    fn load(&mut self, stream: AudioStream) -> Result<(), AudioError> {
        self.stream = Some(stream);
        self.state = PlaybackState::Stopped;
        Ok(())
    }

    fn play(&mut self) -> Result<(), AudioError> {
        let stream = self
            .stream
            .take()
            .ok_or_else(|| AudioError::Failed("stream was not loaded".to_owned()))?;
        let queue_was_full = stream.wait_until_full(Duration::from_secs(1));
        self.probe.lock().expect("probe lock").queue_was_full = queue_was_full;
        self.callback_ready
            .send(())
            .map_err(|_| AudioError::Failed("callback observer closed".to_owned()))?;
        self.callback_release
            .recv()
            .map_err(|_| AudioError::Failed("callback release closed".to_owned()))?;
        let mut buffer = PcmBuffer::new(stream);
        let mut output = [0.0_f32; 512];
        if !matches!(buffer.fill(&mut output), BufferEvent::Frames(_)) {
            return Err(AudioError::StartTimeout);
        }
        self.buffer = Some(buffer);
        self.state = PlaybackState::Playing;
        self.started
            .send(())
            .map_err(|_| AudioError::Failed("start observer closed".to_owned()))
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
        self.stream = None;
        self.buffer = None;
        self.state = PlaybackState::Stopped;
        Ok(())
    }
    fn seek(&mut self, _position_ms: u64) -> Result<(), AudioError> {
        Err(AudioError::Failed(
            "engine must reopen seek stream".to_owned(),
        ))
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
fn production_stream_cut_is_recoverable_and_callback_starts_before_body_completes() {
    // Given
    let media = std::fs::read(
        std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fixtures/streaming-tone.mp3"),
    )
    .expect("streaming fixture reads");
    let certified = rcgen::generate_simple_self_signed(vec!["localhost".to_owned()])
        .expect("certificate generates");
    let certificate = certified.cert.der().clone();
    let key = PrivateKeyDer::Pkcs8(PrivatePkcs8KeyDer::from(
        certified.signing_key.serialize_der(),
    ));
    let server_config = rustls::ServerConfig::builder()
        .with_no_client_auth()
        .with_single_cert(vec![certificate.clone()], key)
        .expect("TLS server config");
    let listener = TcpListener::bind("127.0.0.1:0").expect("listener binds");
    let address = listener.local_addr().expect("listener address");
    let (initial_tx, initial_rx) = sync_channel(1);
    let (release_tx, release_rx) = sync_channel(1);
    let (body_done_tx, body_done_rx) = sync_channel(1);
    let server = thread::spawn(move || {
        let (socket, _) = listener.accept().expect("TLS client accepted");
        let connection =
            rustls::ServerConnection::new(Arc::new(server_config)).expect("TLS connection creates");
        let mut tls = rustls::StreamOwned::new(connection, socket);
        let request = read_request(&mut tls);
        assert!(request.contains("authorization: Bearer product-bearer"));
        assert!(request.contains("range: bytes=0-262143"));
        write!(tls, "HTTP/1.1 206 Partial Content\r\nContent-Range: bytes 0-{}/{}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n", media.len() - 1, media.len(), media.len()).expect("headers write");
        let split = 64 * 1024;
        tls.write_all(&media[..split]).expect("initial body writes");
        tls.flush().expect("initial body flushes");
        initial_tx.send(()).expect("initial body signal");
        release_rx.recv().expect("body release");
        let _ = tls.write_all(&media[split..]);
        let _ = tls.flush();
        body_done_tx.send(()).expect("body done signal");
    });
    let token = SecretToken::parse("product-bearer").expect("token parses");
    let origin =
        url::Url::parse(&format!("https://localhost:{}", address.port())).expect("origin parses");
    let loader = PinnedHttpMediaLoader::new(
        origin.clone(),
        token,
        Arc::new(Mutex::new(Some(certificate.as_ref().to_vec()))),
    );
    let (started_tx, started_rx) = sync_channel(1);
    let probe = Arc::new(Mutex::new(ProbeState {
        queue_was_full: false,
    }));
    let (callback_ready_tx, callback_ready_rx) = sync_channel(1);
    let (callback_release_tx, callback_release_rx) = sync_channel(1);
    let backend = CallbackBackend {
        stream: None,
        buffer: None,
        state: PlaybackState::Idle,
        callback_ready: callback_ready_tx,
        callback_release: callback_release_rx,
        started: started_tx,
        probe: Arc::clone(&probe),
    };
    let directory = tempfile::tempdir().expect("state directory");
    let journal_path = directory.path().join("journal.json");
    let journal = DurableJournal::open(directory.path()).expect("journal opens");
    let mut engine = Engine::new(journal, backend, loader);
    engine.set_session_epoch("epoch");
    let (completed_tx, completed_rx) = sync_channel(1);
    let command = play_command(&origin);
    let expected_recovery = command.clone();
    let renderer = thread::spawn(move || {
        let result = engine.execute(&command);
        completed_tx.send(result).expect("completion sends");
    });
    initial_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("initial body sent");
    callback_ready_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("decoder reaches callback boundary");
    let durable: serde_json::Value =
        serde_json::from_slice(&std::fs::read(&journal_path).expect("in-stream journal reads"))
            .expect("in-stream journal parses");
    assert!(durable["commands"].get("streaming-play").is_some());
    assert_eq!(durable["pendingResults"], serde_json::json!({}));
    assert_eq!(durable["completedResults"], serde_json::json!({}));
    let crash_state = tempfile::tempdir().expect("crash state directory");
    std::fs::copy(&journal_path, crash_state.path().join("journal.json"))
        .expect("in-stream journal snapshot copies");
    let recovered = DurableJournal::open(crash_state.path()).expect("crash snapshot reopens");
    assert_eq!(recovered.unfinished_commands(), [expected_recovery]);
    assert!(
        !std::fs::read_to_string(&journal_path)
            .expect("journal text reads")
            .contains("product-bearer")
    );

    // When
    callback_release_tx.send(()).expect("callback released");
    started_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("callback starts");
    let result = completed_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("play result before EOF");

    // Then
    assert!(result.is_ok());
    assert!(probe.lock().expect("probe lock").queue_was_full);
    assert!(matches!(body_done_rx.try_recv(), Err(TryRecvError::Empty)));
    release_tx.send(()).expect("remaining body released");
    body_done_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("body completes");
    renderer.join().expect("renderer joins");
    server.join().expect("server joins");
}

fn read_request(stream: &mut impl Read) -> String {
    let mut request = Vec::new();
    let mut byte = [0_u8; 1];
    while !request.ends_with(b"\r\n\r\n") {
        stream.read_exact(&mut byte).expect("request byte");
        request.push(byte[0]);
    }
    String::from_utf8(request).expect("request UTF-8")
}

fn play_command(origin: &url::Url) -> Command {
    Command {
        protocol_major: 3,
        frame_type: "command".to_owned(),
        command_id: "streaming-play".to_owned(),
        sequence: 1,
        session_epoch: "epoch".to_owned(),
        zone_id: "zone".to_owned(),
        play_id: Some("play".to_owned()),
        kind: "play".to_owned(),
        deadline: "2099-01-01T00:00:00Z".to_owned(),
        position_ms: None,
        media: Some(MediaRepresentation {
            url: origin.join("media").expect("media URL").to_string(),
            mime_type: "audio/mpeg".to_owned(),
            headers: Default::default(),
            seekable: true,
        }),
    }
}
