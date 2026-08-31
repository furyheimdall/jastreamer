import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/control_events.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/control_models.dart';

const _fingerprint =
    '6230b1244e54c200096c1274c989b1f2e12aec8bab7fa3bf03a53a495e15178c';

void main() {
  late _TlsControlServer driver;

  setUp(() async {
    driver = await _TlsControlServer.start();
  });

  tearDown(() => driver.close());

  test('native pin, real WSS subscription, mutation, and exact refetch',
      () async {
    final endpoint = ControlEndpoint.certificateBound(
      origin: driver.origin,
      certificateSha256: _fingerprint,
    );
    final gateway = endpoint.authenticated(
      const SessionToken('controller-token'),
    );
    addTearDown(gateway.close);

    final session = await gateway.subscribe();
    addTearDown(session.close);
    final update = session.updates.first;
    final result = await gateway.mutateQueue(
      zoneId: const ZoneId('main'),
      expectedRevision: 0,
      idempotencyKey: const IdempotencyKey('tls-append'),
      intent: const QueueMutationIntent.append(trackIds: [TrackId('track-1')]),
      subscription: session,
    );

    expect(result.revision, 1);
    final refreshed = await update;
    expect(refreshed.value, isA<PlaybackState>());
    expect((refreshed.value as PlaybackState).revision, 1);
    expect(driver.queueReads, 1);
    expect(driver.mutations, 1);
  });

  test('real WSS gap performs bounded exact full resync', () async {
    await driver.close();
    driver = await _TlsControlServer.start();
    final gateway = ControlEndpoint.certificateBound(
      origin: driver.origin,
      certificateSha256: _fingerprint,
    ).authenticated(const SessionToken('controller-token'));
    addTearDown(gateway.close);
    final session = await gateway.subscribe(maxFullResyncs: 1);
    addTearDown(session.close);

    final updates = session.updates.take(4).toList().timeout(
          const Duration(seconds: 5),
        );
    driver.sendInvalidation(sequence: 2, resource: 'queue', revision: 1);
    final refreshed = await updates;
    expect(refreshed.map((update) => update.resource), [
      ResourceKind.catalog,
      ResourceKind.zones,
      ResourceKind.queue,
      ResourceKind.continuationPolicy,
    ]);
    expect(driver.catalogReads, 1);
    expect(driver.zoneReads, 1);
    expect(driver.queueReads, 1);
    expect(driver.policyReads, 1);

    final errorSignal = Completer<Object>();
    final eventSubscription = session.events.listen(
      (_) {},
      onError: errorSignal.complete,
    );
    addTearDown(eventSubscription.cancel);
    driver.sendInvalidation(sequence: 4, resource: 'transport', revision: 2);
    expect(
      await errorSignal.future.timeout(const Duration(seconds: 5)),
      isA<ResyncLimitFailure>(),
    );
  });

  test('incompatible zone values fail closed at the real TLS boundary',
      () async {
    await driver.close();
    driver = await _TlsControlServer.start(unknownValues: true);
    final gateway = ControlEndpoint.certificateBound(
      origin: driver.origin,
      certificateSha256: _fingerprint,
    ).authenticated(const SessionToken('controller-token'));
    addTearDown(gateway.close);

    await expectLater(gateway.zones(), throwsFormatException);
    final state = await gateway.playbackState(const ZoneId('main'));
    expect(state.logicalTransport.known, isNull);
    expect(state.observedTransport.known, isNull);
    expect(state.entries.single.state.known, isNull);
  });

  test('native TLS rejects certificate identity change as typed recovery',
      () async {
    final endpoint = ControlEndpoint.certificateBound(
      origin: driver.origin,
      certificateSha256:
          '0000000000000000000000000000000000000000000000000000000000000000',
    );
    addTearDown(endpoint.close);

    await expectLater(
      endpoint.identity(),
      throwsA(
        isA<CertificateIdentityChangedFailure>()
            .having((failure) => failure.recoverable, 'recoverable', isTrue),
      ),
    );
  });
}

final class _TlsControlServer {
  _TlsControlServer._(this._server, this.origin);

  static Future<_TlsControlServer> start({bool unknownValues = false}) async {
    final context = SecurityContext()
      ..useCertificateChain('test/fixtures/control_gateway_tls_cert.pem')
      ..usePrivateKey('test/fixtures/control_gateway_tls_key.pem');
    final server = await HttpServer.bindSecure(
      InternetAddress.loopbackIPv4,
      0,
      context,
    );
    final driver = _TlsControlServer._(
      server,
      Uri.parse('https://127.0.0.1:${server.port}'),
    )..unknownValues = unknownValues;
    driver._serve = server.listen(driver._handle);
    return driver;
  }

  final HttpServer _server;
  final Uri origin;
  late final StreamSubscription<HttpRequest> _serve;
  WebSocket? _events;
  int revision = 0;
  int queueReads = 0;
  int catalogReads = 0;
  int zoneReads = 0;
  int policyReads = 0;
  int mutations = 0;
  bool unknownValues = false;

