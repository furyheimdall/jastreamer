import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:jstreamer_control/control_gateway.dart';
import 'package:jstreamer_control/control_models.dart';
import 'package:jstreamer_control/control_platform.dart';

void main() {
  test(
    'Given Todo13 discovery When parsed Then advertised HTTPS details survive',
    () async {
      final client = MockClient((request) async {
        expect(request.headers['authorization'], 'Bearer session-secret');
        expect(request.headers['x-jake-protocol-major'], '2');
        return http.Response(
          jsonEncode({
            'protocol_major': 2,
            'supported_protocol_majors': [1, 2],
            'capabilities': ['catalog-status', 'queue', 'continuation-policy'],
            'pairing_url': '/pair/',
            'certificate_sha256': 'AA:BB',
            'contract_revision': 'http-api-v1',
            'algorithm_revision': 'policy-v1',
            'analysis_revision': 1,
            'catalog_revision': 42,
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      });
      final gateway = HttpControlGateway(
        client: client,
        origin: Uri.parse('https://living.local:8443'),
        token: const SessionToken('session-secret'),
      );

      final discovery = await gateway.discovery();

      expect(
        discovery.pairingUrl.toString(),
        'https://living.local:8443/pair/',
      );
      expect(discovery.certificateSha256, 'AA:BB');
      expect(discovery.catalogRevision, 42);
    },
  );

  test('Given public identity Then pairing URL is Server-advertised', () async {
    final endpoint = ControlEndpoint(
      client: MockClient((_) async => http.Response(
          jsonEncode({
            'common_name': 'Jake Streamer Server',
            'sha256_fingerprint': 'AABB',
            'pairing_url': '/pair/',
          }),
          200)),
      origin: Uri.parse('https://living.local:8443'),
    );

    final identity = await endpoint.identity();

    expect(identity.pairingUrl.toString(), 'https://living.local:8443/pair/');
  });

  test(
    'Given a token vault When stored Then it never creates a callback URI',
    () async {
      final vault = MemoryTokenVault();
      const token = SessionToken('one-time-secret');

      await vault.store(token);

      expect((await vault.read())?.value, token.value);
      expect(vault.callbackUri, isNull);
    },
  );

  test('Given omitted empty session override Then policy parses no override',
      () async {
    final gateway = HttpControlGateway(
      client: MockClient((_) async => http.Response(
          jsonEncode({
            'mode': 'stop',
            'artist_gap': 4,
            'album_gap': 10,
            'revision': 0,
          }),
          200)),
      origin: Uri.parse('https://living.local:8443'),
      token: const SessionToken('secret'),
    );

    final policy = await gateway.policy();

    expect(policy.sessionOverride, isNull);
  });

  test(
    'Given stale policy response Then typed failure preserves Server revision',
    () async {
      final client = MockClient(
        (request) async => http.Response(
          jsonEncode({'code': 'STALE_POLICY_REVISION', 'message': 'stale'}),
          412,
          headers: {'etag': '"8"'},
        ),
      );
      final gateway = HttpControlGateway(
        client: client,
        origin: Uri.parse('https://living.local:8443'),
        token: const SessionToken('secret'),
      );

      expect(
        () => gateway.updatePolicy(
          const PolicyWrite(
            mode: 'similar',
            artistGap: 4,
            albumGap: 10,
            sessionOverride: '',
          ),
          7,
        ),
        throwsA(isA<StalePolicyFailure>()),
      );
    },
  );
}
