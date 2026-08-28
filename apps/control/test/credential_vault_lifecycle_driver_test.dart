import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/credential_vault.dart';

void main() {
  test('restart upgrade revocation and identity-change lifecycle driver',
      () async {
    final storage = MemoryCredentialVaultStorage();
    final original = SerializedCredentialVault(storage);
    final binding = CredentialBinding(
      serverOrigin: Uri.parse('https://living.local:8443'),
      certificateSha256: 'AABB',
    );
    await original.save(ControlCredential(
      binding: binding,
      token: const SessionToken('driver-runtime-token'),
    ));

    // Restart and a same-signer upgrade reconstruct Dart objects while the
    // platform-owned protected storage remains.
    final afterUpgrade = SerializedCredentialVault(storage);
    final restored = await afterUpgrade.load(binding);
    expect(restored?.token.value, 'driver-runtime-token');

    final gateway = HttpControlGateway(
      client: MockClient((_) async => http.Response(
            jsonEncode({'code': 'TOKEN_REVOKED'}),
            401,
          )),
      origin: binding.serverOrigin,
      token: restored!.token,
      onCredentialInvalidated: afterUpgrade.delete,
    );
    await expectLater(gateway.policy(), throwsA(isA<TokenRevokedFailure>()));
    expect(await afterUpgrade.load(binding), isNull);

    await afterUpgrade.save(ControlCredential(
      binding: binding,
      token: const SessionToken('replacement-runtime-token'),
    ));
    final changedIdentity = CredentialBinding(
      serverOrigin: binding.serverOrigin,
      certificateSha256: 'CCDD',
    );
    expect(await afterUpgrade.load(changedIdentity), isNull);
    expect(await storage.read(), isNull);
  });
}
