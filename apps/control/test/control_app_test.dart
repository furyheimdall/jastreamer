import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:jastreamer_control/control_application.dart';
import 'package:jastreamer_control/control_platform.dart';
import 'package:jastreamer_control/control_theme.dart';

void main() {
  testWidgets('Given startup When rendered Then discovery is the first task', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(const ControlApp());

    expect(find.text('Find a jastreamer server'), findsOneWidget);
    final fingerprint = tester.widget<TextField>(find.byType(TextField).at(1));
    expect(fingerprint.decoration?.helperMaxLines, 2);
    expect(tester.takeException(), isNull);
    final discoverButton = tester.widget<FilledButton>(
      find.byKey(const Key('discover-server-button')),
    );
    final focusedSide = discoverButton.style?.side?.resolve({
      WidgetState.focused,
    });
    final defaultSide = discoverButton.style?.side?.resolve({});
    expect(focusedSide?.color, ControlColors.textPrimary);
    expect(focusedSide?.width, 2);
    expect(defaultSide?.color, Colors.transparent);
    expect(defaultSide?.width, 2);
    for (final heading in ['Control room', 'Find a jastreamer server']) {
      expect(
        tester
            .getSemantics(find.text(heading))
            .getSemanticsData()
            .flagsCollection
            .isHeader,
        isTrue,
      );
    }
    await tester.ensureVisible(find.text('Discover Server'));
    await tester.pump();
    expect(find.text('Discover Server'), findsOneWidget);
    semantics.dispose();
  });

  testWidgets(
    'Given rejected token When pairing load fails Then vault is empty',
    (tester) async {
      final vault = MemoryTokenVault();
      final client = MockClient((request) async {
        if (request.url.path == '/api/v1/identity') {
          return http.Response(
            jsonEncode({
              'common_name': 'jastreamer Server',
              'sha256_fingerprint': 'AABB',
              'pairing_url': '/pair/',
            }),
            200,
          );
        }
        return http.Response(jsonEncode({'code': 'UNAUTHORIZED'}), 401);
      });
      final platform = ControlPlatform(
        clientFactory: (_) => client,
        vault: vault,
        launcher: const _FixtureLauncher(),
      );
      await tester.pumpWidget(
        ControlApp(
          platform: platform,
          initialServer: Uri.parse('https://living.local:8443'),
        ),
      );
      await tester.tap(find.text('Discover Server'));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('Open pairing page'));
      await tester.pump();
      await tester.tap(find.text('Open pairing page'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.bySemanticsLabel('Controller token'),
        'rejected-secret',
      );
      await tester.tap(find.text('Complete pairing'));
      await tester.pumpAndSettle();

      expect(await vault.read(), isNull);
      expect(find.textContaining('UNAUTHORIZED'), findsOneWidget);
    },
  );

  testWidgets(
    'Given Todo13 Server When pairing returns Then shared policy truth renders',
    (tester) async {
      final semantics = tester.ensureSemantics();
      final client = _todo13Client();
      final platform = ControlPlatform(
        clientFactory: (_) => client,
        launcher: const _FixtureLauncher(),
      );
      await tester.pumpWidget(
        ControlApp(
          platform: platform,
          initialServer: Uri.parse('https://living.local:8443'),
        ),
      );

      await tester.tap(find.text('Discover Server'));
      await tester.pumpAndSettle();
      expect(find.text('jastreamer Server'), findsOneWidget);
      expect(find.text('Open pairing page'), findsOneWidget);
      expect(find.textContaining('SHA-256'), findsWidgets);

      await tester.ensureVisible(find.text('Open pairing page'));
      await tester.pump();
      await tester.tap(find.text('Open pairing page'));
      await tester.pumpAndSettle();
      expect(find.text('Complete pairing return'), findsOneWidget);
      await tester.enterText(
        find.bySemanticsLabel('Controller token'),
        'session-secret',
      );
      await tester.tap(find.text('Complete pairing'));
      await tester.pumpAndSettle();

      expect(find.text('Paired device'), findsOneWidget);
      final pairedStatus = find.textContaining(
        'Paired device · authenticated discovery verified',
      );
      expect(
        tester
            .getSemantics(pairedStatus)
            .getSemanticsData()
            .flagsCollection
            .isLiveRegion,
        isTrue,
      );
      for (final label in ['재생 종료', '앨범 이어듣기', '비슷한 음악']) {
        expect(find.text(label), findsOneWidget);
      }
      expect(find.text('Per-session override'), findsOneWidget);
      expect(find.text('Artist cooldown'), findsOneWidget);
      expect(find.text('Album cooldown'), findsOneWidget);
      expect(find.text('Indexing & analysis'), findsOneWidget);
      expect(find.text('Persisted decision reason'), findsOneWidget);
      expect(find.text('Explicit queue'), findsOneWidget);
      expect(find.text('Automatic next preview'), findsOneWidget);
      expect(find.text('Unavailable explicit head'), findsOneWidget);
      expect(find.text('Revocable preview'), findsOneWidget);
      expect(find.textContaining('Analysis incomplete'), findsWidgets);
      semantics.dispose();
    },
  );
}

MockClient _todo13Client() => MockClient((request) async {
      final path = request.url.path;
      final Object body = switch (path) {
        '/api/v1/identity' => {
            'common_name': 'jastreamer Server',
            'sha256_fingerprint': 'AABB',
            'pairing_url': '/pair/',
          },
        '/api/v1/discovery' => {
            'protocol_major': 2,
            'supported_protocol_majors': [1, 2],
            'capabilities': ['catalog-status', 'queue', 'continuation-policy'],
            'pairing_url': '/pair/',
            'certificate_sha256': 'AABB',
            'contract_revision': 'http-api-v1',
            'algorithm_revision': 'policy-v1',
            'analysis_revision': 1,
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
            'track_count': 100,
            'analysis_complete': 61,
            'analysis_queued': 32,
            'analysis_failed': 7,
            'analysis_coverage': 61,
            'analysis_revision': 1,
          },
        '/api/v1/zones/main/queue' => {
            'zone_id': 'main',
            'revision': 7,
            'transport': 'idle',
            'queue': [
              {
                'entry_id': 'e1',
                'track_id': 'missing-track',
                'state': 'blocked',
                'position': 1,
              },
            ],
          },
        '/api/v1/zones/main/automatic-preview' => {
            'active': false,
            'replaceable': true,
            'committed': false,
            'decision': _decision('BLOCK_EXPLICIT'),
          },
        '/api/v1/zones/main/decision-explanation' =>
          _decision('BLOCK_EXPLICIT'),
        _ => throw StateError('Unexpected Todo13 route: $path'),
      };
      return http.Response(
        jsonEncode(body),
        200,
        headers: {'content-type': 'application/json'},
      );
    });

Map<String, Object> _decision(String reason) => {
      'decision_id': 'd1',
      'kind': 'block',
      'reason': reason,
      'source': 'explicit',
      'track_id': 'missing-track',
      'algorithm_revision': 'policy-v1',
      'catalog_revision': 42,
      'policy_revision': 7,
      'contract_revision': 'http-api-v1',
      'signal_coverage': 61,
    };

final class _FixtureLauncher implements ExternalLauncher {
  const _FixtureLauncher();
  @override
  Future<bool> open(Uri uri) async =>
      uri == Uri.parse('https://living.local:8443/pair/');
}
