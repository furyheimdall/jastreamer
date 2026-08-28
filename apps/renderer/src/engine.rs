use crate::audio::{PlaybackBackend, PlaybackEvent, PlaybackState};
use crate::harness::{
    CommandDecision, DurableJournal, JournalCommand, JournalResult, stable_result_id,
};
pub use crate::media::MediaLoader;
use crate::protocol::{Command, MediaRepresentation, failure};

mod command_validation;
mod failure;
mod playback;
mod types;
use failure::audio_failure;
use std::collections::HashSet;
pub use types::{CommandAcceptance, CommandOutcome, EngineError};

pub struct Engine<B, M> {
    journal: DurableJournal,
    backend: B,
    media: M,
    session_epoch: String,
    active_play_id: Option<String>,
    active_media: Option<MediaRepresentation>,
    desired_state: PlaybackState,
    in_flight: HashSet<String>,
}

impl<B: PlaybackBackend, M: MediaLoader> Engine<B, M> {
    pub fn new(journal: DurableJournal, backend: B, media: M) -> Self {
        Self {
            journal,
            backend,
            media,
            session_epoch: String::new(),
            active_play_id: None,
            active_media: None,
            desired_state: PlaybackState::Idle,
            in_flight: HashSet::new(),
        }
    }

    pub fn set_session_epoch(&mut self, epoch: &str) {
        self.session_epoch.clear();
        self.session_epoch.push_str(epoch);
    }

    pub fn journal(&self) -> &DurableJournal {
        &self.journal
    }

    pub fn journal_mut(&mut self) -> &mut DurableJournal {
        &mut self.journal
    }

    pub fn poll_playback_event(&mut self) -> Option<(String, PlaybackEvent)> {
        let event = self.backend.poll_event()?;
        let play_id = self.active_play_id.clone()?;
        match event {
            PlaybackEvent::Ended { position_ms } => {
                self.active_play_id = None;
                self.active_media = None;
                self.desired_state = PlaybackState::Idle;
                Some((play_id, PlaybackEvent::Ended { position_ms }))
            }
            PlaybackEvent::OutputInvalidated => {
                let recovered = self.recover_output();
                match recovered {
                    Ok(()) => None,
                    Err(error) => Some((
                        play_id,
                        PlaybackEvent::Failed {
                            message: error.to_string(),
                        },
                    )),
                }
            }
            PlaybackEvent::Failed { message } => Some((play_id, PlaybackEvent::Failed { message })),
        }
    }

    pub fn execute(&mut self, command: &Command) -> Result<CommandOutcome, EngineError> {
        let acceptance = self.accept_command(command)?;
        if let Some(result) = acceptance.result {
            return Ok(CommandOutcome {
                ack_status: acceptance.ack_status,
                result,
            });
        }
        if let Some(accepted) = acceptance.command {
            self.claim_execution(&accepted.command_id);
            let result = self.execute_accepted(&accepted)?;
            return Ok(CommandOutcome {
                ack_status: acceptance.ack_status,
                result,
            });
        }
        let result = self
            .journal
            .result_for_command(&command.command_id)
            .ok_or_else(|| {
                EngineError::Invalid("accepted command has no durable state".to_owned())
            })?;
        Ok(CommandOutcome {
            ack_status: acceptance.ack_status,
            result,
        })
    }

    pub fn accept_command(&mut self, command: &Command) -> Result<CommandAcceptance, EngineError> {
        self.validate(command)?;
        let record = JournalCommand::from_command(command.clone())
            .map_err(|error| EngineError::Invalid(error.to_string()))?;
        let decision = self.journal.accept(record)?;
        let completed = self.journal.result_for_command(&command.command_id);
        Ok(CommandAcceptance {
            ack_status: match decision {
                CommandDecision::Execute => "received",
                CommandDecision::Duplicate => "duplicate",
            },
            command: completed.is_none().then(|| command.clone()),
            result: self.journal.pending_result_for_command(&command.command_id),
        })
    }

    pub(crate) fn claim_execution(&mut self, command_id: &str) -> bool {
        self.in_flight.insert(command_id.to_owned())
    }

    pub(crate) fn recoverable_commands(&self) -> Vec<Command> {
        self.journal
            .unfinished_commands()
            .into_iter()
            .filter(|command| !self.in_flight.contains(&command.command_id))
            .collect()
    }

    pub(crate) fn release_execution(&mut self, command_id: &str) {
        self.in_flight.remove(command_id);
    }

    pub fn execute_accepted(&mut self, command: &Command) -> Result<JournalResult, EngineError> {
        if self.journal.accepted_command(&command.command_id).as_ref() != Some(command) {
            return Err(EngineError::Invalid(
                "command was not durably accepted".to_owned(),
            ));
        }
        if let Some(result) = self.journal.result_for_command(&command.command_id) {
            self.in_flight.remove(&command.command_id);
            return Ok(result);
        }
        let result = self.execute_once(command);
        self.in_flight.remove(&command.command_id);
        result
    }

    fn execute_once(&mut self, command: &Command) -> Result<JournalResult, EngineError> {
        let result_id = stable_result_id(&command.command_id);
        if !matches!(
            command.kind.as_str(),
            "play" | "pause" | "resume" | "stop" | "seek"
        ) {
            return Ok(self.journal.complete_failure(
                &command.command_id,
                &result_id,
                failure(
                    "UNSUPPORTED_COMMAND",
                    &format!("unsupported Renderer command {}", command.kind),
                    false,
                ),
            )?);
        }
        let operation = match command.kind.as_str() {
            "play" => self.play(command),
            "pause" => {
                let result = self.backend.pause();
                if result.is_ok() {
                    self.desired_state = PlaybackState::Paused;
                }
                result
            }
            "resume" => {
                let result = self.backend.resume();
                if result.is_ok() {
                    self.desired_state = PlaybackState::Playing;
                }
                result
            }
            "stop" => {
                self.active_play_id = None;
                self.active_media = None;
                self.desired_state = PlaybackState::Stopped;
                self.backend.stop()
            }
            "seek" => self.seek(command.position_ms.unwrap_or(0)),
            _ => {
                return Err(EngineError::Invalid(
                    "command kind changed during execution".to_owned(),
                ));
            }
        };
        match operation {
            Ok(()) => {
                let observed = match self.backend.state() {
                    PlaybackState::Idle => "idle",
                    PlaybackState::Playing => "playing",
                    PlaybackState::Paused => "paused",
                    PlaybackState::Stopped => "stopped",
                };
                Ok(self.journal.complete(
                    &command.command_id,
                    &result_id,
                    "succeeded",
                    Some(observed),
                    Some(self.backend.position_ms()),
                )?)
            }
            Err(error) => {
                let protocol_failure = audio_failure(&error, &command.kind);
                Ok(self.journal.complete_failure(
                    &command.command_id,
                    &result_id,
                    protocol_failure,
                )?)
            }
        }
    }
}
