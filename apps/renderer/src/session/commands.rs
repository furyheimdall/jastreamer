use super::SessionError;
use crate::audio::PlaybackBackend;
use crate::engine::Engine;
use crate::media::MediaLoader;
use crate::protocol::{Command, CommandAck, failure};
use crate::session_messages::rejection_code;
use futures_util::{Sink, SinkExt};
use std::sync::{Arc, Mutex};
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::{Error as WebSocketError, protocol::Message};

pub async fn recover<B, M, S>(
    socket: &mut S,
    engine: &Arc<Mutex<Engine<B, M>>>,
    commands: &mpsc::Sender<Command>,
) -> Result<(), SessionError>
where
    B: PlaybackBackend + Send + 'static,
    M: MediaLoader + Send + 'static,
    S: Sink<Message, Error = WebSocketError> + Unpin,
{
    let recovered = engine
        .lock()
        .map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))?
        .recoverable_commands();
    for command in recovered {
        send_ack(socket, &command, "duplicate", None).await?;
        let claimed = engine
            .lock()
            .map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))?
            .claim_execution(&command.command_id);
        if claimed && commands.send(command.clone()).await.is_err() {
            engine
                .lock()
                .map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))?
                .release_execution(&command.command_id);
            return Err(SessionError::Invalid("command executor stopped".to_owned()));
        }
    }
    Ok(())
}

pub async fn accept<B, M, S>(
    socket: &mut S,
    engine: &Arc<Mutex<Engine<B, M>>>,
    commands: &mpsc::Sender<Command>,
    command: Command,
) -> Result<(), SessionError>
where
    B: PlaybackBackend + Send + 'static,
    M: MediaLoader + Send + 'static,
    S: Sink<Message, Error = WebSocketError> + Unpin,
{
    let accepted_command = command.clone();
    let accepted = {
        let engine = Arc::clone(engine);
        tokio::task::spawn_blocking(move || {
            engine
                .lock()
                .map_err(|_| crate::engine::EngineError::Invalid("engine lock failed".to_owned()))?
                .accept_command(&accepted_command)
        })
        .await
        .map_err(|error| SessionError::Invalid(error.to_string()))?
    };
    match accepted {
        Ok(acceptance) => {
            send_ack(socket, &command, acceptance.ack_status, None).await?;
            if let Some(result) = acceptance.result {
                socket
                    .send(Message::Text(serde_json::to_string(&result)?.into()))
                    .await?;
            }
            if let Some(command) = acceptance.command {
                let permit = commands
                    .reserve()
                    .await
                    .map_err(|_| SessionError::Invalid("command executor stopped".to_owned()))?;
                let claimed = engine
                    .lock()
                    .map_err(|_| SessionError::Invalid("engine lock failed".to_owned()))?
                    .claim_execution(&command.command_id);
                if claimed {
                    permit.send(command);
                }
            }
        }
        Err(error) => {
            let rejection = failure(rejection_code(&error), &error.to_string(), false);
            send_ack(socket, &command, "rejected", Some(&rejection)).await?;
        }
    }
    Ok(())
}

async fn send_ack<S>(
    socket: &mut S,
    command: &Command,
    status: &'static str,
    error: Option<&crate::harness::ProtocolFailure>,
) -> Result<(), SessionError>
where
    S: Sink<Message, Error = WebSocketError> + Unpin,
{
    let acknowledgement = CommandAck {
        protocol_major: 3,
        frame_type: "command.ack",
        command_id: &command.command_id,
        sequence: command.sequence,
        status,
        error,
    };
    socket
        .send(Message::Text(
            serde_json::to_string(&acknowledgement)?.into(),
        ))
        .await?;
    Ok(())
}

#[cfg(test)]
mod tests;
