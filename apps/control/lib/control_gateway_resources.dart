part of 'control_gateway.dart';

extension HttpControlGatewayResources on HttpControlGateway {
  Future<PolicyView> policy([ZoneId zoneId = const ZoneId('main')]) async =>
      parsePolicy(
        _object(
          await _get(
            '/api/v1/zones/${_segment(zoneId.value)}/continuation-policy',
          ),
        ),
      );

  Future<PolicyView> updatePolicy(
    PolicyWrite write,
    int expectedRevision, {
    ZoneId zoneId = const ZoneId('main'),
  }) async {
    final response = await _send(
      'PATCH',
      '/api/v1/zones/${_segment(zoneId.value)}/continuation-policy',
      headers: {'if-match': '$expectedRevision'},
      body: {
        'mode': write.mode,
        'artist_gap': write.artistGap,
        'album_gap': write.albumGap,
        'session_override': write.sessionOverride,
      },
    );
    if (response.statusCode == 412) {
      throw StalePolicyFailure(
        serverRevision: EntityTag.parse(response.headers['etag'])?.revision ??
            expectedRevision,
      );
    }
    return parsePolicy(_object(response));
  }

  Future<CatalogView> catalog() async {
    final body = _object(await _get('/api/v1/catalog/status'));
    return CatalogView(
      revision: requiredInteger(body, 'catalog_revision'),
      trackCount: requiredInteger(body, 'track_count'),
      complete: requiredInteger(body, 'analysis_complete'),
      queued: requiredInteger(body, 'analysis_queued'),
      failed: requiredInteger(body, 'analysis_failed'),
      coverage: requiredInteger(body, 'analysis_coverage'),
    );
  }

  Future<CatalogPage> browseCatalog({
    String? query,
    CatalogCursor? cursor,
    int limit = 100,
  }) async {
    if (limit < 1 || limit > 500) {
      throw const FormatException('limit must be between 1 and 500');
    }
    final uri = origin.resolve('/api/v1/catalog/tracks').replace(
      queryParameters: {
        if (query != null && query.isNotEmpty) 'query': query,
        if (cursor != null) 'cursor': cursor.value,
        'limit': '$limit',
      },
    );
    final body = _object(await _getUri(uri));
    final tracks = requiredObjects(
      body,
      'tracks',
    ).map(_parseCatalogTrack).toList(growable: false);
    final next = optionalString(body, 'next_cursor');
    return CatalogPage(
      revision: requiredInteger(body, 'catalog_revision'),
      tracks: tracks,
      nextCursor: next == null || next.isEmpty ? null : CatalogCursor(next),
    );
  }

  Future<ZonesSnapshot> zones() async =>
      parseZonesSnapshot(_object(await _get('/api/v1/zones')));

  Future<QueueView> queue([ZoneId zoneId = const ZoneId('main')]) async {
    final body = _object(
      await _get('/api/v1/zones/${_segment(zoneId.value)}/queue'),
    );
    return QueueView(
      revision: requiredInteger(body, 'revision'),
      entries: requiredObjects(body, 'queue')
          .map(
            (entry) => QueueEntryView(
              trackId: requiredString(entry, 'track_id'),
              state: requiredString(entry, 'state'),
            ),
          )
          .toList(growable: false),
    );
  }

  Future<PlaybackState> playbackState(ZoneId zoneId) async {
    final response = await _get(
      '/api/v1/zones/${_segment(zoneId.value)}/playback-state',
    );
    return _parsePlaybackState(_object(response), response.headers['etag']);
  }
}
