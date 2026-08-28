use crate::audio::{AudioTelemetry, BufferEvent, PcmBuffer, PlaybackEvent, PlaybackState};
use crate::media::PcmFormat;
use parking_lot::Mutex;
use std::collections::VecDeque;
use std::sync::mpsc::SyncSender;

pub(super) struct Shared {
    pub(super) buffer: Option<PcmBuffer>,
    pub(super) state: PlaybackState,
    pub(super) events: VecDeque<PlaybackEvent>,
    pub(super) telemetry: AudioTelemetry,
    pub(super) stream_frames: u64,
    pub(super) format: PcmFormat,
}

pub(super) fn render_callback(
    output: &mut [f32],
    shared: &Mutex<Shared>,
    started: &SyncSender<()>,
) {
    let mut state = shared.lock();
    if state.state != PlaybackState::Playing {
        output.fill(0.0);
        return;
    }
    let Some(buffer) = state.buffer.as_mut() else {
        output.fill(0.0);
        state.telemetry.underruns = state.telemetry.underruns.saturating_add(1);
        return;
    };
    let event = buffer.fill(output);
    match event {
        BufferEvent::Frames(frames) => {
            state.stream_frames = state.stream_frames.saturating_add(frames as u64);
            state.telemetry.frames_written =
                state.telemetry.frames_written.saturating_add(frames as u64);
            let _ = started.try_send(());
        }
        BufferEvent::Underrun => {
            state.telemetry.underruns = state.telemetry.underruns.saturating_add(1);
        }
        BufferEvent::Draining => {}
        BufferEvent::Ended => {
            let position_ms = state.format.start_position_ms
                + state.stream_frames.saturating_mul(1000) / u64::from(state.format.sample_rate_hz);
            state.events.push_back(PlaybackEvent::Ended { position_ms });
            state.state = PlaybackState::Idle;
        }
        BufferEvent::Failed(error) => {
            state.events.push_back(PlaybackEvent::Failed {
                message: error.to_string(),
            });
            state.state = PlaybackState::Stopped;
        }
    }
}
