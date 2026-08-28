import 'dart:convert';

import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/tls_fingerprint.dart';

import 'credential_vault_factory_stub.dart'
    if (dart.library.io) 'credential_vault_factory_native.dart'
    if (dart.library.js_interop) 'credential_vault_factory_web.dart'
    as vault_factory;

const int controlCredentialSchemaVersion = 1;

final class CredentialBinding {
  CredentialBinding({
    required Uri serverOrigin,
    required String certificateSha256,
  })  : serverOrigin = canonicalServerOrigin(serverOrigin),
        certificateSha256 = normalizeCertificateFingerprint(
          certificateSha256,
        ) {
    if (this.serverOrigin.scheme != 'https' || this.certificateSha256.isEmpty) {
      throw const FormatException(
        'A secure Server origin and certificate fingerprint are required.',
      );
    }
  }

  final Uri serverOrigin;
  final String certificateSha256;

  @override
  bool operator ==(Object other) =>
      other is CredentialBinding &&
      other.serverOrigin == serverOrigin &&
      constantTimeFingerprintEquals(
        other.certificateSha256,
        certificateSha256,
      );

  @override
  int get hashCode => Object.hash(serverOrigin, certificateSha256);
}

final class ControlCredential {
  const ControlCredential({required this.binding, required this.token});

  final CredentialBinding binding;
  final SessionToken token;

  @override
  String toString() => 'ControlCredential(<redacted>)';
}

final class CredentialVaultFailure implements Exception {
  const CredentialVaultFailure();

  @override
  String toString() => 'Secure Control credential storage is unavailable.';
}

abstract interface class CredentialVault {
  Future<ControlCredential?> load(CredentialBinding binding);
  Future<void> save(ControlCredential credential);
  Future<void> delete();
}

abstract interface class CredentialVaultStorage {
  Future<String?> read();
  Future<void> write(String value);
  Future<void> delete();
}

/// Owns the versioned wire record and fail-closed binding checks. Platform
/// storage only ever receives this opaque record and must protect it at rest.
final class SerializedCredentialVault implements CredentialVault {
  const SerializedCredentialVault(this.storage);

  final CredentialVaultStorage storage;

  @override
  Future<ControlCredential?> load(CredentialBinding binding) async {
    final serialized = await storage.read();
    if (serialized == null) return null;
    try {
      final decoded = jsonDecode(serialized);
      if (decoded is! Map<String, Object?> ||
          decoded['schema'] != controlCredentialSchemaVersion ||
          decoded['kind'] != 'control' ||
          decoded['origin'] is! String ||
          decoded['fingerprint'] is! String ||
          decoded['token'] is! String ||
          (decoded['token'] as String).isEmpty) {
        await storage.delete();
        return null;
      }
      final storedBinding = CredentialBinding(
        serverOrigin: Uri.parse(decoded['origin'] as String),
        certificateSha256: decoded['fingerprint'] as String,
      );
      if (storedBinding != binding) {
        await storage.delete();
        return null;
      }
      return ControlCredential(
        binding: storedBinding,
        token: SessionToken(decoded['token'] as String),
      );
    } on FormatException {
      await storage.delete();
      return null;
    } on TypeError {
      await storage.delete();
      return null;
    }
  }

  @override
  Future<void> save(ControlCredential credential) {
    final value = jsonEncode(<String, Object>{
      'schema': controlCredentialSchemaVersion,
      'kind': 'control',
      'origin': credential.binding.serverOrigin.toString(),
      'fingerprint': credential.binding.certificateSha256,
      'token': credential.token.value,
    });
    return storage.write(value);
  }

  @override
  Future<void> delete() => storage.delete();
}

final class MemoryCredentialVaultStorage implements CredentialVaultStorage {
  String? _value;

  @override
  Future<String?> read() async => _value;

  @override
  Future<void> write(String value) async {
    _value = value;
  }

  @override
  Future<void> delete() async {
    _value = null;
  }
}

CredentialVault createPlatformCredentialVault() =>
    vault_factory.createPlatformCredentialVault();
