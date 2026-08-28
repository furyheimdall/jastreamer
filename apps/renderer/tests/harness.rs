use jastreamer_renderer::harness::{CommandDecision, DurableJournal, JournalCommand, SecretToken};
use jastreamer_renderer::protocol::{Command, MediaRepresentation};
use std::fs;

fn wire_command(id: &str, sequence: u64, kind: &str) -> Command {
    Command {
        protocol_major: 3,
        frame_type: "command".to_owned(),
        command_id: id.to_owned(),
        sequence,
        session_epoch: "epoch-1".to_owned(),
        zone_id: "zone-1".to_owned(),
        play_id: Some("play-1".to_owned()),
        kind: kind.to_owned(),
        deadline: "2099-01-01T00:00:00Z".to_owned(),
        position_ms: None,
        media: Some(MediaRepresentation {
            url: "https://server/media".to_owned(),
            mime_type: "audio/flac".to_owned(),
            headers: Default::default(),
            seekable: true,
        }),
    }
}

fn command(id: &str, sequence: u64, kind: &str) -> JournalCommand {
    JournalCommand::from_command(wire_command(id, sequence, kind)).expect("command digests")
}

#[test]
fn journal_replays_duplicates_and_rejects_identity_conflicts() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let mut journal = DurableJournal::open(directory.path()).expect("journal opens");
    let original = command("command-1", 1, "play");

    // When
    let first = journal
        .accept(original.clone())
        .expect("first command is durable");
    let duplicate = journal.accept(original).expect("duplicate is classified");
    let conflict = journal.accept(command("command-1", 1, "stop"));

    // Then
    assert_eq!(first, CommandDecision::Execute);
    assert_eq!(duplicate, CommandDecision::Duplicate);
    assert!(matches!(
        conflict,
        Err(jastreamer_renderer::harness::JournalError::CommandConflict { .. })
    ));
}

#[test]
fn journal_persists_redacted_results_but_never_bearer() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let secret = SecretToken::parse("renderer-bearer-sentinel\n").expect("token parses");
    let mut journal = DurableJournal::open(directory.path()).expect("journal opens");

    // When
    journal
        .accept(command("command-1", 1, "play"))
        .expect("command accepted");
    journal
        .complete(
            "command-1",
            "result-1",
            "succeeded",
            Some("playing"),
            Some(0),
        )
        .expect("result persisted");
    drop(secret);
    drop(journal);

    // Then
    let persisted =
        fs::read_to_string(directory.path().join("journal.json")).expect("journal readable");
    assert!(persisted.contains("command-1"));
    assert!(persisted.contains("result-1"));
    assert!(!persisted.contains("renderer-bearer-sentinel"));
}

#[test]
fn one_process_holds_each_state_directory_lock() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let first = DurableJournal::open(directory.path()).expect("first journal opens");

    // When
    let second = DurableJournal::open(directory.path());

    // Then
    assert!(matches!(
        second,
        Err(jastreamer_renderer::harness::JournalError::StateDirectoryBusy)
    ));
    drop(first);
}

#[test]
fn reconnect_replays_pending_result_until_result_ack_is_durable() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let mut journal = DurableJournal::open(directory.path()).expect("journal opens");
    journal
        .accept(command("command-1", 1, "play"))
        .expect("command accepted");
    journal
        .complete(
            "command-1",
            "result-1",
            "succeeded",
            Some("playing"),
            Some(0),
        )
        .expect("result persisted");
    drop(journal);

    // When
    let mut reopened = DurableJournal::open(directory.path()).expect("journal reopens");
    let replay = reopened.pending_results();
    reopened
        .acknowledge_result("result-1")
        .expect("ack persists");
    drop(reopened);
    let final_state = DurableJournal::open(directory.path()).expect("journal reopens after ack");

    // Then
    assert_eq!(replay.len(), 1);
    assert_eq!(replay[0].result_id, "result-1");
    assert!(final_state.pending_results().is_empty());
    assert!(final_state.unfinished_commands().is_empty());
    assert!(final_state.result_for_command("command-1").is_some());
}

#[test]
fn accepted_without_result_survives_restart_for_matching_redelivery() {
    // Given
    let directory = tempfile::tempdir().expect("temporary state directory");
    let original = command("command-1", 1, "play");
    let mut journal = DurableJournal::open(directory.path()).expect("journal opens");
    journal.accept(original.clone()).expect("command accepted");
    drop(journal);

    // When
    let mut reopened = DurableJournal::open(directory.path()).expect("journal reopens");
    let decision = reopened.accept(original).expect("redelivery classified");
    let pending = reopened
        .accepted_command("command-1")
        .expect("accepted payload is recoverable");

    // Then
    assert_eq!(decision, CommandDecision::Duplicate);
    assert_eq!(pending.command_id, "command-1");
    assert!(reopened.result_for_command("command-1").is_none());
}

#[test]
fn token_is_trimmed_and_always_redacted_from_debug() {
    // Given / When
    let token = SecretToken::parse("  bearer-value  \r\n").expect("token parses");

    // Then
    assert_eq!(token.expose(), "bearer-value");
    assert_eq!(format!("{token:?}"), "SecretToken([REDACTED])");
}
