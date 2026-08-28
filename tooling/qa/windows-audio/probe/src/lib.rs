use serde::Serialize;

#[derive(Clone, Copy, Debug, PartialEq, Serialize)]
pub struct SignalMetrics {
    pub peak_frequency_hz: f64,
    pub rms: f64,
    pub absolute_max: f32,
    pub duration_ms: u64,
}

#[must_use]
pub fn analyze(samples: &[f32], sample_rate_hz: u32, channels: u16) -> Option<SignalMetrics> {
    let channel_count = usize::from(channels);
    if samples.is_empty() || channel_count == 0 || !samples.len().is_multiple_of(channel_count) {
        return None;
    }
    let frames = samples.len() / channel_count;
    let mut mono = Vec::with_capacity(frames);
    for frame in samples.chunks_exact(channel_count) {
        mono.push(frame.iter().copied().sum::<f32>() / f32::from(channels));
    }
    let first_active = mono.iter().position(|sample| sample.abs() > 0.005);
    let last_active = mono.iter().rposition(|sample| sample.abs() > 0.005);
    let (start, end) = match (first_active, last_active) {
        (Some(first), Some(last)) => (first, last + 1),
        (None, None) => (0, frames),
        (Some(_), None) | (None, Some(_)) => return None,
    };
    let active = &samples[start * channel_count..end * channel_count];
    let square_sum = active
        .iter()
        .map(|sample| f64::from(*sample).powi(2))
        .sum::<f64>();
    let sample_count = u32::try_from(active.len()).ok()?;
    let rms = (square_sum / f64::from(sample_count)).sqrt();
    let absolute_max = active.iter().copied().map(f32::abs).fold(0.0, f32::max);
    Some(SignalMetrics {
        peak_frequency_hz: zero_crossing_frequency(&mono[start..end], sample_rate_hz),
        rms,
        absolute_max,
        duration_ms: u64::try_from(end - start).ok()?.saturating_mul(1000)
            / u64::from(sample_rate_hz),
    })
}

#[derive(Clone, Copy, Debug)]
pub struct FrequencyComparison {
    pub sample_rate_hz: u32,
    pub dominant_hz: f64,
    pub rejected_hz: f64,
}

#[must_use]
pub fn frequency_rejection_db(samples: &[f32], comparison: FrequencyComparison) -> f64 {
    let dominant = goertzel_power(samples, comparison.sample_rate_hz, comparison.dominant_hz);
    let rejected = goertzel_power(samples, comparison.sample_rate_hz, comparison.rejected_hz)
        .max(f64::MIN_POSITIVE);
    10.0 * (dominant / rejected).log10()
}

#[must_use]
pub fn dominance_latency_ms(samples: &[f32], comparison: FrequencyComparison) -> Option<u64> {
    let window_frames = usize::try_from(comparison.sample_rate_hz / 50).ok()?;
    samples
        .chunks(window_frames)
        .enumerate()
        .find_map(|(index, window)| {
            if frequency_rejection_db(window, comparison) > 0.0 {
                u64::try_from(index + 1)
                    .ok()
                    .map(|value| value.saturating_mul(20))
            } else {
                None
            }
        })
}

fn zero_crossing_frequency(samples: &[f32], sample_rate_hz: u32) -> f64 {
    let crossings = samples
        .windows(2)
        .filter(|pair| pair[0] <= 0.0 && pair[1] > 0.0)
        .fold(0.0, |count, _| count + 1.0);
    let sample_count = samples.iter().fold(0.0, |count, _| count + 1.0);
    crossings * f64::from(sample_rate_hz) / sample_count
}

fn goertzel_power(samples: &[f32], sample_rate_hz: u32, frequency_hz: f64) -> f64 {
    let coefficient =
        2.0 * (std::f64::consts::TAU * frequency_hz / f64::from(sample_rate_hz)).cos();
    let (mut previous, mut before_previous) = (0.0, 0.0);
    for sample in samples {
        let current = f64::from(*sample) + coefficient * previous - before_previous;
        before_previous = previous;
        previous = current;
    }
    previous.mul_add(previous, before_previous * before_previous)
        - coefficient * previous * before_previous
}

#[cfg(windows)]
pub mod exclusive;
#[cfg(windows)]
pub mod wasapi;

#[cfg(test)]
mod tests {
    use super::*;
    use std::f32::consts::TAU;

    fn stereo_tone_frames(frequency_hz: f32, frames: u32, amplitude: f32) -> Vec<f32> {
        (0..frames)
            .scan(0.0_f32, |phase, _| {
                let value = amplitude * (*phase).sin();
                *phase += TAU * frequency_hz / 48_000.0;
                Some([value, value])
            })
            .flatten()
            .collect()
    }

    #[test]
    fn measures_normalized_tone_when_stereo_capture_is_complete() {
        // Given
        let frame_count = if cfg!(miri) { 4_800 } else { 96_000 };
        let samples = stereo_tone_frames(1_000.0, frame_count, 10.0_f32.powf(-12.0 / 20.0));
        // When
        let metrics = analyze(&samples, 48_000, 2);
        // Then
        let duration = if cfg!(miri) { 90..=110 } else { 1_900..=2_100 };
        assert!(
            metrics.is_some_and(|value| (990.0..=1010.0).contains(&value.peak_frequency_hz)
                && (0.17..=0.19).contains(&value.rms)
                && value.absolute_max < 0.30
                && duration.contains(&value.duration_ms))
        );
    }

    #[test]
    fn rejects_empty_and_misaligned_capture() {
        // Given / When / Then
        assert_eq!(analyze(&[], 48_000, 2), None);
        assert_eq!(analyze(&[0.0], 48_000, 2), None);
    }

    #[test]
    fn detects_seek_dominance_within_bounded_latency() {
        // Given
        let mut samples = stereo_tone_frames(440.0, 4_800, 0.2);
        samples.extend(stereo_tone_frames(1_000.0, 9_600, 0.2));
        let mono: Vec<f32> = samples.chunks_exact(2).map(|frame| frame[0]).collect();
        // When / Then
        assert!(
            dominance_latency_ms(
                &mono,
                FrequencyComparison {
                    sample_rate_hz: 48_000,
                    dominant_hz: 1_000.0,
                    rejected_hz: 440.0,
                }
            )
            .is_some_and(|value| value <= 200)
        );
    }

    #[test]
    fn measures_seek_frequency_rejection_when_target_dominates() {
        // Given
        let target = stereo_tone_frames(1_000.0, 4_800, 0.2);
        let mono: Vec<f32> = target.chunks_exact(2).map(|frame| frame[0]).collect();
        // When
        let rejection = frequency_rejection_db(
            &mono,
            FrequencyComparison {
                sample_rate_hz: 48_000,
                dominant_hz: 1_000.0,
                rejected_hz: 440.0,
            },
        );
        // Then
        assert!(rejection >= 40.0);
    }
}