  void sendInvalidation({
    required int sequence,
    required String resource,
    required int revision,
  }) {
    _events?.add(jsonEncode({
      'type': 'invalidation',
      'server_epoch': 1,
      'sequence': sequence,
      'resource': resource,
      'revision': revision,
    }));
  }

  Future<void> _handle(HttpRequest request) async {
    try {
      if (request.uri.path == '/api/v1/identity') {
        return _json(request, HttpStatus.ok, {
          'common_name': 'TLS fixture',
          'sha256_fingerprint': _fingerprint,
          'pairing_url': '/pair/',
        });
      }
      if (request.uri.path == '/api/v1/event-tickets') {
        _requireBearer(request);
        return _json(request, HttpStatus.created, {
          'ticket': 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ',
          'expires_at': '2026-08-26T00:01:00Z',
        });
      }
      if (request.uri.path == '/api/v1/events') {
        if (request.uri.queryParameters['ticket'] == null) {
          return _json(request, HttpStatus.unauthorized, {
            'code': 'EVENT_TICKET_INVALID',
            'message': 'ticket required',
          });
        }
        final socket = await WebSocketTransformer.upgrade(request);
        _events = socket;
        socket.add(jsonEncode({
          'type': 'snapshot',
          'server_epoch': 1,
          'sequence': 0,
          'resources': [
            {'resource': 'queue', 'revision': revision}
          ],
        }));
        return;
      }
      if (request.uri.path == '/api/v1/zones/main/queue' &&
          request.method == 'POST') {
        _requireBearer(request);
        expect(request.headers.value('if-match'), '$revision');
        expect(request.headers.value('idempotency-key'), 'tls-append');
        expect(jsonDecode(await utf8.decoder.bind(request).join()), {
          'command': 'append',
          'track_ids': ['track-1'],
        });
        mutations++;
        revision++;
        request.response.headers.set('etag', '"$revision"');
        await _json(request, HttpStatus.created, {
          'revision': revision,
          'entry_ids': ['entry-1'],
        });
        _events?.add(jsonEncode({
          'type': 'invalidation',
          'server_epoch': 1,
          'sequence': 1,
          'resource': 'queue',
          'revision': revision,
        }));
        return;
      }
      if (request.uri.path == '/api/v1/catalog/status') {
        _requireBearer(request);
        catalogReads++;
        return _json(request, HttpStatus.ok, {
          'catalog_revision': 1,
          'track_count': 1,
          'analysis_complete': 1,
          'analysis_queued': 0,
          'analysis_failed': 0,
          'analysis_coverage': 100,
        });
      }
      if (request.uri.path == '/api/v1/zones') {
        _requireBearer(request);
        zoneReads++;
        return _json(request, HttpStatus.ok, {
          'zones': [
            {
              'zone_id': 'main',
              'name': 'Main',
              'revision': revision,
              'renderer_id': 'renderer-1',
              'transport': unknownValues ? 'future-logical' : 'idle',
            }
          ],
          'renderers': [
            {
              'renderer_id': 'renderer-1',
              'name': 'Renderer',
              'kind': unknownValues ? 'future-kind' : 'custom',
              'status': unknownValues ? 'future-status' : 'connected',
              'capabilities': ['seek'],
              'last_seen_at': '2026-08-26T00:00:00Z',
            }
          ],
        });
      }
      if (request.uri.path == '/api/v1/zones/main/continuation-policy') {
        _requireBearer(request);
        policyReads++;
        return _json(request, HttpStatus.ok, {
          'mode': 'stop',
          'artist_gap': 4,
          'album_gap': 10,
          'revision': 1,
        });
      }
      if (request.uri.path == '/api/v1/zones/main/playback-state') {
        _requireBearer(request);
        queueReads++;
        return _json(request, HttpStatus.ok, {
          'zone_id': 'main',
          'revision': revision,
          'transport': unknownValues ? 'future-logical' : 'idle',
          'observed_transport': unknownValues ? 'future-observed' : 'idle',
          'pending_command_id': null,
          'queue': unknownValues
              ? [
                  {
                    'entry_id': 'entry-1',
                    'track_id': 'track-1',
                    'state': 'future-state',
                    'position': 0,
                  }
                ]
              : revision == 0
                  ? <Object?>[]
                  : [
                      {
                        'entry_id': 'entry-1',
                        'track_id': 'track-1',
                        'state': 'pending',
                        'position': 0,
                      }
                    ],
        });
      }
      return _json(request, HttpStatus.notFound, {
        'code': 'NOT_FOUND',
        'message': 'missing fixture route',
      });
    } catch (error, stack) {
      request.response.statusCode = HttpStatus.internalServerError;
      request.response
          .write(jsonEncode({'code': 'INTERNAL', 'message': '$error\n$stack'}));
      await request.response.close();
    }
  }

  void _requireBearer(HttpRequest request) {
    expect(request.headers.value('authorization'), 'Bearer controller-token');
  }

  Future<void> _json(HttpRequest request, int status, Object body) async {
    request.response.statusCode = status;
    request.response.headers.contentType = ContentType.json;
    request.response.write(jsonEncode(body));
    await request.response.close();
  }

  Future<void> close() async {
    await _events?.close();
    await _serve.cancel();
    await _server.close(force: true);
  }
}
