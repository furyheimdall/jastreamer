use crate::protocol::Command;
use fs2::FileExt;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

mod ids;
mod types;
pub use ids::{payload_digest, stable_result_id};
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
pub use types::{CommandDecision, JournalCommand, JournalError, JournalResult, ProtocolFailure};

#[derive(Debug, Deserialize, Default, Serialize)]
#[serde(rename_all = "camelCase")]
struct JournalState {
    last_server_sequence: u64,
    commands: BTreeMap<String, JournalCommand>,
    #[serde(default)]
    completed_results: BTreeMap<String, JournalResult>,
    pending_results: BTreeMap<String, JournalResult>,
}

pub struct DurableJournal {
    directory: PathBuf,
    state: JournalState,
    _lock: File,
}

impl DurableJournal {
    pub fn open(directory: &Path) -> Result<Self, JournalError> {
        fs::create_dir_all(directory)?;
        let lock = OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .open(directory.join("renderer.lock"))?;
        lock.try_lock_exclusive()
            .map_err(|_| JournalError::StateDirectoryBusy)?;
        let path = directory.join("journal.json");
        let state = match fs::read(&path) {
            Ok(bytes) => serde_json::from_slice(&bytes)?,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => JournalState::default(),
            Err(error) => return Err(error.into()),
        };
        Ok(Self {
            directory: directory.to_owned(),
            state,
            _lock: lock,
        })
    }

    pub fn last_server_sequence(&self) -> u64 {
        self.state.last_server_sequence
    }

    pub fn pending_results(&self) -> Vec<JournalResult> {
        self.state.pending_results.values().cloned().collect()
    }

    pub fn pending_result_for_command(&self, command_id: &str) -> Option<JournalResult> {
        self.state
            .pending_results
            .values()
            .find(|result| result.command_id == command_id)
            .cloned()
    }

    pub fn result_for_command(&self, command_id: &str) -> Option<JournalResult> {
        self.state
            .completed_results
            .get(command_id)
            .cloned()
            .or_else(|| self.pending_result_for_command(command_id))
    }

    pub fn accepted_command(&self, command_id: &str) -> Option<Command> {
        self.state
            .commands
            .get(command_id)
            .map(|record| record.command.clone())
    }

    pub fn unfinished_commands(&self) -> Vec<Command> {
        let mut commands: Vec<_> = self
            .state
            .commands
            .values()
            .filter(|record| self.result_for_command(&record.command_id).is_none())
            .map(|record| record.command.clone())
            .collect();
        commands.sort_by_key(|command| command.sequence);
        commands
    }

    pub fn accept(&mut self, command: JournalCommand) -> Result<CommandDecision, JournalError> {
        if let Some(existing) = self.state.commands.get(&command.command_id) {
            if existing == &command {
                return Ok(CommandDecision::Duplicate);
            }
            return Err(JournalError::CommandConflict {
                command_id: command.command_id,
            });
        }
        self.state.last_server_sequence = self.state.last_server_sequence.max(command.sequence);
        self.state
            .commands
            .insert(command.command_id.clone(), command);
        self.persist()?;
        Ok(CommandDecision::Execute)
    }

    pub fn complete(
        &mut self,
        command_id: &str,
        result_id: &str,
        status: &str,
        observed_state: Option<&str>,
        position_ms: Option<u64>,
    ) -> Result<JournalResult, JournalError> {
        let result = JournalResult {
            protocol_major: 3,
            frame_type: "command.result".to_owned(),
            command_id: command_id.to_owned(),
            result_id: result_id.to_owned(),
            status: status.to_owned(),
            observed_state: observed_state.map(str::to_owned),
            position_ms,
            error: None,
        };
        self.record_result(result)
    }

    pub fn complete_failure(
        &mut self,
        command_id: &str,
        result_id: &str,
        failure: ProtocolFailure,
    ) -> Result<JournalResult, JournalError> {
        self.record_result(JournalResult {
            protocol_major: 3,
            frame_type: "command.result".to_owned(),
            command_id: command_id.to_owned(),
            result_id: result_id.to_owned(),
            status: "failed".to_owned(),
            observed_state: None,
            position_ms: None,
            error: Some(failure),
        })
    }

    pub fn acknowledge_result(&mut self, result_id: &str) -> Result<(), JournalError> {
        if let Some(result) = self.state.pending_results.remove(result_id) {
            self.state
                .completed_results
                .insert(result.command_id.clone(), result);
        }
        self.persist()
    }

    fn record_result(&mut self, result: JournalResult) -> Result<JournalResult, JournalError> {
        if let Some(existing) = self.result_for_command(&result.command_id) {
            return Ok(existing);
        }
        self.state
            .completed_results
            .insert(result.command_id.clone(), result.clone());
        self.state
            .pending_results
            .insert(result.result_id.clone(), result.clone());
        self.persist()?;
        Ok(result)
    }

    fn persist(&self) -> Result<(), JournalError> {
        let temporary = self.directory.join("journal.json.tmp");
        let destination = self.directory.join("journal.json");
        let mut file = OpenOptions::new()
            .create(true)
            .truncate(true)
            .write(true)
            .open(&temporary)?;
        serde_json::to_writer(&mut file, &self.state)?;
        file.write_all(b"\n")?;
        file.sync_all()?;
        fs::rename(temporary, destination)?;
        Ok(())
    }
}
