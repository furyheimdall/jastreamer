import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:jastreamer_control/control_application.dart';
import 'package:jastreamer_control/control_events.dart';
import 'package:jastreamer_control/control_platform.dart';
import 'package:jastreamer_control/control_workflow.dart';
import 'package:jastreamer_control/credential_vault.dart';

void main() {
  testWidgets('pairing protocol v3 opens an event session before success',
      (tester) async {
    tester.view.physicalSize = const Size(1280, 3000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final requests = <http.Request>[];
    var failNextCatalogReload = false;
    final sockets = _RecordingSocketFactory();
    final platform = ControlPlatform(
      clientFactory: (_) => MockClient((request) async {
        requests.add(request);
        if (failNextCatalogReload &&
            request.url.path == '/api/v1/catalog/status') {
          failNextCatalogReload = false;
          throw http.ClientException('replacement reload failed');
        }
        return _response(request.url.path);
      }),
      eventSocketFactory: sockets,
      vault: SerializedCredentialVault(MemoryCredentialVaultStorage()),
      launcher: const _FixtureLauncher(),
    );
    await tester.pumpWidget(ControlApp(
      platform: platform,
      initialServer: Uri.parse('https://living.local:8443'),
      initialFingerprint: 'AA:BB',
    ));

    await tester.tap(find.text('Discover Server'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Open pairing page'));
    await tester.pump();
    await tester.tap(find.text('Open pairing page'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.bySemanticsLabel('Controller token'),
      'session-secret',
    );
    await tester.tap(find.text('Complete pairing'));
    await tester.pumpAndSettle();

    expect(find.text('Paired device'), findsOneWidget);
    expect(
      requests.where((request) => request.url.path == '/api/v1/event-tickets'),
      hasLength(1),
    );
    expect(sockets.connections, [
      Uri.parse(
        'wss://living.local:8443/api/v1/events'
        '?ticket=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ',
      ),
    ]);
    expect(
      requests
          .indexWhere((request) => request.url.path == '/api/v1/event-tickets'),
      lessThan(requests
          .indexWhere((request) => request.url.path == '/api/v1/zones')),
    );

    final gapReloadStart = requests.length;
    sockets.sockets.single.add(jsonEncode({
      'type': 'invalidation',
      'server_epoch': '1',
      'sequence': 2,
      'resource': 'queue',
      'zone_id': 'main',
      'revision': 8,
    }));
    await tester.pumpAndSettle();
    expect(sockets.connections, hasLength(1));
    expect(
      requests
          .skip(gapReloadStart)
          .map((request) => request.url.path)
          .where((path) => {
                '/api/v1/catalog/status',
                '/api/v1/zones',
                '/api/v1/zones/main/playback-state',
                '/api/v1/zones/main/continuation-policy',
              }.contains(path))
          .toList(),
      [
        '/api/v1/catalog/status',
        '/api/v1/zones',
        '/api/v1/zones/main/playback-state',
        '/api/v1/zones/main/continuation-policy',
      ],
    );

    final replacementSignal = sockets.nextConnection();
    sockets.sockets.single.fail(StateError('transport closed'));
    final replacement = await replacementSignal;
    sockets.sockets.first.fail(StateError('duplicate transport error'));
    await tester.pump();
    expect(sockets.connections, hasLength(2));
    final replacementReloadStart = requests.length;
    replacement.addSnapshot(serverEpoch: '2');
    await tester.pumpAndSettle();

    expect(
      requests
          .skip(replacementReloadStart)
          .map((request) => request.url.path)
          .toList(),
      [
        '/api/v1/catalog/status',
        '/api/v1/zones',
        '/api/v1/zones/main/playback-state',
        '/api/v1/zones/main/continuation-policy',
      ],
    );
    expect(
      requests.where((request) => request.url.path == '/api/v1/event-tickets'),
      hasLength(2),
    );
    expect(sockets.connections, hasLength(2));
    final failedReplacementSignal = sockets.nextConnection();
    replacement.fail(StateError('second transport error'));
    final failedReplacement = await failedReplacementSignal;
    failNextCatalogReload = true;
    failedReplacement.addSnapshot(serverEpoch: '3');
    await tester.pumpAndSettle();
    final playbackRequestsBefore = requests
        .where((request) =>
            request.url.path == '/api/v1/zones/main/playback-state')
        .length;
    failedReplacement.add(jsonEncode({
      'type': 'invalidation',
      'server_epoch': '3',
      'sequence': 1,
      'resource': 'queue',
      'zone_id': 'main',
      'revision': 10,
    }));
    await tester.pumpAndSettle();
    expect(
      requests
          .where((request) =>
              request.url.path == '/api/v1/zones/main/playback-state')
          .length,
      playbackRequestsBefore + 1,
    );
    final unwatchedZoneStart = requests.length;
    failedReplacement.add(jsonEncode({
      'type': 'invalidation',
      'server_epoch': '3',
      'sequence': 2,
      'resource': 'queue',
      'zone_id': 'secondary',
      'revision': 1,
    }));
    await tester.pumpAndSettle();
    expect(requests.skip(unwatchedZoneStart), isEmpty);
    expect(sockets.connections, hasLength(3));
    await tester.drag(
      find.byKey(const Key('control-scroll-body')),
      const Offset(0, -2500),
    );
    await tester.pumpAndSettle();
    expect(find.byType(ControlWorkflow), findsOneWidget);
    final workflow =
        tester.widget<ControlWorkflow>(find.byType(ControlWorkflow));
    expect(workflow.failure, isNotNull);
    expect(workflow.syncNotice, isNotNull);
  });
}

http.Response _response(String path) {
  final Object body = switch (path) {
    '/api/v1/identity' => {
        'common_name': 'jastreamer Server',
        'sha256_fingerprint': 'AABB',
        'pairing_url': '/pair/',
      },
    '/api/v1/discovery' => {
        'protocol_major': 3,
        'supported_protocol_majors': [3],
        'capabilities': ['catalog-status', 'queue', 'continuation-policy'],
        'pairing_url': '/pair/',
        'certificate_sha256': 'AABB',
        'contract_revision': 'http-api-v1',
        'catalog_revision': 42,
      },
    '/api/v1/zones/main/continuation-policy' => {
        'mode': 'stop',
        'artist_gap': 4,
        'album_gap': 10,
        'session_override': '',
        'revision': 7,
      },
    '/api/v1/catalog/status' => {
        'scan_status': 'ready',
        'catalog_revision': 42,
        'track_count': 1,
        'analysis_complete': 1,
        'analysis_queued': 0,
        'analysis_failed': 0,
        'analysis_coverage': 100,
        'analysis_revision': 1,
      },
    '/api/v1/zones/main/queue' => {
        'zone_id': 'main',
        'revision': 7,
        'transport': 'idle',
        'queue': <Object>[],
      },
    '/api/v1/zones/main/automatic-preview' => {
        'active': false,
        'replaceable': true,
        'committed': false,
        'decision': _decision(),
      },
    '/api/v1/zones/main/decision-explanation' => _decision(),
    '/api/v1/zones' => {
        'zones': [
          {
            'zone_id': 'main',
            'name': 'Main',
            'revision': 7,
            'renderer_id': null,
            'transport': 'idle',
          },
        ],
        'renderers': <Object>[],
      },
    '/api/v1/zones/main/playback-state' => {
        'zone_id': 'main',
        'revision': 7,
        'transport': 'idle',
        'observed_transport': 'idle',
        'pending_command_id': null,
        'queue': <Object>[],
      },
    '/api/v1/catalog/tracks' => {
        'catalog_revision': 42,
        'next_cursor': null,
        'tracks': <Object>[],
      },
    '/api/v1/event-tickets' => {
        'ticket': 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ',
        'expires_at': '2026-08-30T00:01:00Z',
      },
    _ => throw StateError('Unexpected route: $path'),
  };
  return http.Response(
    jsonEncode(body),
    path == '/api/v1/event-tickets' ? 201 : 200,
    headers: {'content-type': 'application/json'},
  );
}

Map<String, Object?> _decision() => {
      'decision_id': 'd1',
      'kind': 'block',
      'reason': 'QUEUE_EMPTY',
      'source': 'automatic',
      'track_id': null,
      'algorithm_revision': 'policy-v1',
      'catalog_revision': 42,
      'policy_revision': 7,
      'contract_revision': 'http-api-v1',
      'signal_coverage': 100,
    };

final class _RecordingSocketFactory implements EventSocketFactory {
  final connections = <Uri>[];
  final sockets = <_SnapshotSocket>[];
  Completer<_SnapshotSocket>? _nextConnection;

  Future<_SnapshotSocket> nextConnection() {
    final next = Completer<_SnapshotSocket>();
    _nextConnection = next;
    return next.future;
  }

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async {
    connections.add(uri);
    final socket = _SnapshotSocket(emitInitialSnapshot: sockets.isEmpty);
    sockets.add(socket);
    _nextConnection?.complete(socket);
    _nextConnection = null;
    return socket;
  }
}

final class _SnapshotSocket implements EventSocket {
  _SnapshotSocket({required bool emitInitialSnapshot}) {
    if (emitInitialSnapshot) scheduleMicrotask(addSnapshot);
  }

  final _messages = StreamController<Object?>();

  void add(Object? event) => _messages.add(event);
  void fail(Object error) => _messages.addError(error);
  void addSnapshot({String serverEpoch = '1'}) => add(jsonEncode({
        'type': 'snapshot',
        'server_epoch': serverEpoch,
        'sequence': 0,
        'resources': <Object>[],
      }));

  @override
  Stream<Object?> get messages => _messages.stream;

  @override
  Future<void> close() => _messages.close();
}

final class _FixtureLauncher implements ExternalLauncher {
  const _FixtureLauncher();

  @override
  Future<bool> open(Uri uri) async => true;
}
