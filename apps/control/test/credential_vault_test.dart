import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/credential_vault.dart';

void main() {
  final binding = CredentialBinding(
    serverOrigin: Uri.parse('https://living.local:8443/path?ignored=yes'),
    certificateSha256: 'SHA-256:AA:BB',
  );

  test('save and load preserve a valid Control credential', () async {
    final storage = MemoryCredentialVaultStorage();
    final first = SerializedCredentialVault(storage);
    await first.save(ControlCredential(
      binding: binding,
      token: const SessionToken('runtime-generated-token'),
    ));

    final afterRestart = SerializedCredentialVault(storage);
    final loaded = await afterRestart.load(binding);

    expect(loaded?.token.value, 'runtime-generated-token');
    expect(
        loaded?.binding.serverOrigin, Uri.parse('https://living.local:8443'));
    expect(loaded.toString(), isNot(contains('runtime-generated-token')));
  });

  test('delete revokes the persisted credential', () async {
    final storage = MemoryCredentialVaultStorage();
    final vault = SerializedCredentialVault(storage);
    await vault.save(ControlCredential(
      binding: binding,
      token: const SessionToken('runtime-generated-token'),
    ));

    await vault.delete();

    expect(await vault.load(binding), isNull);
  });

  test('legacy unbound memory-only record is discarded, not trusted', () async {
    final storage = MemoryCredentialVaultStorage();
    await storage.write('{"token":"legacy-token"}');
    final vault = SerializedCredentialVault(storage);

    expect(await vault.load(binding), isNull);
    expect(await storage.read(), isNull);
  });

  test('Server fingerprint change deletes the old credential', () async {
    final storage = MemoryCredentialVaultStorage();
    final vault = SerializedCredentialVault(storage);
    await vault.save(ControlCredential(
      binding: binding,
      token: const SessionToken('runtime-generated-token'),
    ));
    final changed = CredentialBinding(
      serverOrigin: binding.serverOrigin,
      certificateSha256: 'CCDD',
    );

    expect(await vault.load(changed), isNull);
    expect(await storage.read(), isNull);
  });

  test('Server origin change deletes the old credential', () async {
    final storage = MemoryCredentialVaultStorage();
    final vault = SerializedCredentialVault(storage);
    await vault.save(ControlCredential(
      binding: binding,
      token: const SessionToken('runtime-generated-token'),
    ));
    final changed = CredentialBinding(
      serverOrigin: Uri.parse('https://other.local:8443'),
      certificateSha256: binding.certificateSha256,
    );

    expect(await vault.load(changed), isNull);
    expect(await storage.read(), isNull);
  });

  test('corrupt or cross-app payload fails closed and is deleted', () async {
    final storage = MemoryCredentialVaultStorage();
    await storage.write('not a credential');
    final vault = SerializedCredentialVault(storage);

    expect(await vault.load(binding), isNull);
    expect(await storage.read(), isNull);
  });
}
