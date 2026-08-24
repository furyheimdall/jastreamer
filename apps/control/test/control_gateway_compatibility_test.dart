import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/protocol_compatibility.dart';

void main() {
  test(
    'Given a v1 Server When discovered Then Control retries with major 1',
    () async {
      final requestedMajors = <String>[];
      final gateway = HttpControlGateway(
        client: MockClient((request) async {
          final requestedMajor = request.headers['x-jake-protocol-major']!;
          requestedMajors.add(requestedMajor);
          expect(request.headers['x-jake-supported-protocol-majors'], '2,1');
          if (requestedMajor == '2') {
            return http.Response(
              jsonEncode({'code': 'UNSUPPORTED_PROTOCOL_MAJOR'}),
              426,
            );
          }
          if (request.url.path == '/api/v1/zones/main/queue') {
            return http.Response(jsonEncode({'revision': 1, 'queue': []}), 200);
          }
          return http.Response(
            jsonEncode({
              'protocol_major': 1,
              'supported_protocol_majors': [1],
              'capabilities': ['queue', 'future-capability'],
              'pairing_url': '/pair/',
              'certificate_sha256': 'AA:BB',
              'contract_revision': 'http-api-v1',
              'catalog_revision': 42,
            }),
            200,
          );
        }),
        origin: Uri.parse('https://living.local:8443'),
        token: const SessionToken('session-secret'),
      );

      final discovery = await gateway.discovery();
      final queue = await gateway.queue();

      expect(requestedMajors, ['2', '1', '1']);
      expect(discovery.protocolMajor, 1);
      expect(discovery.catalogRevision, 42);
      expect(discovery.capabilities, contains('future-capability'));
      expect(queue.entries, isEmpty);
    },
  );

  test(
    'Given inconsistent selected major When discovery succeeds Then reject response',
    () async {
      final gateway = HttpControlGateway(
        client: MockClient(
          (_) async => http.Response(
            jsonEncode({
              'protocol_major': 99,
              'supported_protocol_majors': [2, 1],
              'capabilities': ['queue'],
              'pairing_url': '/pair/',
              'certificate_sha256': 'AABB',
              'contract_revision': 'http-api-v1',
              'catalog_revision': 1,
            }),
            200,
          ),
        ),
        origin: Uri.parse('https://living.local:8443'),
        token: const SessionToken('session-secret'),
      );

      expect(gateway.discovery(), throwsA(isA<FormatException>()));
    },
  );

  test(
    'Given no common major When discovered Then failure is typed',
    () async {
      final gateway = HttpControlGateway(
        client: MockClient((_) async => http.Response(
              jsonEncode({
                'supported_protocol_majors': [99],
                'capabilities': ['future-capability'],
                'pairing_url': '/pair/',
                'certificate_sha256': 'AA:BB',
                'contract_revision': 'future',
                'catalog_revision': 42,
                'optional_metadata': 'additive',
              }),
              200,
            )),
        origin: Uri.parse('https://living.local:8443'),
        token: const SessionToken('session-secret'),
      );

      await expectLater(
        gateway.discovery(),
        throwsA(
          isA<UnsupportedProtocolMajor>()
              .having(
            (failure) => failure.code,
            'code',
            'UNSUPPORTED_PROTOCOL_MAJOR',
          )
              .having((failure) => failure.serverMajors, 'server majors', [99]),
        ),
      );
    },
  );

  test(
    'Given unknown policy and queue state When read Then values stay explicit',
    () async {
      final gateway = HttpControlGateway(
        client: MockClient((request) async => switch (request.url.path) {
              '/api/v1/zones/main/continuation-policy' => http.Response(
                  jsonEncode({
                    'mode': 'future-mode',
                    'artist_gap': 4,
                    'album_gap': 10,
                    'session_override': 'future-override',
                    'revision': 8,
                    'additive': true,
                  }),
                  200,
                ),
              '/api/v1/zones/main/queue' => http.Response(
                  jsonEncode({
                    'revision': 9,
                    'queue': [
                      {'track_id': 'next', 'state': 'future-state'}
                    ],
                  }),
                  200,
                ),
              _ => throw StateError('unexpected request'),
            }),
        origin: Uri.parse('https://living.local:8443'),
        token: const SessionToken('session-secret'),
      );

      final policy = await gateway.policy();
      final queue = await gateway.queue();

      expect(policy.mode, isA<UnknownWirePolicy>());
      expect(policy.mode.wireValue, 'future-mode');
      expect(policy.sessionOverride, isA<UnknownWirePolicy>());
      expect(queue.entries.single.state, 'future-state');
      expect(queue.entries.single.isUnavailableHead, isFalse);
    },
  );

  test(
    'Given unknown decision reason When read Then it is never a known mutation',
    () async {
      final gateway = HttpControlGateway(
        client: MockClient((_) async => http.Response(
              jsonEncode({
                'reason': 'FUTURE_MUTATION',
                'source': 'future',
                'track_id': 'track',
                'signal_coverage': 90,
                'catalog_revision': 4,
                'policy_revision': 8,
                'optional_metadata': 'additive',
              }),
              200,
            )),
        origin: Uri.parse('https://living.local:8443'),
        token: const SessionToken('session-secret'),
      );

      final decision = await gateway.explanation();

      expect(decision.reason, isA<UnknownDecisionReason>());
      expect(decision.reason.code, 'FUTURE_MUTATION');
      expect(decision.reason.isKnown, isFalse);
      expect(decision.reason == DecisionReason.blockExplicit, isFalse);
    },
  );
}
