use jastreamer_renderer::audio::{BufferEvent, PcmBuffer};
use jastreamer_renderer::media::{MediaError, PcmFormat, StreamItem, pcm_channel};
use std::sync::mpsc::{TryRecvError, sync_channel};
use std::thread;
use std::time::Duration;

fn format() -> PcmFormat {
    PcmFormat {
        channels: 2,
        sample_rate_hz: 48_000,
        start_position_ms: 0,
    }
}

#[test]
fn bounded_pcm_channel_applies_backpressure_at_capacity() {
    // Given
    let (producer, mut stream) = pcm_channel(format(), 2);
    let (attempted_tx, attempted_rx) = sync_channel(1);
    let (done_tx, done_rx) = sync_channel(1);
    let worker = thread::spawn(move || {
        producer.send(vec![1.0]).expect("first chunk");
        producer.send(vec![2.0]).expect("second chunk");
        attempted_tx.send(()).expect("attempt signal");
        let result = producer.send(vec![3.0]);
        done_tx.send(result).expect("done signal");
    });
    attempted_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("third send attempted");

    // When
    let blocked = done_rx.try_recv();
    let first = stream
        .recv_timeout(Duration::from_secs(1))
        .expect("first buffered chunk");
    let completion = done_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("third send completes");

    // Then
    assert!(matches!(blocked, Err(TryRecvError::Empty)));
    assert!(matches!(first, StreamItem::Samples(_)));
    assert!(completion.is_ok());
    worker.join().expect("producer joins");
}

#[test]
fn dropping_stream_cancels_blocked_producer_without_orphan() {
    // Given
    let (producer, stream) = pcm_channel(format(), 1);
    let (blocked_tx, blocked_rx) = sync_channel(1);
    let (done_tx, done_rx) = sync_channel(1);
    let worker = thread::spawn(move || {
        producer.send(vec![1.0]).expect("buffer fills");
        blocked_tx.send(()).expect("blocked signal");
        let result = producer.send(vec![2.0]);
        done_tx.send(result).expect("done signal");
    });
    blocked_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("producer reached blocked send");

    // When
    drop(stream);
    let result = done_rx
        .recv_timeout(Duration::from_secs(1))
        .expect("producer released");

    // Then
    assert!(result.is_err());
    worker.join().expect("producer joins");
}

#[test]
fn underrun_is_reported_while_decoder_is_open_and_buffer_empty() {
    // Given
    let (_producer, stream) = pcm_channel(format(), 2);
    let mut buffer = PcmBuffer::new(stream);
    let mut output = [1.0; 8];

    // When
    let event = buffer.fill(&mut output);

    // Then
    assert_eq!(event, BufferEvent::Underrun);
    assert_eq!(output, [0.0; 8]);
}

#[test]
fn natural_end_requires_decoder_eof_then_endpoint_drain_callback() {
    // Given
    let (producer, stream) = pcm_channel(format(), 2);
    producer.send(vec![0.5; 4]).expect("samples queued");
    producer.finish().expect("EOF queued");
    let mut buffer = PcmBuffer::new(stream);
    let mut output = [0.0; 4];

    // When
    let data = buffer.fill(&mut output);
    let drain = buffer.fill(&mut output);
    let ended = buffer.fill(&mut output);

    // Then
    assert_eq!(data, BufferEvent::Frames(2));
    assert_eq!(drain, BufferEvent::Draining);
    assert_eq!(ended, BufferEvent::Ended);
}

#[test]
fn truncated_decoder_stream_is_failure_not_eof() {
    // Given
    let (producer, stream) = pcm_channel(format(), 2);
    producer
        .fail(MediaError::Truncated)
        .expect("failure queued");
    let mut buffer = PcmBuffer::new(stream);
    let mut output = [0.0; 4];

    // When
    let event = buffer.fill(&mut output);

    // Then
    assert!(matches!(event, BufferEvent::Failed(_)));
}
