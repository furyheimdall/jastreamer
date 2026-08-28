import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/credential_vault.dart';

void main() {
  test(
    'Given Todo13 discovery When parsed Then advertised HTTPS details survive',
    () async {
      final client = MockClient((request) async {
        expect(request.headers['authorization'], 'Bearer session-secret');
        final requested = request.headers['x-jake-protocol-major'];
        if (requested == '3') {
          return http.Response(
            jsonEncode({'code': 'UNSUPPORTED_PROTOCOL_MAJOR'}),
            426,
          );
        }
        expect(requested, '2');
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
            'common_name': 'jastreamer Server',
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
      final vault = SerializedCredentialVault(
        MemoryCredentialVaultStorage(),
      );
      const token = SessionToken('one-time-secret');
      final binding = CredentialBinding(
        serverOrigin: Uri.parse('https://living.local:8443'),
        certificateSha256: 'AABB',
      );

      await vault.save(ControlCredential(binding: binding, token: token));

      expect((await vault.load(binding))?.token.value, token.value);
    },
  );

  test('revocation invalidates the persisted credential before surfacing',
      () async {
    var invalidations = 0;
    final gateway = HttpControlGateway(
      client: MockClient((_) async => http.Response(
            jsonEncode({'code': 'TOKEN_REVOKED', 'message': 'do-not-reflect'}),
            401,
          )),
      origin: Uri.parse('https://living.local:8443'),
      token: const SessionToken('runtime-generated-token'),
      onCredentialInvalidated: () async => invalidations++,
    );

    await expectLater(gateway.policy(), throwsA(isA<TokenRevokedFailure>()));
    expect(invalidations, 1);
    await expectLater(gateway.policy(), throwsA(isA<TokenRevokedFailure>()));
    expect(invalidations, 1);
  });

  test('failed secure deletion is typed and never reflects credentials',
      () async {
    final gateway = HttpControlGateway(
      client: MockClient((_) async => http.Response(
            jsonEncode({'code': 'TOKEN_REVOKED'}),
            401,
          )),
      origin: Uri.parse('https://living.local:8443'),
      token: const SessionToken('runtime-generated-token'),
      onCredentialInvalidated: () => throw const CredentialVaultFailure(),
    );

    await expectLater(
      gateway.policy(),
      throwsA(
        isA<CredentialInvalidationFailure>().having(
          (failure) => failure.toString(),
          'redacted diagnostic',
          isNot(contains('runtime-generated-token')),
        ),
      ),
    );
  });

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
