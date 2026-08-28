use jastreamer_renderer::harness::SecretToken;
use jastreamer_renderer::media::{RangeMediaSource, StreamItem, decode_media_source};
use std::io::{Read, Seek, SeekFrom, Write};
use std::net::TcpListener;
use std::sync::mpsc::{SyncSender, sync_channel};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::Duration;

struct Gate {
    released: Mutex<bool>,
    changed: Condvar,
    blocked: Mutex<Option<SyncSender<()>>>,
}

struct GateRelease(Arc<Gate>);
impl Drop for GateRelease {
    fn drop(&mut self) {
        if let Ok(mut released) = self.0.released.lock() {
            *released = true;
            self.0.changed.notify_all();
        }
    }
}

struct GateSource {
    bytes: Vec<u8>,
    position: usize,
    total: u64,
    gate: Arc<Gate>,
}

impl Read for GateSource {
    fn read(&mut self, output: &mut [u8]) -> std::io::Result<usize> {
        if self.position < self.bytes.len() {
            let count = output.len().min(self.bytes.len() - self.position);
            output[..count].copy_from_slice(&self.bytes[self.position..self.position + count]);
            self.position += count;
            return Ok(count);
        }
        if let Some(blocked) = self
            .gate
            .blocked
            .lock()
            .map_err(|_| std::io::Error::other("blocked lock poisoned"))?
            .take()
        {
            let _ = blocked.send(());
        }
        let mut released = self
            .gate
            .released
            .lock()
            .map_err(|_| std::io::Error::other("release lock poisoned"))?;
        while !*released {
            released = self
                .gate
                .changed
                .wait(released)
                .map_err(|_| std::io::Error::other("release wait poisoned"))?;
        }
        Ok(0)
    }
}

impl Seek for GateSource {
    fn seek(&mut self, position: SeekFrom) -> std::io::Result<u64> {
        let target = match position {
            SeekFrom::Start(value) => value,
            SeekFrom::Current(delta) => (self.position as u64)
                .checked_add_signed(delta)
                .ok_or_else(|| std::io::Error::other("seek overflow"))?,
            SeekFrom::End(delta) => self
                .total
                .checked_add_signed(delta)
                .ok_or_else(|| std::io::Error::other("seek overflow"))?,
        };
        self.position =
            usize::try_from(target).map_err(|_| std::io::Error::other("seek overflow"))?;
        Ok(target)
    }
}

impl symphonia::core::io::MediaSource for GateSource {
    fn is_seekable(&self) -> bool {
        true
    }
    fn byte_len(&self) -> Option<u64> {
        Some(self.total)
    }
}

#[test]
fn streaming_decode_produces_pcm_before_source_body_completes() {
    // Given
    let (blocked_tx, blocked_rx) = sync_channel(1);
    let gate = Arc::new(Gate {
        released: Mutex::new(false),
        changed: Condvar::new(),
        blocked: Mutex::new(Some(blocked_tx)),
    });
    let mut encoded = std::fs::read(
        std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/tone.mp3"),
    )
    .expect("MP3 fixture reads");
    encoded.resize(128 * 1024, 0);
    let source = GateSource {
        bytes: encoded,
        position: 0,
        total: 256 * 1024,
        gate: Arc::clone(&gate),
    };
    let release = GateRelease(Arc::clone(&gate));

    // When
    let mut stream = decode_media_source(Box::new(source), "audio/mpeg", 0).expect("stream opens");
    let first = stream.recv_timeout(Duration::from_secs(1));
    let body_incomplete = !*gate.released.lock().expect("release lock");
    drop(release);

    // Then
    assert!(matches!(
        first.expect("PCM arrives"),
        StreamItem::Samples(_)
    ));
    assert!(body_incomplete);
    let _ = blocked_rx.recv_timeout(Duration::from_secs(1));
}

