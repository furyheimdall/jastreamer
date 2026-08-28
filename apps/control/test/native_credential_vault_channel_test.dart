import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/credential_vault.dart';
import 'package:jastreamer_control/credential_vault_factory_native.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('credential-vault-contract-test');
  String? protectedPlatformValue;

  setUp(() {
    protectedPlatformValue = null;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      switch (call.method) {
        case 'load':
          return protectedPlatformValue;
        case 'save':
          protectedPlatformValue = call.arguments as String;
        case 'delete':
          protectedPlatformValue = null;
        default:
          throw PlatformException(code: 'unsupported');
      }
      return null;
    });
  });

  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
  });

  test('native channel supplements the shared save/load/delete contract',
      () async {
    const storage = NativeCredentialVaultStorage(channel);
    const vault = SerializedCredentialVault(storage);
    final binding = CredentialBinding(
      serverOrigin: Uri.parse('https://living.local:8443'),
      certificateSha256: 'AABB',
    );
    await vault.save(ControlCredential(
      binding: binding,
      token: const SessionToken('channel-token'),
    ));

    expect((await vault.load(binding))?.token.value, 'channel-token');
    await vault.delete();
    expect(await vault.load(binding), isNull);
  });
}
