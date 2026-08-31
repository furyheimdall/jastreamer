import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/io_client.dart';
import 'package:jastreamer_control/control_events.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/control_models.dart';

void main() {
  group('v3 typed HTTP boundary', () {
    late HttpServer server;
    late HttpControlGateway gateway;
    final requests = <_RecordedRequest>[];
    var queueRevision = 4;
    var revoked = false;
    var validZones = false;

    setUp(() async {
      requests.clear();
      queueRevision = 4;
      revoked = false;
      validZones = false;
      server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      unawaited(
        server.forEach((request) async {
          final body = await utf8.decoder.bind(request).join();
          requests.add(
            _RecordedRequest(
              request.method,
              request.uri,
              request.headers,
              body,
            ),
          );
          request.response.headers.contentType = ContentType.json;
          if (revoked) {
            request.response.statusCode = HttpStatus.unauthorized;
            request.response.write(
              jsonEncode({'code': 'TOKEN_REVOKED', 'message': 'revoked'}),
            );
          } else if (request.uri.path == '/api/v1/event-tickets') {
            request.response.statusCode = HttpStatus.created;
            request.response.write(
              jsonEncode({
                'ticket': 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ',
                'expires_at': '2026-08-26T00:01:00Z',
              }),
            );
          } else if (request.uri.path == '/api/v1/catalog/tracks') {
            request.response.write(
              jsonEncode({
                'catalog_revision': 7,
                'next_cursor': 'next opaque+/=',
                'tracks': [
                  {
                    'track_id': 'track-1',
                    'title': 'Tone',
                    'artist': 'Fixture',
                    'album': 'Contracts',
                    'album_artist': null,
                    'duration_ms': 2000,
                    'available': true,
                    'representations': [
                      {
                        'representation_id': 'future',
                        'kind': 'future-lossless',
                        'mime_type': 'audio/future',
                        'codec': null,
                        'sample_rate_hz': null,
                        'channels': null,
                        'bits_per_sample': null,
                        'seekable': true,
                      },
                    ],
                  },
                ],
              }),
            );
          } else if (request.uri.path == '/api/v1/catalog/status') {
            request.response.write(
              jsonEncode({
                'scan_status': 'ready',
                'catalog_revision': 7,
                'track_count': 1,
                'analysis_complete': 1,
                'analysis_queued': 0,
                'analysis_failed': 0,
                'analysis_coverage': 100,
                'analysis_revision': 1,
              }),
            );
          } else if (request.uri.path.endsWith('/continuation-policy')) {
            request.response.write(
              jsonEncode({
                'mode': 'stop',
                'artist_gap': 4,
                'album_gap': 10,
                'session_override': '',
                'revision': 7,
              }),
            );
          } else if (request.uri.path == '/api/v1/zones') {
            request.response.write(
              jsonEncode({
                'zones': [
                  {
                    'zone_id': 'main',
                    'name': 'Main',
                    'revision': 2,
                    'renderer_id': 'r1',
                    'transport': validZones ? 'idle' : 'future-logical',
                  },
                ],
                'renderers': [
                  {
                    'renderer_id': 'r1',
                    'name': 'Renderer',
                    'kind': validZones ? 'k17' : 'future-kind',
                    'status': validZones ? 'available' : 'future-status',
                    'capabilities': ['seek'],
                    'last_seen_at': '2026-08-26T00:00:00Z',
                  },
                ],
              }),
            );
          } else if (request.uri.path.endsWith('/queue') &&
                  request.method == 'GET' ||
              request.uri.path.endsWith('/playback-state')) {
            request.response.write(
              jsonEncode({
                'zone_id': 'main',
                'revision': queueRevision,
                'transport': 'playing',
                'observed_transport': 'future-observed',
                'pending_command_id': 'command-1',
                'queue': [
                  {
                    'entry_id': 'entry-1',
                    'track_id': 'track-1',
                    'state': 'future-state',
                    'position': 0,
                  },
                ],
              }),
            );
          } else if (request.uri.path.endsWith('/queue')) {
            if (request.headers.value('if-match') != '$queueRevision') {
              request.response.statusCode = HttpStatus.conflict;
              request.response.headers.set('etag', '"$queueRevision"');
              request.response.write(
                jsonEncode({'code': 'STALE_REVISION', 'message': 'stale'}),
              );
            } else {
              queueRevision++;
              request.response.statusCode = HttpStatus.created;
              request.response.headers.set('etag', '"$queueRevision"');
              request.response.write(
                jsonEncode({
                  'revision': queueRevision,
                  'entry_ids': ['entry-2'],
                }),
              );
            }
          } else if (request.uri.path.endsWith('/transport')) {
            if (request.headers.value('idempotency-key') == 'start-1') {
              request.response.statusCode = HttpStatus.conflict;
              request.response.write(
                jsonEncode({'code': 'RENDERER_OFFLINE', 'message': 'offline'}),
              );
            } else {
              queueRevision++;
              request.response.statusCode = HttpStatus.accepted;
              request.response.headers.set('etag', '"$queueRevision"');
              request.response.write(
                jsonEncode({
                  'revision': queueRevision,
                  'command_id': 'command-$queueRevision',
                  'status': 'pending',
                }),
              );
            }
          } else if (request.uri.path.endsWith('/renderer') &&
              request.method == 'PUT') {
            queueRevision++;
            request.response.headers.set('etag', '"$queueRevision"');
            final assignment = jsonDecode(body) as Map<String, Object?>;
            request.response.write(
              jsonEncode({
                'zone_id': 'main',
                'renderer_id': assignment['renderer_id'],
                'revision': queueRevision,
              }),
            );
          } else {
            request.response.statusCode = HttpStatus.notFound;
            request.response.write(
              jsonEncode({'code': 'NOT_FOUND', 'message': 'missing'}),
            );
          }
          await request.response.close();
        }),
      );
      gateway = HttpControlGateway(
        client: IOClient(),
        origin: Uri.parse('http://${server.address.address}:${server.port}'),
        token: const SessionToken('secret'),
        eventSocketFactory: const _SnapshotSocketFactory(),
      );
    });

    tearDown(() async {
      await gateway.close();
      await server.close(force: true);
    });

    test(
      'serializes catalog cursors and rejects incompatible zone enums',
      () async {
        final page = await gateway.browseCatalog(
          query: 'cafe & jazz',
          cursor: const CatalogCursor('opaque+/='),
          limit: 25,
        );
        expect(requests.single.uri.queryParameters, {
          'query': 'cafe & jazz',
          'cursor': 'opaque+/=',
          'limit': '25',
        });
        expect(page.nextCursor?.value, 'next opaque+/=');
        expect(page.tracks.single.representations.single.kind.known, isNull);
        expect(
          page.tracks.single.representations.single.kind.wireValue,
          'future-lossless',
        );

        await expectLater(gateway.zones(), throwsFormatException);
      },
    );

    test('buffers events emitted before the caller attaches its listener',
        () async {
      await gateway.close();
      validZones = true;
      final sockets = _PreReadyEventSocketFactory();
      gateway = HttpControlGateway(
        client: IOClient(),
        origin: Uri.parse('http://${server.address.address}:${server.port}'),
        token: const SessionToken('secret'),
        eventSocketFactory: sockets,
      );
      final session = await gateway.subscribe();

      final events = await session.events.take(2).toList().timeout(
            const Duration(seconds: 1),
          );

      expect(events, [
        isA<ControlSnapshotEvent>(),
        isA<ControlInvalidationEvent>(),
      ]);
      await session.close();
    });

    test('closes the event socket when initial readiness fails', () async {
      await gateway.close();
      final sockets = _MalformedSocketFactory();
      gateway = HttpControlGateway(
        client: IOClient(),
        origin: Uri.parse('http://${server.address.address}:${server.port}'),
        token: const SessionToken('secret'),
        eventSocketFactory: sockets,
      );

      await expectLater(gateway.subscribe(), throwsFormatException);

      expect(sockets.socket.closed, isTrue);
    });

    test('surfaces an established event stream close for recovery', () async {
      await gateway.close();
      final sockets = _ClosingSocketFactory();
      gateway = HttpControlGateway(
        client: IOClient(),
        origin: Uri.parse('http://${server.address.address}:${server.port}'),
        token: const SessionToken('secret'),
        eventSocketFactory: sockets,
      );
      final session = await gateway.subscribe();
      final failure = expectLater(
        session.events,
        emitsInOrder([
          isA<ControlSnapshotEvent>(),
          emitsError(isA<ServerOfflineFailure>()),
        ]),
      );

      await sockets.socket.closeFromServer();

      await failure.timeout(const Duration(seconds: 1));
    });

    test('gateway close awaits every event subscription shutdown', () async {
      await gateway.close();
      final sockets = _DelayedCloseSocketFactory();
      gateway = HttpControlGateway(
        client: IOClient(),
        origin: Uri.parse('http://${server.address.address}:${server.port}'),
        token: const SessionToken('secret'),
        eventSocketFactory: sockets,
      );
      await gateway.subscribe();

      final closing = gateway.close();
      await sockets.socket.closeStarted.future.timeout(
        const Duration(seconds: 1),
      );
      expect(sockets.socket.closed, isFalse);
      sockets.socket.allowClose.complete();
      await closing.timeout(const Duration(seconds: 1));

      expect(sockets.socket.closed, isTrue);
    });

    test(
      'requires a live subscription and emits exact revision/idempotency JSON',
      () async {
        const intent = QueueMutationIntent.append(
          trackIds: [TrackId('track-1')],
        );
        await expectLater(
          gateway.mutateQueue(
            zoneId: const ZoneId('main'),
            expectedRevision: 4,
            idempotencyKey: const IdempotencyKey('append-1'),
            intent: intent,
          ),
          throwsA(isA<SubscriptionRequiredFailure>()),
        );

        final permit = await gateway.subscribe();
        final result = await gateway.mutateQueue(
          zoneId: const ZoneId('main'),
          expectedRevision: 4,
          idempotencyKey: const IdempotencyKey('append-1'),
          intent: intent,
          subscription: permit,
        );
        final mutation = requests.last;
        expect(mutation.headers.value('if-match'), '4');
        expect(mutation.headers.value('idempotency-key'), 'append-1');
        expect(jsonDecode(mutation.body), {
          'command': 'append',
          'track_ids': ['track-1'],
        });
        expect(result.revision, 5);
        expect(result.etag.revision, 5);
      },
    );

    test(
      'stale and offline failures preserve intent and are never retried',
      () async {
        final permit = await gateway.subscribe();
        const append = QueueMutationIntent.append(
          trackIds: [TrackId('track-1')],
        );
        await expectLater(
          gateway.mutateQueue(
            zoneId: const ZoneId('main'),
            expectedRevision: 2,
            idempotencyKey: const IdempotencyKey('stale-1'),
            intent: append,
            subscription: permit,
          ),
          throwsA(
            isA<StaleRevisionFailure>().having(
              (e) => e.intent,
              'intent',
              same(append),
            ),
          ),
        );
        expect(
          requests
              .where((r) => r.uri.path.endsWith('/queue') && r.method == 'POST')
              .length,
          1,
        );

        const transport = TransportMutationIntent.start();
        await expectLater(
          gateway.mutateTransport(
            zoneId: const ZoneId('main'),
            expectedRevision: 4,
            idempotencyKey: const IdempotencyKey('start-1'),
            intent: transport,
            subscription: permit,
          ),
          throwsA(
            isA<RendererOfflineFailure>().having(
              (e) => e.intent,
              'intent',
              same(transport),
            ),
          ),
        );
        expect(
          requests.where((r) => r.uri.path.endsWith('/transport')).length,
          1,
        );
      },
    );

    test('serializes every queue, transport, and assignment command', () async {
      final subscription = await gateway.subscribe();
      final queueIntents = <QueueMutationIntent>[
        const QueueMutationIntent.append(trackIds: [TrackId('a')]),
        const QueueMutationIntent.insert(
          trackIds: [TrackId('b')],
          beforeEntryId: QueueEntryId('before'),
        ),
        const QueueMutationIntent.remove(QueueEntryId('entry')),
        const QueueMutationIntent.move(
          QueueEntryId('entry'),
          beforeEntryId: QueueEntryId('before'),
        ),
        const QueueMutationIntent.clear(),
        const QueueMutationIntent.retryBlocked(QueueEntryId('entry')),
        const QueueMutationIntent.skipBlocked(QueueEntryId('entry')),
      ];
      for (var index = 0; index < queueIntents.length; index++) {
        await gateway.mutateQueue(
          zoneId: const ZoneId('main'),
          expectedRevision: queueRevision,
          idempotencyKey: IdempotencyKey('queue-$index'),
          intent: queueIntents[index],
          subscription: subscription,
        );
      }
      expect(
        requests
            .where((request) =>
                request.uri.path.endsWith('/queue') && request.method == 'POST')
            .map((request) =>
                (jsonDecode(request.body) as Map<String, Object?>)['command']),
        [
          'append',
          'insert',
          'remove',
          'move',
          'clear',
          'retry_blocked',
          'skip_blocked',
        ],
      );

      final transportIntents = <TransportMutationIntent>[
        const TransportMutationIntent.start(),
        const TransportMutationIntent.pause(),
        const TransportMutationIntent.resume(),
        const TransportMutationIntent.stop(),
        const TransportMutationIntent.skip(),
        const TransportMutationIntent.previous(),
        const TransportMutationIntent.seek(1234),
      ];
      for (var index = 0; index < transportIntents.length; index++) {
        await gateway.mutateTransport(
          zoneId: const ZoneId('main'),
          expectedRevision: queueRevision,
          idempotencyKey: IdempotencyKey('transport-$index'),
          intent: transportIntents[index],
          subscription: subscription,
        );
      }
      final transportBodies = requests
          .where((request) => request.uri.path.endsWith('/transport'))
          .map((request) => jsonDecode(request.body) as Map<String, Object?>)
          .toList(growable: false);
      expect(
        transportBodies.map((body) => body['command']),
        ['start', 'pause', 'resume', 'stop', 'skip', 'previous', 'seek'],
      );
      expect(transportBodies.last['position_ms'], 1234);

      final assignment = await gateway.assignRenderer(
        zoneId: const ZoneId('main'),
        expectedRevision: queueRevision,
        idempotencyKey: const IdempotencyKey('assign-1'),
        intent: const RendererAssignmentIntent(RendererId('renderer-1')),
        subscription: subscription,
      );
      expect(assignment.rendererId, const RendererId('renderer-1'));
      expect(assignment.etag.revision, queueRevision);
    });

    test(
      'logical observed and pending playback states remain separate',
      () async {
        final state = await gateway.playbackState(const ZoneId('main'));
        expect(state.logicalTransport.known, LogicalTransport.playing);
        expect(state.observedTransport.known, isNull);
        expect(state.observedTransport.wireValue, 'future-observed');
        expect(state.pendingCommandId, const CommandId('command-1'));
        expect(state.entries.single.state.known, isNull);
      },
    );

    test('offline Server is a typed recoverable state without retry', () async {
      await server.close(force: true);
      await expectLater(
        gateway.zones(),
        throwsA(
          isA<ServerOfflineFailure>().having(
            (failure) => failure.recoverable,
            'recoverable',
            isTrue,
          ),
        ),
      );
    });

    test('revoked token clears the credential exactly once', () async {
      await gateway.close();
      var credentialClears = 0;
      gateway = HttpControlGateway(
        client: IOClient(),
        origin: Uri.parse('http://${server.address.address}:${server.port}'),
        token: const SessionToken('secret'),
        eventSocketFactory: const _SnapshotSocketFactory(),
        onCredentialInvalidated: () => credentialClears++,
      );
      revoked = true;
      for (var attempt = 0; attempt < 2; attempt++) {
        await expectLater(
          gateway.zones(),
          throwsA(
            isA<TokenRevokedFailure>().having(
              (e) => e.recoverable,
              'recoverable',
              isTrue,
            ),
          ),
        );
      }
      expect(credentialClears, 1);
    });
  });

  test('duplicate and stale sequences are ignored without recovery', () async {
    var exact = 0;
    var full = 0;
    final coordinator = EventSyncCoordinator(
      refetchInvalidated: (_) async => exact++,
      fullResync: () async => full++,
    );
    await coordinator.accept(
      const ControlSnapshotEvent(
        serverEpoch: 'one',
        sequence: 4,
        resources: [],
      ),
    );
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 4,
        resource: WireResource.queue,
        revision: 1,
      ),
    );
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 3,
        resource: WireResource.queue,
        revision: 1,
      ),
    );
    expect(exact, 0);
    expect(full, 0);
    expect(coordinator.fullResyncCount, 0);
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 5,
        resource: WireResource.queue,
        revision: 2,
      ),
    );
    expect(exact, 1);
    expect(full, 0);
  });

  test('real gaps epoch changes and explicit resync each recover once',
      () async {
    var exact = 0;
    var full = 0;
    final coordinator = EventSyncCoordinator(
      refetchInvalidated: (_) async => exact++,
      fullResync: () async => full++,
      maxFullResyncs: 4,
    );
    await coordinator.accept(
      const ControlSnapshotEvent(
        serverEpoch: 'one',
        sequence: 4,
        resources: [],
      ),
    );
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 5,
        resource: WireResource.queue,
        revision: 1,
      ),
    );
    expect(exact, 1);
    expect(full, 0);
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 7,
        resource: WireResource.queue,
        revision: 2,
      ),
    );
    expect(full, 1);
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'two',
        sequence: 1,
        resource: WireResource.queue,
        revision: 3,
      ),
    );
    expect(full, 2);
    await coordinator.accept(
      const ControlResyncRequiredEvent(serverEpoch: 'two', sequence: 2),
    );
    expect(full, 3);
    expect(coordinator.fullResyncCount, 3);
  });

  test('initial non-snapshot event performs one bounded recovery', () async {
    var full = 0;
    final coordinator = EventSyncCoordinator(
      refetchInvalidated: (_) async {},
      fullResync: () async => full++,
    );
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 1,
        resource: WireResource.queue,
        revision: 1,
      ),
    );
    expect(full, 1);
    expect(coordinator.fullResyncCount, 1);
  });

  test('wire parser rejects integers outside JavaScript safe range', () {
    expect(
      () => parseControlEvent({
        'type': 'invalidation',
        'server_epoch': 'one',
        'sequence': 9007199254740992,
        'resource': 'queue',
        'revision': 1,
      }),
      throwsFormatException,
    );
    expect(
      () => parseControlEvent({
        'type': 'invalidation',
        'server_epoch': 'one',
        'sequence': 1,
        'resource': 'queue',
        'revision': 9007199254740992,
      }),
      throwsFormatException,
    );
  });

  test('wire parser preserves the Server zone_id invalidation scope', () {
    final event = parseControlEvent({
      'type': 'invalidation',
      'server_epoch': 'one',
      'sequence': 1,
      'resource': 'queue',
      'zone_id': 'living',
      'revision': 4,
    });

    expect(
      event,
      isA<ControlInvalidationEvent>().having(
        (value) => value.resourceId,
        'resourceId',
        'living',
      ),
    );
  });

  test('gap and epoch changes trigger bounded full resync', () async {
    var exact = 0;
    var full = 0;
    final coordinator = EventSyncCoordinator(
      refetchInvalidated: (_) async => exact++,
      fullResync: () async => full++,
      maxFullResyncs: 2,
    );
    await coordinator.accept(
      const ControlSnapshotEvent(
        serverEpoch: 'one',
        sequence: 4,
        resources: [],
      ),
    );
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 5,
        resource: WireResource.queue,
        revision: 2,
      ),
    );
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'one',
        sequence: 7,
        resource: WireResource.queue,
        revision: 3,
      ),
    );
    await coordinator.accept(
      const ControlInvalidationEvent(
        serverEpoch: 'two',
        sequence: 1,
        resource: WireResource.queue,
        revision: 4,
      ),
    );
    expect(exact, 1);
    expect(full, 2);
    expect(coordinator.fullResyncCount, 2);
    await expectLater(
      coordinator.accept(
        const ControlResyncRequiredEvent(serverEpoch: 'two', sequence: 2),
      ),
      throwsA(isA<ResyncLimitFailure>()),
    );
  });
}