#[test]
fn range_source_seek_restarts_request_with_same_bearer() {
    // Given
    let listener = TcpListener::bind("127.0.0.1:0").expect("listener binds");
    let address = listener.local_addr().expect("listener address");
    let (requests_tx, requests_rx) = sync_channel(2);
    let server = thread::spawn(move || {
        for index in 0..2 {
            let (mut socket, _) = listener.accept().expect("request accepted");
            let request = read_request(&mut socket);
            requests_tx.send(request).expect("request logged");
            let (range, body) = if index == 0 {
                ("bytes 0-9/10", b"0123456789".as_slice())
            } else {
                ("bytes 5-9/10", b"56789".as_slice())
            };
            write!(socket, "HTTP/1.1 206 Partial Content\r\nContent-Range: {range}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n", body.len()).expect("response headers");
            socket.write_all(body).expect("response body");
        }
    });
    let mut source = range_source(address);

    // When
    source
        .seek(SeekFrom::Start(5))
        .expect("seek restarts Range");
    let first = requests_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("first request");
    let second = requests_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("second request");

    // Then
    assert!(first.contains("range: bytes=0-"));
    assert!(second.contains("range: bytes=5-"));
    assert!(first.contains("authorization: Bearer range-bearer"));
    assert!(second.contains("authorization: Bearer range-bearer"));
    server.join().expect("server joins");
}

#[test]
fn range_source_continues_with_fresh_request_after_bounded_response() {
    // Given
    let listener = TcpListener::bind("127.0.0.1:0").expect("listener binds");
    let address = listener.local_addr().expect("listener address");
    let (requests_tx, requests_rx) = sync_channel(2);
    let server = thread::spawn(move || {
        for (range, body) in [("bytes 0-2/6", b"abc"), ("bytes 3-5/6", b"def")] {
            let (mut socket, _) = listener.accept().expect("request accepted");
            requests_tx
                .send(read_request(&mut socket))
                .expect("request logged");
            write!(socket, "HTTP/1.1 206 Partial Content\r\nContent-Range: {range}\r\nContent-Length: 3\r\nConnection: close\r\n\r\n").expect("response headers");
            socket.write_all(body).expect("response body");
        }
    });
    let mut source = range_source(address);
    let mut body = Vec::new();

    // When
    source.read_to_end(&mut body).expect("all ranges read");

    // Then
    assert_eq!(body, b"abcdef");
    assert!(
        requests_rx
            .recv_timeout(Duration::from_secs(1))
            .expect("first request")
            .contains("range: bytes=0-")
    );
    assert!(
        requests_rx
            .recv_timeout(Duration::from_secs(1))
            .expect("second request")
            .contains("range: bytes=3-")
    );
    server.join().expect("server joins");
}

#[test]
fn truncated_range_body_returns_unexpected_eof() {
    // Given
    let listener = TcpListener::bind("127.0.0.1:0").expect("listener binds");
    let address = listener.local_addr().expect("listener address");
    let server = thread::spawn(move || {
        let (mut socket, _) = listener.accept().expect("request accepted");
        let _ = read_request(&mut socket);
        write!(socket, "HTTP/1.1 206 Partial Content\r\nContent-Range: bytes 0-9/10\r\nContent-Length: 5\r\nConnection: close\r\n\r\nshort").expect("response writes");
    });
    let mut source = range_source(address);
    let mut body = Vec::new();

    // When
    let result = source.read_to_end(&mut body);

    // Then
    assert_eq!(
        result.expect_err("truncation fails").kind(),
        std::io::ErrorKind::UnexpectedEof
    );
    server.join().expect("server joins");
}

fn read_request(socket: &mut std::net::TcpStream) -> String {
    let mut request = Vec::new();
    let mut byte = [0_u8; 1];
    while !request.ends_with(b"\r\n\r\n") {
        socket.read_exact(&mut byte).expect("request byte");
        request.push(byte[0]);
    }
    String::from_utf8(request).expect("request UTF-8")
}

fn range_source(address: std::net::SocketAddr) -> RangeMediaSource {
    let agent = ureq::Agent::new_with_defaults();
    let url = url::Url::parse(&format!("http://{address}/media")).expect("URL parses");
    let token = SecretToken::parse("range-bearer").expect("token parses");
    RangeMediaSource::open_untracked(agent, url, token).expect("source opens")
}
