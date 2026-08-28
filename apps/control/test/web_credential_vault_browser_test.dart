@TestOn('browser')
library;

import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/credential_vault.dart';
import 'package:jastreamer_control/credential_vault_factory_web.dart';
import 'package:web/web.dart' as web;

void main() {
  const storage = WebSessionCredentialVaultStorage();
  final binding = CredentialBinding(
    serverOrigin: Uri.parse('https://living.local:8443'),
    certificateSha256: 'AABB',
  );

  setUp(storage.delete);
  tearDown(storage.delete);

  test('same browser tab session survives a vault reconstruction', () async {
    const first = SerializedCredentialVault(storage);
    await first.save(ControlCredential(
      binding: binding,
      token: const SessionToken('browser-session-token'),
    ));

    const reconstructed = SerializedCredentialVault(storage);
    expect((await reconstructed.load(binding))?.token.value,
        'browser-session-token');
    expect(
      web.window.localStorage.getItem('jastreamer-control-credential-v1'),
      isNull,
    );
  });

  test('session deletion removes the browser credential', () async {
    const vault = SerializedCredentialVault(storage);
    await vault.save(ControlCredential(
      binding: binding,
      token: const SessionToken('browser-session-token'),
    ));
    await vault.delete();

    expect(await vault.load(binding), isNull);
  });
}
