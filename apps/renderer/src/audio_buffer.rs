use crate::media::{AudioStream, MediaError, StreamItem};
use std::sync::mpsc::TryRecvError;

#[derive(Debug, thiserror::Error)]
pub enum BufferFailure {
    #[error(transparent)]
    Media(#[from] MediaError),
    #[error("PLAYBACK_FAILED: decoder channel disconnected before EOF")]
    Disconnected,
}

#[derive(Debug)]
pub enum BufferEvent {
    Frames(usize),
    Underrun,
    Draining,
    Ended,
    Failed(BufferFailure),
}

impl PartialEq for BufferEvent {
    fn eq(&self, other: &Self) -> bool {
        matches!(
            (self, other),
            (Self::Frames(left), Self::Frames(right)) if left == right
        ) || matches!(
            (self, other),
            (Self::Underrun, Self::Underrun)
                | (Self::Draining, Self::Draining)
                | (Self::Ended, Self::Ended)
                | (Self::Failed(_), Self::Failed(_))
        )
    }
}

pub struct PcmBuffer {
    stream: AudioStream,
    current: Vec<f32>,
    offset: usize,
    eof: bool,
    drain_callback_seen: bool,
}

impl PcmBuffer {
    pub fn new(stream: AudioStream) -> Self {
        Self {
            stream,
            current: Vec::new(),
            offset: 0,
            eof: false,
            drain_callback_seen: false,
        }
    }

    pub fn format(&self) -> crate::media::PcmFormat {
        self.stream.format()
    }

    pub fn fill(&mut self, output: &mut [f32]) -> BufferEvent {
        output.fill(0.0);
        if self.eof {
            if self.drain_callback_seen {
                return BufferEvent::Ended;
            }
            self.drain_callback_seen = true;
            return BufferEvent::Draining;
        }
        let mut written = 0;
        while written < output.len() {
            if self.offset < self.current.len() {
                let count = (output.len() - written).min(self.current.len() - self.offset);
                output[written..written + count]
                    .copy_from_slice(&self.current[self.offset..self.offset + count]);
                self.offset += count;
                written += count;
                continue;
            }
            self.current.clear();
            self.offset = 0;
            match self.stream.try_next() {
                Ok(StreamItem::Samples(samples)) => self.current = samples,
                Ok(StreamItem::Eof) => {
                    self.eof = true;
                    break;
                }
                Ok(StreamItem::Failed(error)) => {
                    return BufferEvent::Failed(BufferFailure::Media(error));
                }
                Err(TryRecvError::Empty) => break,
                Err(TryRecvError::Disconnected) => {
                    return BufferEvent::Failed(BufferFailure::Disconnected);
                }
            }
        }
        if written > 0 {
            BufferEvent::Frames(written / usize::from(self.stream.format().channels))
        } else if self.eof {
            self.drain_callback_seen = true;
            BufferEvent::Draining
        } else {
            BufferEvent::Underrun
        }
    }
}
