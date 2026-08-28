part of 'control_gateway.dart';

extension HttpControlGatewayMutations on HttpControlGateway {
  Future<QueueMutationResult> mutateQueue({
    required ZoneId zoneId,
    required int expectedRevision,
    required IdempotencyKey idempotencyKey,
    required QueueMutationIntent intent,
    ControlLiveSession? subscription,
  }) async {
    _requireSubscription(subscription);
    final response = await _mutation(
      path: '/api/v1/zones/${_segment(zoneId.value)}/queue',
      expectedRevision: expectedRevision,
      idempotencyKey: idempotencyKey,
      intent: intent,
    );
    final body = _object(response, intent: intent);
    final revision = requiredInteger(body, 'revision');
    return QueueMutationResult(
      revision: revision,
      etag: EntityTag.parse(response.headers['etag']) ?? EntityTag(revision),
      entryIds: body['entry_ids'] == null
          ? const []
          : requiredStrings(
              body,
              'entry_ids',
            ).map(QueueEntryId.new).toList(growable: false),
    );
  }

  Future<TransportMutationResult> mutateTransport({
    required ZoneId zoneId,
    required int expectedRevision,
    required IdempotencyKey idempotencyKey,
    required TransportMutationIntent intent,
    ControlLiveSession? subscription,
  }) async {
    _requireSubscription(subscription);
    if (intent.positionMs case final value? when value < 0) {
      throw const FormatException('positionMs must be non-negative');
    }
    final response = await _mutation(
      path: '/api/v1/zones/${_segment(zoneId.value)}/transport',
      expectedRevision: expectedRevision,
      idempotencyKey: idempotencyKey,
      intent: intent,
    );
    final body = _object(response, intent: intent);
    final revision = requiredInteger(body, 'revision');
    return TransportMutationResult(
      revision: revision,
      etag: EntityTag.parse(response.headers['etag']) ?? EntityTag(revision),
      commandId: CommandId(requiredString(body, 'command_id')),
      status: parseWireValue(
        requiredString(body, 'status'),
        TransportMutationStatus.values,
      ),
    );
  }

  Future<RendererAssignmentResult> assignRenderer({
    required ZoneId zoneId,
    required int expectedRevision,
    required IdempotencyKey idempotencyKey,
    required RendererAssignmentIntent intent,
    ControlLiveSession? subscription,
  }) async {
    _requireSubscription(subscription);
    final response = await _send(
      'PUT',
      '/api/v1/zones/${_segment(zoneId.value)}/renderer',
      headers: {
        'if-match': '$expectedRevision',
        'idempotency-key': idempotencyKey.value,
      },
      body: intent.toJson(),
      intent: intent,
    );
    final body = _object(response, intent: intent);
    final revision = requiredInteger(body, 'revision');
    return RendererAssignmentResult(
      zoneId: ZoneId(requiredString(body, 'zone_id')),
      rendererId: _optionalRendererId(body, 'renderer_id'),
      revision: revision,
      etag: EntityTag.parse(response.headers['etag']) ?? EntityTag(revision),
    );
  }

  Future<EventTicket> eventTicket() async {
    final body = _object(
      await _send('POST', '/api/v1/event-tickets', body: const {}),
    );
    return EventTicket(
      value: requiredString(body, 'ticket'),
      expiresAt: DateTime.parse(requiredString(body, 'expires_at')).toUtc(),
    );
  }

  Future<ControlLiveSession> subscribe({
    Set<ZoneId> watchedZones = const {ZoneId('main')},
    int maxFullResyncs = 3,
  }) async {
    final ticket = await eventTicket();
    final eventUri = origin.replace(
      scheme: origin.scheme == 'https' ? 'wss' : 'ws',
      path: '/api/v1/events',
      queryParameters: {'ticket': ticket.value},
    );
    try {
      final socket = await _eventSocketFactory.connect(
        uri: eventUri,
        certificateSha256: certificateSha256,
      );
      final session = ControlLiveSession._(
        gateway: this,
        socket: socket,
        watchedZones: watchedZones,
        maxFullResyncs: maxFullResyncs,
      );
      _subscriptions.add(session);
      await session.ready;
      return session;
    } catch (error) {
      if (error is ControlFailure || error is FormatException) rethrow;
      throw _networkFailure(error);
    }
  }
}
