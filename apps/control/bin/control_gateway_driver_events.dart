part of 'control_gateway_driver.dart';

Future<void> _eventGap(HttpControlGateway gateway, ZoneId zoneId) async {
  final catalog = await gateway.browseCatalog(limit: 1);
  if (catalog.tracks.isEmpty) throw StateError('Catalog is empty.');
  final events = await gateway.subscribe(watchedZones: {zoneId});
  final updates = _PlaybackUpdates(events);
  try {
    final initial = await gateway.playbackState(zoneId);
    final first = await gateway.mutateQueue(
      zoneId: zoneId,
      expectedRevision: initial.revision,
      idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
      intent: QueueMutationIntent.append(trackIds: [catalog.tracks.first.id]),
      subscription: events,
    );
    final second = await gateway.mutateQueue(
      zoneId: zoneId,
      expectedRevision: first.revision,
      idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
      intent: QueueMutationIntent.append(trackIds: [catalog.tracks.first.id]),
      subscription: events,
    );
    final state = await updates.wait(
      ResourceKind.queue,
      minimumRevision: second.revision,
    );
    if (events.fullResyncCount != 1 || state.entries.length < 2) {
      throw StateError('Sequence gap did not cause one bounded full resync.');
    }
    stdout.writeln(
      jsonEncode({
        'scenario': 'event-sequence-gap',
        'first_revision': first.revision,
        'second_revision': second.revision,
        'refetched_revision': state.revision,
        'full_resyncs': events.fullResyncCount,
        'bounded': events.fullResyncCount == 1,
      }),
    );
  } finally {
    await updates.close();
    await events.close();
  }
}
Future<void> _unknownEnum(HttpControlGateway gateway) async {
  var rejected = false;
  try {
    await gateway.zones();
  } on FormatException {
    rejected = true;
  }
  if (!rejected) {
    throw StateError('Incompatible zone enum was accepted.');
  }
  stdout.writeln(
    jsonEncode({
      'scenario': 'unknown-enum',
      'incompatible_zone_rejected': true,
      'mutations': 0,
    }),
  );
}
