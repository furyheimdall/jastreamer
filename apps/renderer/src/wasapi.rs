#[cfg(windows)]
mod platform {
    use crate::audio::{
        AudioError, AudioTelemetry, PcmBuffer, PlaybackBackend, PlaybackEvent, PlaybackState,
    };
    use crate::endpoint::{EndpointPolicy, EndpointPreference, EndpointTransition};
    use crate::endpoint_windows::EndpointNotifier;
    use crate::media::{AudioStream, PcmFormat};
    use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
    use cpal::{BufferSize, SampleFormat, SampleRate, Stream, StreamConfig};
    use parking_lot::Mutex;
    use std::collections::VecDeque;
    use std::sync::Arc;
    use std::sync::mpsc::{Receiver, SyncSender, sync_channel};
    use std::time::Duration;

    mod render;
    use render::{Shared, render_callback};

    pub struct WasapiBackend {
        policy: EndpointPolicy,
        notifier: EndpointNotifier,
        stream: Option<Stream>,
        shared: Arc<Mutex<Shared>>,
        started_sender: SyncSender<()>,
        started_receiver: Receiver<()>,
    }

    impl WasapiBackend {
        pub fn new(endpoint: &str) -> Result<Self, AudioError> {
            let (started_sender, started_receiver) = sync_channel(1);
            let preference = if endpoint.eq_ignore_ascii_case("default") {
                EndpointPreference::Default
            } else {
                EndpointPreference::Named(endpoint.to_owned())
            };
            Ok(Self {
                policy: EndpointPolicy::new(preference),
                notifier: EndpointNotifier::start()?,
                stream: None,
                shared: Arc::new(Mutex::new(Shared {
                    buffer: None,
                    state: PlaybackState::Idle,
                    events: VecDeque::new(),
                    telemetry: AudioTelemetry::default(),
                    stream_frames: 0,
                    format: PcmFormat {
                        channels: 2,
                        sample_rate_hz: 48_000,
                        start_position_ms: 0,
                    },
                })),
                started_sender,
                started_receiver,
            })
        }

        fn output_device(&self) -> Result<cpal::Device, AudioError> {
            let host = cpal::default_host();
            match self.policy.preference() {
                EndpointPreference::Default => {
                    host.default_output_device().ok_or(AudioError::NoEndpoint)
                }
                EndpointPreference::Named(name) => host
                    .output_devices()
                    .map_err(|_| AudioError::NoEndpoint)?
                    .find(|device| device.name().is_ok_and(|value| value == *name))
                    .ok_or(AudioError::NoEndpoint),
            }
        }

        fn selected_endpoint_present(&self) -> bool {
            self.output_device().is_ok()
        }

        fn build_stream(&mut self, audio: AudioStream) -> Result<(), AudioError> {
            let format = audio.format();
            let device = self.output_device()?;
            device
                .supported_output_configs()
                .map_err(|_| AudioError::BusyEndpoint)?
                .find(|range| {
                    range.sample_format() == SampleFormat::F32
                        && range.channels() == format.channels
                        && range.min_sample_rate().0 <= format.sample_rate_hz
                        && range.max_sample_rate().0 >= format.sample_rate_hz
                })
                .ok_or(AudioError::UnsupportedFormat)?;
            let stream_config = StreamConfig {
                channels: format.channels,
                sample_rate: SampleRate(format.sample_rate_hz),
                buffer_size: BufferSize::Default,
            };
            {
                let mut shared = self.shared.lock();
                shared.buffer = Some(PcmBuffer::new(audio));
                shared.format = format;
                shared.stream_frames = 0;
                shared.state = PlaybackState::Stopped;
            }
            let shared = Arc::clone(&self.shared);
            let invalidated = Arc::clone(&self.shared);
            let started = self.started_sender.clone();
            self.stream = Some(
                device
                    .build_output_stream(
                        &stream_config,
                        move |output: &mut [f32], _| {
                            render_callback(output, &shared, &started);
                        },
                        move |_error| {
                            let mut state = invalidated.lock();
                            state.events.push_back(PlaybackEvent::OutputInvalidated);
                            state.state = PlaybackState::Stopped;
                        },
                        None,
                    )
                    .map_err(|error| AudioError::Failed(error.to_string()))?,
            );
            Ok(())
        }

        fn process_endpoint_events(&mut self) {
            while let Some(event) = self.notifier.try_recv() {
                let present = self.selected_endpoint_present();
                match self.policy.transition(event, present) {
                    EndpointTransition::KeepCurrent => {}
                    EndpointTransition::Recover | EndpointTransition::Unavailable => {
                        self.shared
                            .lock()
                            .events
                            .push_back(PlaybackEvent::OutputInvalidated);
                    }
                }
            }
        }
    }

    impl PlaybackBackend for WasapiBackend {
        fn load(&mut self, stream: AudioStream) -> Result<(), AudioError> {
            self.stream = None;
            self.build_stream(stream)
        }

        fn play(&mut self) -> Result<(), AudioError> {
            while self.started_receiver.try_recv().is_ok() {}
            self.shared.lock().state = PlaybackState::Playing;
            self.stream
                .as_ref()
                .ok_or(AudioError::NoEndpoint)?
                .play()
                .map_err(|error| AudioError::Failed(error.to_string()))?;
            self.started_receiver
                .recv_timeout(Duration::from_secs(3))
                .map_err(|_| AudioError::StartTimeout)
        }

        fn pause(&mut self) -> Result<(), AudioError> {
            self.stream
                .as_ref()
                .ok_or(AudioError::NoEndpoint)?
                .pause()
                .map_err(|error| AudioError::Failed(error.to_string()))?;
            self.shared.lock().state = PlaybackState::Paused;
            Ok(())
        }

        fn resume(&mut self) -> Result<(), AudioError> {
            self.play()
        }

        fn stop(&mut self) -> Result<(), AudioError> {
            if let Some(stream) = &self.stream {
                stream
                    .pause()
                    .map_err(|error| AudioError::Failed(error.to_string()))?;
            }
            let buffer = {
                let mut state = self.shared.lock();
                state.state = PlaybackState::Stopped;
                state.stream_frames = 0;
                state.buffer.take()
            };
            drop(buffer);
            Ok(())
        }

        fn seek(&mut self, _position_ms: u64) -> Result<(), AudioError> {
            Err(AudioError::Failed(
                "seek requires a fresh Range decoder stream".to_owned(),
            ))
        }

        fn state(&self) -> PlaybackState {
            self.shared.lock().state
        }

        fn position_ms(&self) -> u64 {
            let state = self.shared.lock();
            state.format.start_position_ms
                + state.stream_frames.saturating_mul(1000) / u64::from(state.format.sample_rate_hz)
        }

        fn poll_event(&mut self) -> Option<PlaybackEvent> {
            self.process_endpoint_events();
            self.shared.lock().events.pop_front()
        }

        fn telemetry(&self) -> AudioTelemetry {
            self.shared.lock().telemetry
        }

        fn recovery_succeeded(&mut self) {
            let mut shared = self.shared.lock();
            shared.telemetry.recoveries = shared.telemetry.recoveries.saturating_add(1);
        }
    }
}

#[cfg(windows)]
pub use platform::WasapiBackend;
