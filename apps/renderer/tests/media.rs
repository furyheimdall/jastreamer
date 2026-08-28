use jastreamer_renderer::media::{AudioStream, MediaError, StreamItem, decode_media_source};
use std::fs;
use std::path::PathBuf;

fn fixture(name: &str) -> Vec<u8> {
    fs::read(
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fixtures")
            .join(name),
    )
    .expect("media fixture reads")
}

fn open(encoded: Vec<u8>, mime_type: &str) -> Result<AudioStream, MediaError> {
    decode_media_source(Box::new(std::io::Cursor::new(encoded)), mime_type, 0)
}

fn collect(mut stream: AudioStream) -> Vec<f32> {
    let mut samples = Vec::new();
    loop {
        match stream
            .recv_timeout(std::time::Duration::from_secs(1))
            .expect("decoder message")
        {
            StreamItem::Samples(chunk) => samples.extend(chunk),
            StreamItem::Eof => return samples,
            StreamItem::Failed(error) => panic!("decoder failed: {error}"),
        }
    }
}

fn mono_wav() -> Vec<u8> {
    let samples = [0_i16, 1000, -1000, 0];
    let data_size = (samples.len() * 2) as u32;
    let mut bytes = Vec::new();
    bytes.extend_from_slice(b"RIFF");
    bytes.extend_from_slice(&(36 + data_size).to_le_bytes());
    bytes.extend_from_slice(b"WAVEfmt ");
    bytes.extend_from_slice(&16_u32.to_le_bytes());
    bytes.extend_from_slice(&1_u16.to_le_bytes());
    bytes.extend_from_slice(&1_u16.to_le_bytes());
    bytes.extend_from_slice(&8_000_u32.to_le_bytes());
    bytes.extend_from_slice(&16_000_u32.to_le_bytes());
    bytes.extend_from_slice(&2_u16.to_le_bytes());
    bytes.extend_from_slice(&16_u16.to_le_bytes());
    bytes.extend_from_slice(b"data");
    bytes.extend_from_slice(&data_size.to_le_bytes());
    for sample in samples {
        bytes.extend_from_slice(&sample.to_le_bytes());
    }
    bytes
}

#[test]
fn wav_decoder_returns_interleaved_pcm() {
    // Given
    let encoded = mono_wav();

    // When
    let stream = open(encoded, "audio/wav").expect("WAV opens");
    let format = stream.format();
    let samples = collect(stream);

    // Then
    assert_eq!(format.channels, 1);
    assert_eq!(format.sample_rate_hz, 8_000);
    assert_eq!(samples.len(), 4);
}

#[test]
fn flac_decoder_returns_pcm() {
    // Given / When
    let stream = open(fixture("tone.flac"), "audio/flac").expect("FLAC opens");
    let format = stream.format();
    let samples = collect(stream);

    // Then
    assert_eq!(format.channels, 2);
    assert!(!samples.is_empty());
}

#[test]
fn mp3_decoder_returns_pcm() {
    // Given / When
    let stream = open(fixture("tone.mp3"), "audio/mpeg").expect("MP3 opens");
    let format = stream.format();
    let samples = collect(stream);

    // Then
    assert_eq!(format.channels, 2);
    assert!(!samples.is_empty());
}

#[test]
fn vorbis_decoder_returns_pcm() {
    // Given / When
    let stream = open(fixture("tone-vorbis.ogg"), "audio/ogg").expect("Vorbis opens");
    let format = stream.format();
    let samples = collect(stream);

    // Then
    assert_eq!(format.channels, 2);
    assert!(!samples.is_empty());
}

#[test]
fn opus_decoder_returns_pcm() {
    // Given / When
    let stream = open(fixture("tone-opus.ogg"), "audio/opus").expect("Opus opens");
    let format = stream.format();
    let samples = collect(stream);

    // Then
    assert_eq!(format.channels, 2);
    assert!(!samples.is_empty());
}

#[test]
fn truncated_media_never_becomes_fake_completion() {
    // Given / When
    let result = open(b"RIFF\x10\0\0\0WAVE".to_vec(), "audio/wav");

    // Then
    assert!(matches!(
        result,
        Err(MediaError::Truncated) | Err(MediaError::Decode(_))
    ));
}

#[test]
fn unsupported_media_is_rejected_before_decode() {
    // Given / When
    let result = open(b"not audio".to_vec(), "audio/aac-future");

    // Then
    assert!(matches!(result, Err(MediaError::Unsupported(_))));
}
