use crate::media::{MediaError, PcmProducer};
use opus_decoder::OpusDecoder;
use symphonia::core::errors::Error as SymphoniaError;
use symphonia::core::formats::FormatReader;

const MAX_OPUS_PACKET_FRAMES: usize = 5_760;

pub struct OpusStreamRequest<'a> {
    pub format: &'a mut dyn FormatReader,
    pub track_id: u32,
    pub channels: u16,
    pub pre_skip: usize,
    pub padding: usize,
    pub producer: &'a PcmProducer,
    pub integrity: Option<&'a crate::decoder::StreamIntegrity>,
}

pub fn decode_opus_stream(request: OpusStreamRequest<'_>) -> Result<(), MediaError> {
    if request.channels == 0 || request.channels > 2 {
        return Err(MediaError::Unsupported(
            "Opus harness supports mono or stereo".to_owned(),
        ));
    }
    if request.padding > MAX_OPUS_PACKET_FRAMES {
        return Err(MediaError::Truncated);
    }
    let channel_count = usize::from(request.channels);
    let mut decoder = OpusDecoder::new(48_000, channel_count)
        .map_err(|error| MediaError::Decode(error.to_string()))?;
    let maximum_samples = decoder.max_frame_size_per_channel() * channel_count;
    let mut packet_samples = vec![0.0_f32; maximum_samples];
    let retained_samples = request.padding.saturating_mul(channel_count);
    let mut skipped = 0;
    let mut tail = Vec::new();
    loop {
        let packet = match request.format.next_packet() {
            Ok(value) => value,
            Err(SymphoniaError::IoError(error))
                if error.kind() == std::io::ErrorKind::UnexpectedEof =>
            {
                if request
                    .integrity
                    .is_some_and(crate::decoder::StreamIntegrity::is_complete_for_decoder)
                    || request.integrity.is_none()
                {
                    return Ok(());
                }
                return Err(MediaError::Truncated);
            }
            Err(error) => return Err(MediaError::Decode(error.to_string())),
        };
        if request.producer.is_cancelled() {
            return Ok(());
        }
        if packet.track_id() != request.track_id {
            continue;
        }
        let frames = decoder
            .decode_float(packet.buf(), &mut packet_samples, false)
            .map_err(|_| MediaError::Truncated)?;
        let count = frames.saturating_mul(channel_count);
        if count > packet_samples.len() {
            return Err(MediaError::Truncated);
        }
        let skip = request
            .pre_skip
            .saturating_mul(channel_count)
            .saturating_sub(skipped)
            .min(count);
        skipped = skipped.saturating_add(skip);
        tail.extend_from_slice(&packet_samples[skip..count]);
        let ready = tail.len().saturating_sub(retained_samples);
        if ready > 0 {
            let retained = tail.split_off(ready);
            request
                .producer
                .send(std::mem::replace(&mut tail, retained))
                .map_err(|_| MediaError::Cancelled)?;
        }
    }
}
