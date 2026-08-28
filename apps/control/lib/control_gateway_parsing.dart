part of 'control_gateway.dart';

CatalogTrack _parseCatalogTrack(Map<String, Object?> body) {
  final artist = optionalString(body, 'artist');
  final artists = body.containsKey('artists')
      ? requiredStrings(body, 'artists')
      : <String>[if (artist != null && artist.isNotEmpty) artist];
  final rawRepresentations = body['representations'];
  return CatalogTrack(
    id: TrackId(requiredString(body, 'track_id')),
    title: requiredString(body, 'title'),
    artists: artists,
    album: requiredString(body, 'album'),
    albumArtist: optionalString(body, 'album_artist'),
    durationMs: optionalInteger(body, 'duration_ms'),
    available: body.containsKey('available')
        ? requiredBoolean(body, 'available')
        : true,
    representations: rawRepresentations == null
        ? const []
        : requiredObjects(
            body,
            'representations',
          ).map(_parseRepresentation).toList(growable: false),
  );
}

MediaRepresentation _parseRepresentation(Map<String, Object?> body) =>
    MediaRepresentation(
      id: requiredString(body, 'representation_id'),
      kind: parseWireValue(
        requiredString(body, 'kind'),
        MediaRepresentationKind.values,
      ),
      mimeType: requiredString(body, 'mime_type'),
      codec: optionalString(body, 'codec'),
      sampleRateHz: optionalInteger(body, 'sample_rate_hz'),
      channels: optionalInteger(body, 'channels'),
      bitsPerSample: optionalInteger(body, 'bits_per_sample'),
      seekable: requiredBoolean(body, 'seekable'),
    );

PlaybackState _parsePlaybackState(Map<String, Object?> body, String? etag) {
  final revision = requiredInteger(body, 'revision');
  final pending = optionalString(body, 'pending_command_id');
  return PlaybackState(
    zoneId: ZoneId(requiredString(body, 'zone_id')),
    revision: revision,
    etag: EntityTag.parse(etag) ?? EntityTag(revision),
    logicalTransport: parseWireValue(
      requiredString(body, 'transport'),
      LogicalTransport.values,
    ),
    observedTransport: parseWireValue(
      optionalString(body, 'observed_transport') ?? 'unknown',
      ObservedTransport.values,
    ),
    pendingCommandId:
        pending == null || pending.isEmpty ? null : CommandId(pending),
    entries:
        requiredObjects(body, body.containsKey('queue') ? 'queue' : 'entries')
            .map((entry) {
      final blocked = entry['blocked_error'];
      return PlaybackQueueEntry(
        id: QueueEntryId(requiredString(entry, 'entry_id')),
        trackId: TrackId(requiredString(entry, 'track_id')),
        state: parseWireValue(
          requiredString(entry, 'state'),
          QueueEntryState.values,
        ),
        position: optionalInteger(entry, 'position'),
        blockedError: blocked is Map<String, Object?>
            ? _failureFromBody(0, blocked)
            : null,
      );
    }).toList(growable: false),
  );
}

RendererId? _optionalRendererId(Map<String, Object?> body, String key) {
  final value = optionalString(body, key);
  return value == null || value.isEmpty ? null : RendererId(value);
}

String _segment(String value) => Uri.encodeComponent(value);

Map<String, Object?> _object(http.Response response, {MutationIntent? intent}) {
  Object? decoded;
  try {
    decoded = jsonDecode(response.body);
  } on FormatException {
    throw FormatException(
      'response must contain JSON (HTTP ${response.statusCode})',
    );
  }
  if (decoded is! Map<String, Object?>) {
    throw const FormatException('response must be an object');
  }
  if (response.statusCode < 200 || response.statusCode >= 300) {
    throw _failureFromBody(
      response.statusCode,
      decoded,
      response: response,
      intent: intent,
    );
  }
  return decoded;
}

ControlFailure _failureFromBody(
  int status,
  Map<String, Object?> body, {
  http.Response? response,
  MutationIntent? intent,
}) {
  final code = requiredString(body, 'code');
  // Server-provided error text is not diagnostic-safe: a peer could reflect
  // Authorization material. Stable machine codes are sufficient here.
  final message =
      code == 'TOKEN_REVOKED' ? 'Controller credential was revoked.' : code;
  if (code == 'TOKEN_REVOKED') return TokenRevokedFailure(message: message);
  if (code == 'STALE_REVISION' && intent != null) {
    return StaleRevisionFailure(
      status: status,
      message: message,
      serverRevision: EntityTag.parse(response?.headers['etag'])?.revision,
      intent: intent,
    );
  }
  if (code == 'RENDERER_OFFLINE' && intent != null) {
    return RendererOfflineFailure(message: message, intent: intent);
  }
  return ControlApiFailure(
    status: status,
    code: code,
    message: message,
    recoverable: const {
      'UNAUTHORIZED',
      'CATALOG_REVISION_CHANGED',
      'ZONE_ACTIVE',
      'UNSUPPORTED_CAPABILITY',
      'INVALID_STATE',
      'QUEUE_EMPTY',
      'EVENT_TICKET_EXPIRED',
      'RESYNC_REQUIRED',
    }.contains(code),
    details: const <String, Object?>{},
    intent: intent,
  );
}

ControlFailure _networkFailure(Object error, {MutationIntent? intent}) {
  if (error is ControlFailure) return error;
  final lower = error.toString().toLowerCase();
  if (lower.contains('certificate') || lower.contains('handshake')) {
    return const CertificateIdentityChangedFailure(
      message: 'Server certificate identity changed.',
    );
  }
  return ServerOfflineFailure(
    message: 'Server is unavailable.',
    intent: intent,
  );
}
