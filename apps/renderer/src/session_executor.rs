use crate::audio::PlaybackBackend;
use crate::engine::{Engine, EngineError};
use crate::harness::JournalResult;
use crate::media::MediaLoader;
use crate::protocol::Command;
use std::sync::{Arc, Mutex};
use tokio::sync::mpsc;
use tokio::task::JoinHandle;

pub struct CommandExecutor {
    pub commands: mpsc::Sender<Command>,
    pub results: mpsc::Receiver<Result<JournalResult, EngineError>>,
    pub worker: JoinHandle<()>,
}

pub fn start<B, M>(engine: Arc<Mutex<Engine<B, M>>>) -> CommandExecutor
where
    B: PlaybackBackend + Send + 'static,
    M: MediaLoader + Send + 'static,
{
    let (commands, mut command_receiver) = mpsc::channel::<Command>(32);
    let (result_sender, results) = mpsc::channel::<Result<JournalResult, EngineError>>(32);
    let worker = tokio::spawn(async move {
        while let Some(command) = command_receiver.recv().await {
            let engine = Arc::clone(&engine);
            let result = tokio::task::spawn_blocking(move || {
                engine
                    .lock()
                    .map_err(|_| EngineError::Invalid("engine lock failed".to_owned()))?
                    .execute_accepted(&command)
            })
            .await
            .map_err(|error| EngineError::Invalid(error.to_string()))
            .and_then(|result| result);
            if result_sender.send(result).await.is_err() {
                return;
            }
        }
    });
    CommandExecutor {
        commands,
        results,
        worker,
    }
}
