use crate::audio::PlaybackBackend;
use crate::engine::Engine;
use crate::harness::{JournalResult, SecretToken};
use crate::media::MediaLoader;
use crate::protocol::{Command, ServerFrame};
use crate::security::CertificateFingerprint;
use crate::session_messages::{hello, playback_frame};
use crate::session_transport::{ConnectRequest, connect, read_frame};
use futures_util::SinkExt;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::protocol::Message;
use url::Url;

mod commands;

pub struct SessionConfig<'a> {
    pub server_origin: &'a Url,
    pub fingerprint: CertificateFingerprint,
    pub renderer_id: &'a str,
    pub token: &'a SecretToken,
    pub observed_certificate: Arc<Mutex<Option<Vec<u8>>>>,
}

#[derive(Debug, thiserror::Error)]
pub enum SessionError {
    #[error("SESSION_FAILED: {0}")]
    Transport(#[from] tokio_tungstenite::tungstenite::Error),
    #[error("SESSION_FAILED: {0}")]
    Protocol(#[from] crate::protocol::FrameError),
    #[error("SESSION_FAILED: {0}")]
    Json(#[from] serde_json::Error),
    #[error("SESSION_FAILED: {0}")]
    Engine(#[from] crate::engine::EngineError),
    #[error("SESSION_FAILED: {0}")]
    Journal(#[from] crate::harness::JournalError),
    #[error("SESSION_FAILED: {0}")]
    Invalid(String),
}

pub async fn run_foreground<B, M>(
    config: SessionConfig<'_>,
    engine: Arc<Mutex<Engine<B, M>>>,
) -> Result<(), SessionError>
where
    B: PlaybackBackend + Send + 'static,
    M: MediaLoader + Send + 'static,
{
    let crate::session_executor::CommandExecutor {
        commands: command_sender,
        results: mut result_receiver,
        worker,
    } = crate::session_executor::start(Arc::clone(&engine));
    let outcome = loop {
        let result = run_connection(
            &config,
            ConnectionRuntime {
                engine: Arc::clone(&engine),
                command_sender: &command_sender,
                result_receiver: &mut result_receiver,
            },
        )
        .await;
        match result {
            Ok(()) => break Ok(()),
            Err(SessionError::Transport(_)) => {
                tokio::select! {
                    signal = tokio::signal::ctrl_c() => {
                        match signal {
                            Ok(()) => break Ok(()),
                            Err(error) => break Err(SessionError::Invalid(error.to_string())),
                        }
                    }
                    () = tokio::time::sleep(Duration::from_secs(1)) => {}
                }
            }
            Err(error) => break Err(error),
        }
    };
    drop(command_sender);
    worker
        .await
        .map_err(|error| SessionError::Invalid(error.to_string()))?;
    outcome
}

struct ConnectionRuntime<'a, B, M> {
    engine: Arc<Mutex<Engine<B, M>>>,
    command_sender: &'a mpsc::Sender<Command>,
    result_receiver: &'a mut mpsc::Receiver<Result<JournalResult, crate::engine::EngineError>>,
}

async fn run_connection<B, M>(
    config: &SessionConfig<'_>,
    runtime: ConnectionRuntime<'_, B, M>,
) -> Result<(), SessionError>
where
    B: PlaybackBackend + Send + 'static,
    M: MediaLoader + Send + 'static,
{
    let ConnectionRuntime {
        engine,
        command_sender,
        result_receiver,
    } = runtime;
    let mut socket = connect(ConnectRequest {
        server_origin: config.server_origin,
        fingerprint: config.fingerprint,
        renderer_id: config.renderer_id,
        token: config.token,
        observed_certificate: Arc::clone(&config.observed_certificate),
    })
    .await?;
    let (cursor, pending) = {
        let guard = engine
            .lock()
            .map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))?;
        (
            guard.journal().last_server_sequence(),
            guard.journal().pending_results(),
        )
    };
    let hello = hello(config.renderer_id, cursor, &pending);
    socket
        .send(Message::Text(serde_json::to_string(&hello)?.into()))
        .await?;
    let welcome = read_frame(&mut socket).await?;
    let epoch = match welcome {
        ServerFrame::Welcome(frame)
            if frame.protocol_major == 3
                && frame.frame_type == "welcome"
                && frame.selected_major == 3 =>
        {
            frame.session_epoch
        }
        ServerFrame::Welcome(_) => {
            return Err(SessionError::Invalid("welcome is invalid".to_owned()));
        }
        ServerFrame::Command(_) | ServerFrame::ResultAck(_) | ServerFrame::Error(_) => {
            return Err(SessionError::Invalid("welcome was not first".to_owned()));
        }
    };
    engine
        .lock()
        .map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))?
        .set_session_epoch(&epoch);
    commands::recover(&mut socket, &engine, command_sender).await?;
    let mut playback_events = tokio::time::interval(Duration::from_millis(100));
    playback_events.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            signal = tokio::signal::ctrl_c() => {
                signal.map_err(|error| SessionError::Invalid(error.to_string()))?;
                socket.close(None).await?;
                return Ok(());
            }
            _ = playback_events.tick() => {
                let event = {
                    let engine = Arc::clone(&engine);
                    tokio::task::spawn_blocking(move || {
                        engine
                            .lock()
                            .map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))
                            .map(|mut value| value.poll_playback_event())
                    })
                    .await
                    .map_err(|error| SessionError::Invalid(error.to_string()))??
                };
                if let Some((play_id, event)) = event {
                    let frame = playback_frame(&epoch, &play_id, event)
                        .map_err(|error| SessionError::Invalid(error.to_string()))?;
                    socket.send(Message::Text(serde_json::to_string(&frame)?.into())).await?;
                }
            }
            result = result_receiver.recv() => {
                let result = result.ok_or_else(|| {
                    SessionError::Invalid("command executor stopped".to_owned())
                })??;
                socket
                    .send(Message::Text(serde_json::to_string(&result)?.into()))
                    .await?;
            }
            frame = read_frame(&mut socket) => {
                match frame? {
                    ServerFrame::Command(command) => {
                        commands::accept(&mut socket, &engine, command_sender, command).await?;
                    }
                    ServerFrame::ResultAck(frame) => {
                        if frame.protocol_major != 3 || frame.frame_type != "result.ack" {
                            return Err(SessionError::Invalid("result acknowledgement is invalid".to_owned()));
                        }
                        engine.lock().map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))?.journal_mut().acknowledge_result(&frame.result_id)?;
                    }
                    ServerFrame::Error(frame) => {
                        return Err(SessionError::Invalid(format!("{}: {}", frame.code, frame.message)));
                    }
                    ServerFrame::Welcome(_) => return Err(SessionError::Invalid("duplicate welcome".to_owned())),
                }
            }
        }
    }
}

pub use crate::session_messages::{command_error, command_fingerprint};
