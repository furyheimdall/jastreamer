part of 'control_models.dart';

final class QueueEntryView {
  const QueueEntryView({required this.trackId, required this.state});
  final String trackId;
  final String state;
  bool get isUnavailableHead => state == 'blocked';
}

final class QueueView {
  const QueueView({required this.revision, required this.entries});
  final int revision;
  final List<QueueEntryView> entries;
}

final class PlaybackQueueEntry {
  const PlaybackQueueEntry({
    required this.id,
    required this.trackId,
    required this.state,
    required this.position,
    required this.blockedError,
  });
  final QueueEntryId id;
  final TrackId trackId;
  final WireValue<QueueEntryState> state;
  final int? position;
  final ControlFailure? blockedError;
}

final class PlaybackState {
  const PlaybackState({
    required this.zoneId,
    required this.revision,
    required this.etag,
    required this.logicalTransport,
    required this.observedTransport,
    required this.pendingCommandId,
    required this.entries,
  });
  final ZoneId zoneId;
  final int revision;
  final EntityTag etag;
  final WireValue<LogicalTransport> logicalTransport;
  final WireValue<ObservedTransport> observedTransport;
  final CommandId? pendingCommandId;
  final List<PlaybackQueueEntry> entries;
}

final class RendererView {
  const RendererView({
    required this.id,
    required this.name,
    required this.kind,
    required this.status,
    required this.capabilities,
    required this.lastSeenAt,
  });
  final RendererId id;
  final String name;
  final WireValue<RendererKind> kind;
  final WireValue<RendererStatus> status;
  final List<String> capabilities;
  final DateTime lastSeenAt;
}

final class ZoneView {
  const ZoneView({
    required this.id,
    required this.name,
    required this.revision,
    required this.rendererId,
    required this.transport,
  });
  final ZoneId id;
  final String name;
  final int revision;
  final RendererId? rendererId;
  final WireValue<LogicalTransport> transport;
}

final class ZonesSnapshot {
  const ZonesSnapshot({required this.zones, required this.renderers});
  final List<ZoneView> zones;
  final List<RendererView> renderers;
}
