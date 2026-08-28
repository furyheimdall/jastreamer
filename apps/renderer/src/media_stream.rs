use crate::media::MediaError;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{Receiver, RecvTimeoutError, SyncSender, TryRecvError, sync_channel};
use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::thread::JoinHandle;
use std::time::Duration;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct PcmFormat {
    pub channels: u16,
    pub sample_rate_hz: u32,
    pub start_position_ms: u64,
}

#[derive(Debug)]
pub enum StreamItem {
    Samples(Vec<f32>),
    Eof,
    Failed(MediaError),
}

#[derive(Debug, thiserror::Error)]
#[error("decoder stream was cancelled")]
pub struct StreamSendError;

struct QueueState {
    queued: Mutex<usize>,
    changed: Condvar,
    capacity: usize,
}

impl QueueState {
    fn lock(&self) -> MutexGuard<'_, usize> {
        match self.queued.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    fn wait<'a>(&self, guard: MutexGuard<'a, usize>) -> MutexGuard<'a, usize> {
        match self.changed.wait(guard) {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    fn wait_until_full(&self, timeout: Duration) -> bool {
        let result = self
            .changed
            .wait_timeout_while(self.lock(), timeout, |queued| *queued < self.capacity);
        let (queued, timeout_result) = match result {
            Ok(value) => value,
            Err(poisoned) => poisoned.into_inner(),
        };
        !timeout_result.timed_out() && *queued == self.capacity
    }
}

pub struct PcmProducer {
    sender: SyncSender<StreamItem>,
    cancelled: Arc<AtomicBool>,
    queue: Arc<QueueState>,
}

impl PcmProducer {
    pub fn send(&self, samples: Vec<f32>) -> Result<(), StreamSendError> {
        self.send_item(StreamItem::Samples(samples))
    }

    pub fn finish(&self) -> Result<(), StreamSendError> {
        self.send_item(StreamItem::Eof)
    }

    pub fn fail(&self, error: MediaError) -> Result<(), StreamSendError> {
        self.send_item(StreamItem::Failed(error))
    }

    pub fn is_cancelled(&self) -> bool {
        self.cancelled.load(Ordering::Acquire)
    }

    fn send_item(&self, item: StreamItem) -> Result<(), StreamSendError> {
        let mut queued = self.queue.lock();
        while *queued == self.queue.capacity && !self.is_cancelled() {
            queued = self.queue.wait(queued);
        }
        if self.is_cancelled() {
            return Err(StreamSendError);
        }
        *queued += 1;
        self.queue.changed.notify_all();
        drop(queued);
        if self.sender.send(item).is_err() {
            self.release_slot();
            return Err(StreamSendError);
        }
        Ok(())
    }

    fn release_slot(&self) {
        let mut queued = self.queue.lock();
        *queued = queued.saturating_sub(1);
        self.queue.changed.notify_all();
    }
}

pub struct AudioStream {
    format: PcmFormat,
    receiver: Option<Receiver<StreamItem>>,
    cancelled: Arc<AtomicBool>,
    queue: Arc<QueueState>,
    worker: Option<JoinHandle<()>>,
}

impl AudioStream {
    pub fn format(&self) -> PcmFormat {
        self.format
    }

    pub fn buffered_items(&self) -> usize {
        *self.queue.lock()
    }

    pub fn wait_until_full(&self, timeout: Duration) -> bool {
        self.queue.wait_until_full(timeout)
    }

    pub fn try_next(&mut self) -> Result<StreamItem, TryRecvError> {
        let result = self
            .receiver
            .as_ref()
            .ok_or(TryRecvError::Disconnected)?
            .try_recv();
        if result.is_ok() {
            self.release_slot();
        }
        result
    }

    pub fn recv_timeout(&mut self, timeout: Duration) -> Result<StreamItem, RecvTimeoutError> {
        let result = self
            .receiver
            .as_ref()
            .ok_or(RecvTimeoutError::Disconnected)?
            .recv_timeout(timeout);
        if result.is_ok() {
            self.release_slot();
        }
        result
    }

    pub(crate) fn attach_worker(&mut self, worker: JoinHandle<()>) {
        self.worker = Some(worker);
    }

    fn release_slot(&self) {
        let mut queued = self.queue.lock();
        *queued = queued.saturating_sub(1);
        self.queue.changed.notify_all();
    }
}

impl Drop for AudioStream {
    fn drop(&mut self) {
        {
            let _queued = self.queue.lock();
            self.cancelled.store(true, Ordering::Release);
            self.queue.changed.notify_all();
        }
        self.receiver.take();
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

pub fn pcm_channel(format: PcmFormat, capacity: usize) -> (PcmProducer, AudioStream) {
    let capacity = capacity.max(1);
    let (sender, receiver) = sync_channel(capacity);
    let cancelled = Arc::new(AtomicBool::new(false));
    let queue = Arc::new(QueueState {
        queued: Mutex::new(0),
        changed: Condvar::new(),
        capacity,
    });
    (
        PcmProducer {
            sender,
            cancelled: Arc::clone(&cancelled),
            queue: Arc::clone(&queue),
        },
        AudioStream {
            format,
            receiver: Some(receiver),
            cancelled,
            queue,
            worker: None,
        },
    )
}
