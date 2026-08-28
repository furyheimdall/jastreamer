use crate::media::{AudioStream, MediaError, PcmFormat, PcmProducer, pcm_channel};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use symphonia::core::audio::SampleBuffer;
use symphonia::core::codecs::{CODEC_TYPE_OPUS, DecoderOptions};
use symphonia::core::errors::Error as SymphoniaError;
use symphonia::core::formats::{FormatOptions, SeekMode, SeekTo};
use symphonia::core::io::{MediaSource, MediaSourceStream};
use symphonia::core::meta::MetadataOptions;
use symphonia::core::probe::Hint;
use symphonia::core::units::Time;

const PCM_CHANNEL_CAPACITY: usize = 8;
const PCM_CHUNK_SAMPLES: usize = 4096;

#[derive(Clone, Default)]
pub struct StreamIntegrity {
    complete: Arc<AtomicBool>,
}

impl StreamIntegrity {
    pub fn mark_complete(&self) {
        self.complete.store(true, Ordering::Release);
    }

    pub fn clear(&self) {
        self.complete.store(false, Ordering::Release);
    }

    pub(crate) fn is_complete_for_decoder(&self) -> bool {
        self.complete.load(Ordering::Acquire)
    }
}

pub struct DecodeRequest<'a> {
    pub source: Box<dyn MediaSource>,
    pub mime_type: &'a str,
    pub position_ms: u64,
    pub integrity: Option<StreamIntegrity>,
}

pub fn decode_source(request: DecodeRequest<'_>) -> Result<AudioStream, MediaError> {
    let mut hint = Hint::new();
    if let Some(extension) = crate::media::extension_for_mime(request.mime_type) {
        hint.with_extension(extension);
    }
    let stream = MediaSourceStream::new(request.source, Default::default());
    let mut format = symphonia::default::get_probe()
        .format(
            &hint,
            stream,
            &FormatOptions::default(),
            &MetadataOptions::default(),
        )
        .map_err(crate::media::classify_decode_error)?
        .format;
    let track = format
        .default_track()
        .ok_or_else(|| MediaError::Unsupported("media has no default audio track".to_owned()))?;
    let track_id = track.id;
    let channels = track
        .codec_params
        .channels
        .map(|value| value.count() as u16)
        .ok_or(MediaError::Truncated)?;
    let sample_rate_hz = track.codec_params.sample_rate.unwrap_or(48_000);
    let is_opus = track.codec_params.codec == CODEC_TYPE_OPUS;
    let delay = track.codec_params.delay.unwrap_or(0) as usize;
    let padding = track.codec_params.padding.unwrap_or(0) as usize;
    let mut decoder = if is_opus {
        None
    } else {
        Some(
            symphonia::default::get_codecs()
                .make(&track.codec_params, &DecoderOptions::default())
                .map_err(crate::media::classify_decode_error)?,
        )
    };
    if request.position_ms > 0 {
        let seconds = request.position_ms / 1000;
        let fraction = (request.position_ms % 1000) as f64 / 1000.0;
        format
            .seek(
                SeekMode::Accurate,
                SeekTo::Time {
                    time: Time::new(seconds, fraction),
                    track_id: Some(track_id),
                },
            )
            .map_err(crate::media::classify_decode_error)?;
        if let Some(value) = decoder.as_mut() {
            value.reset();
        }
    }
    let pcm_format = PcmFormat {
        channels,
        sample_rate_hz: if is_opus { 48_000 } else { sample_rate_hz },
        start_position_ms: request.position_ms,
    };
    let (producer, mut output) = pcm_channel(pcm_format, PCM_CHANNEL_CAPACITY);
    let worker = std::thread::spawn(move || {
        let result = if is_opus {
            crate::opus::decode_opus_stream(crate::opus::OpusStreamRequest {
                format: &mut *format,
                track_id,
                channels,
                pre_skip: delay,
                padding,
                producer: &producer,
                integrity: request.integrity.as_ref(),
            })
        } else {
            decode_packets(PacketDecoder {
                format: &mut *format,
                track_id,
                decoder,
                producer: &producer,
                integrity: request.integrity.as_ref(),
            })
        };
        match result {
            Ok(()) => {
                let _ = producer.finish();
            }
            Err(error) => {
                let _ = producer.fail(error);
            }
        }
    });
    output.attach_worker(worker);
    Ok(output)
}

struct PacketDecoder<'a> {
    format: &'a mut dyn symphonia::core::formats::FormatReader,
    track_id: u32,
    decoder: Option<Box<dyn symphonia::core::codecs::Decoder>>,
    producer: &'a PcmProducer,
    integrity: Option<&'a StreamIntegrity>,
}

fn decode_packets(mut job: PacketDecoder<'_>) -> Result<(), MediaError> {
    let decoder = job
        .decoder
        .as_mut()
        .ok_or_else(|| MediaError::Decode("decoder was not initialized".to_owned()))?;
    loop {
        let packet = match job.format.next_packet() {
            Ok(value) => value,
            Err(SymphoniaError::IoError(error))
                if error.kind() == std::io::ErrorKind::UnexpectedEof =>
            {
                if job
                    .integrity
                    .is_some_and(StreamIntegrity::is_complete_for_decoder)
                    || job.integrity.is_none()
                {
                    return Ok(());
                }
                return Err(MediaError::Truncated);
            }
            Err(error) => return Err(crate::media::classify_decode_error(error)),
        };
        if job.producer.is_cancelled() {
            return Ok(());
        }
        if packet.track_id() != job.track_id {
            continue;
        }
        let decoded = decoder
            .decode(&packet)
            .map_err(crate::media::classify_decode_error)?;
        let specification = *decoded.spec();
        let mut converted = SampleBuffer::<f32>::new(decoded.capacity() as u64, specification);
        converted.copy_interleaved_ref(decoded);
        for chunk in converted.samples().chunks(PCM_CHUNK_SAMPLES) {
            job.producer
                .send(chunk.to_vec())
                .map_err(|_| MediaError::Cancelled)?;
        }
    }
}