final class _SnapshotSocketFactory implements EventSocketFactory {
  const _SnapshotSocketFactory();

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async =>
      _SnapshotSocket();
}

final class _SnapshotSocket implements EventSocket {
  _SnapshotSocket() {
    scheduleMicrotask(
      () => _controller.add(
        jsonEncode({
          'type': 'snapshot',
          'server_epoch': 1,
          'sequence': 0,
          'resources': <Object?>[],
        }),
      ),
    );
  }

  final _controller = StreamController<Object?>();

  @override
  Stream<Object?> get messages => _controller.stream;

  @override
  Future<void> close() => _controller.close();
}

final class _PreReadyEventSocketFactory implements EventSocketFactory {
  final socket = _ControlledSocket();

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async {
    scheduleMicrotask(() {
      socket.addSnapshot();
      socket.add(jsonEncode({
        'type': 'invalidation',
        'server_epoch': 1,
        'sequence': 1,
        'resource': 'future-resource',
        'revision': 1,
      }));
    });
    return socket;
  }
}

final class _MalformedSocketFactory implements EventSocketFactory {
  final socket = _ControlledSocket();

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async {
    scheduleMicrotask(() => socket.add('{malformed'));
    return socket;
  }
}

final class _ClosingSocketFactory implements EventSocketFactory {
  final socket = _ControlledSocket();

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async {
    scheduleMicrotask(socket.addSnapshot);
    return socket;
  }
}

final class _DelayedCloseSocketFactory implements EventSocketFactory {
  final socket = _DelayedCloseSocket();

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async {
    scheduleMicrotask(socket.addSnapshot);
    return socket;
  }
}

final class _DelayedCloseSocket extends _ControlledSocket {
  final closeStarted = Completer<void>();
  final allowClose = Completer<void>();

  @override
  Future<void> close() async {
    closeStarted.complete();
    await allowClose.future;
    await super.close();
  }
}

base class _ControlledSocket implements EventSocket {
  final _controller = StreamController<Object?>();
  bool closed = false;

  void add(Object? value) => _controller.add(value);
  void addSnapshot() => add(jsonEncode({
        'type': 'snapshot',
        'server_epoch': 1,
        'sequence': 0,
        'resources': <Object?>[],
      }));

  Future<void> closeFromServer() => _controller.close();

  @override
  Stream<Object?> get messages => _controller.stream;

  @override
  Future<void> close() async {
    closed = true;
    await _controller.close();
  }
}

final class _RecordedRequest {
  const _RecordedRequest(this.method, this.uri, this.headers, this.body);
  final String method;
  final Uri uri;
  final HttpHeaders headers;
  final String body;
}
