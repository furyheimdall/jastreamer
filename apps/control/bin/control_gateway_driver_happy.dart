part of 'control_gateway_driver.dart';

Future<void> _happy(HttpControlGateway gateway, ZoneId zoneId) async {
  final discovery = await gateway.discovery();
  final catalog = await gateway.browseCatalog(limit: 10);
  if (catalog.revision <= 0 || catalog.tracks.length < 2) {
    throw StateError('The production Server catalog needs two fixture tracks.');
  }
  final query = catalog.tracks.first.title.split(' ').first;
  final search = await gateway.browseCatalog(query: query, limit: 10);
  if (search.tracks.isEmpty || search.revision != catalog.revision) {
    throw StateError('Production catalog search did not return the fixture.');
  }

  final events = await gateway.subscribe(watchedZones: {zoneId});
  final updates = _PlaybackUpdates(events);
  final transportSignals =
      Platform.environment['JASTREAMER_TRANSPORT_SIGNAL'] == 'stdin'
          ? StreamIterator(
              stdin.transform(utf8.decoder).transform(const LineSplitter()),
            )
          : null;
  try {
    final initial = await gateway.playbackState(zoneId);
    final appended = await gateway.mutateQueue(
      zoneId: zoneId,
      expectedRevision: initial.revision,
      idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
      intent: QueueMutationIntent.append(
        trackIds: [
          catalog.tracks.first.id,
          catalog.tracks[1].id,
          catalog.tracks.first.id,
        ],
      ),
      subscription: events,
    );
    var confirmed = await updates.wait(
      ResourceKind.queue,
      minimumRevision: appended.revision,
    );
    if (confirmed.entries.length != 3) {
      throw StateError('Append was not confirmed with three queue entries.');
    }

    final moved = await gateway.mutateQueue(
      zoneId: zoneId,
      expectedRevision: confirmed.revision,
      idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
      intent: QueueMutationIntent.move(
        confirmed.entries.last.id,
        beforeEntryId: confirmed.entries.first.id,
      ),
      subscription: events,
    );
    confirmed = await updates.wait(
      ResourceKind.queue,
      minimumRevision: moved.revision,
    );

    final removed = await gateway.mutateQueue(
      zoneId: zoneId,
      expectedRevision: confirmed.revision,
      idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
      intent: QueueMutationIntent.remove(confirmed.entries.last.id),
      subscription: events,
    );
    confirmed = await updates.wait(
      ResourceKind.queue,
      minimumRevision: removed.revision,
    );
    if (confirmed.entries.length != 2) {
      throw StateError('Remove was not confirmed with two queue entries.');
    }

    final started = await gateway.mutateTransport(
      zoneId: zoneId,
      expectedRevision: confirmed.revision,
      idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
      intent: const TransportMutationIntent.start(),
      subscription: events,
    );
    confirmed = await updates.wait(
      ResourceKind.transport,
      minimumRevision: started.revision,
    );
    if (transportSignals != null) {
      stdout.writeln(
        jsonEncode({
          'ready': 'play-terminal',
          'play_revision': started.revision,
        }),
      );
      if (!await transportSignals.moveNext().timeout(
        const Duration(seconds: 30),
      )) {
        throw StateError('Play terminal signal stream closed.');
      }
      confirmed = await gateway.playbackState(zoneId);
    }
    if (confirmed.logicalTransport.known != LogicalTransport.playing ||
        confirmed.observedTransport.known != ObservedTransport.playing ||
        confirmed.pendingCommandId != null) {
      throw StateError('Fixture Renderer did not confirm play.');
    }

    final paused = await gateway.mutateTransport(
      zoneId: zoneId,
      expectedRevision: confirmed.revision,
      idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
      intent: const TransportMutationIntent.pause(),
      subscription: events,
    );
    confirmed = await updates.wait(
      ResourceKind.transport,
      minimumRevision: paused.revision,
    );
    if (transportSignals != null) {
      stdout.writeln(
        jsonEncode({
          'ready': 'pause-terminal',
          'pause_revision': paused.revision,
        }),
      );
      if (!await transportSignals.moveNext().timeout(
        const Duration(seconds: 30),
      )) {
        throw StateError('Pause terminal signal stream closed.');
      }
      confirmed = await gateway.playbackState(zoneId);
    }

    const staleIntent = QueueMutationIntent.clear();
    final beforeStale = confirmed;
    var staleAttempts = 0;
    StaleRevisionFailure? stale;
    try {
      staleAttempts++;
      await gateway.mutateQueue(
        zoneId: zoneId,
        expectedRevision: initial.revision,
        idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
        intent: staleIntent,
        subscription: events,
      );
    } on StaleRevisionFailure catch (failure) {
      stale = failure;
    }
    if (stale == null || !identical(stale.intent, staleIntent)) {
      throw StateError(
        'Stale revision did not preserve the exact user intent.',
      );
    }
    final afterStale = await gateway.playbackState(zoneId);
    if (afterStale.revision != beforeStale.revision || staleAttempts != 1) {
      throw StateError('Stale mutation retried or changed Server state.');
    }
    confirmed = afterStale;

    stdout.writeln(
      jsonEncode({
        'scenario': 'happy-and-stale',
        'protocol_major': discovery.protocolMajor,
        'catalog_revision': catalog.revision,
        'catalog_tracks': catalog.tracks.length,
        'search_tracks': search.tracks.length,
        'zone_id': zoneId.value,
        'append_revision': appended.revision,
        'move_revision': moved.revision,
        'remove_revision': removed.revision,
        'play_revision': started.revision,
        'pause_revision': paused.revision,
        'queue_revision': confirmed.revision,
        'queue_entries': confirmed.entries.length,
        'logical_transport': confirmed.logicalTransport.wireValue,
        'observed_transport': confirmed.observedTransport.wireValue,
        'pending_command': confirmed.pendingCommandId?.value,
        'subscribed_before_mutation': true,
        'matching_invalidation_refetched': true,
        'stale': {
          'code': stale.code,
          'intent_preserved': identical(stale.intent, staleIntent),
          'attempts': staleAttempts,
          'revision_unchanged': afterStale.revision == beforeStale.revision,
        },
      }),
    );
  } finally {
    await transportSignals?.cancel();
    await updates.close();
    await events.close();
  }
}
