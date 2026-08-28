import 'package:flutter/services.dart';
import 'package:jastreamer_control/credential_vault.dart';

const _channel = MethodChannel('io.jastreamer.control/credential-vault');

final class NativeCredentialVaultStorage implements CredentialVaultStorage {
  const NativeCredentialVaultStorage([this.channel = _channel]);

  final MethodChannel channel;

  @override
  Future<String?> read() => _invoke(() => channel.invokeMethod<String>('load'));

  @override
  Future<void> write(String value) =>
      _invoke(() => channel.invokeMethod<void>('save', value));

  @override
  Future<void> delete() => _invoke(() => channel.invokeMethod<void>('delete'));

  Future<T> _invoke<T>(Future<T> Function() action) async {
    try {
      return await action();
    } on Object {
      throw const CredentialVaultFailure();
    }
  }
}

CredentialVault createPlatformCredentialVault() =>
    const SerializedCredentialVault(NativeCredentialVaultStorage());
