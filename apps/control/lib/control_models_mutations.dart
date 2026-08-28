part of 'control_models.dart';

sealed class MutationIntent {
  const MutationIntent();
  Map<String, Object?> toJson();
}

enum QueueMutationCommand {
  append,
  insert,
  remove,
  move,
  clear,
  retryBlocked,
  skipBlocked,
}

final class QueueMutationIntent extends MutationIntent {
  const QueueMutationIntent._({
    required this.command,
    this.trackIds = const [],
    this.entryId,
    this.beforeEntryId,
  });
  const QueueMutationIntent.append({required List<TrackId> trackIds})
      : this._(command: QueueMutationCommand.append, trackIds: trackIds);
  const QueueMutationIntent.insert({
    required List<TrackId> trackIds,
    QueueEntryId? beforeEntryId,
  }) : this._(
          command: QueueMutationCommand.insert,
          trackIds: trackIds,
          beforeEntryId: beforeEntryId,
        );
  const QueueMutationIntent.remove(QueueEntryId entryId)
      : this._(command: QueueMutationCommand.remove, entryId: entryId);
  const QueueMutationIntent.move(
    QueueEntryId entryId, {
    QueueEntryId? beforeEntryId,
  }) : this._(
          command: QueueMutationCommand.move,
          entryId: entryId,
          beforeEntryId: beforeEntryId,
        );
  const QueueMutationIntent.clear()
      : this._(command: QueueMutationCommand.clear);
  const QueueMutationIntent.retryBlocked(QueueEntryId entryId)
      : this._(command: QueueMutationCommand.retryBlocked, entryId: entryId);
  const QueueMutationIntent.skipBlocked(QueueEntryId entryId)
      : this._(command: QueueMutationCommand.skipBlocked, entryId: entryId);

  final QueueMutationCommand command;
  final List<TrackId> trackIds;
  final QueueEntryId? entryId;
  final QueueEntryId? beforeEntryId;

  @override
  Map<String, Object?> toJson() => <String, Object?>{
        'command': switch (command) {
          QueueMutationCommand.retryBlocked => 'retry_blocked',
          QueueMutationCommand.skipBlocked => 'skip_blocked',
          _ => command.name,
        },
        if (trackIds.isNotEmpty)
          'track_ids': trackIds.map((id) => id.value).toList(growable: false),
        if (entryId case final value?) 'entry_id': value.value,
        if (command == QueueMutationCommand.move ||
            command == QueueMutationCommand.insert)
          'before_entry_id': beforeEntryId?.value,
      };
}

enum TransportCommand { start, pause, resume, stop, skip, previous, seek }

final class TransportMutationIntent extends MutationIntent {
  const TransportMutationIntent._(this.command, this.positionMs);
  const TransportMutationIntent.start() : this._(TransportCommand.start, null);
  const TransportMutationIntent.pause() : this._(TransportCommand.pause, null);
  const TransportMutationIntent.resume()
      : this._(TransportCommand.resume, null);
  const TransportMutationIntent.stop() : this._(TransportCommand.stop, null);
  const TransportMutationIntent.skip() : this._(TransportCommand.skip, null);
  const TransportMutationIntent.previous()
      : this._(TransportCommand.previous, null);
  const TransportMutationIntent.seek(int positionMs)
      : this._(TransportCommand.seek, positionMs);

  final TransportCommand command;
  final int? positionMs;

  @override
  Map<String, Object?> toJson() => <String, Object?>{
        'command': command.name,
        if (positionMs case final value?) 'position_ms': value,
      };
}

final class RendererAssignmentIntent extends MutationIntent {
  const RendererAssignmentIntent(this.rendererId);
  final RendererId? rendererId;
  @override
  Map<String, Object?> toJson() => {'renderer_id': rendererId?.value};
}

final class QueueMutationResult {
  const QueueMutationResult({
    required this.revision,
    required this.etag,
    required this.entryIds,
  });
  final int revision;
  final EntityTag etag;
  final List<QueueEntryId> entryIds;
}

final class TransportMutationResult {
  const TransportMutationResult({
    required this.revision,
    required this.etag,
    required this.commandId,
    required this.status,
  });
  final int revision;
  final EntityTag etag;
  final CommandId commandId;
  final WireValue<TransportMutationStatus> status;
}

final class RendererAssignmentResult {
  const RendererAssignmentResult({
    required this.zoneId,
    required this.rendererId,
    required this.revision,
    required this.etag,
  });
  final ZoneId zoneId;
  final RendererId? rendererId;
  final int revision;
  final EntityTag etag;
}

final class EventTicket {
  const EventTicket({required this.value, required this.expiresAt});
  final String value;
  final DateTime expiresAt;
}
