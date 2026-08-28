import 'package:jastreamer_control/credential_vault.dart';
import 'package:web/web.dart' as web;

const _storageKey = 'jastreamer-control-credential-v1';

final class WebSessionCredentialVaultStorage implements CredentialVaultStorage {
  const WebSessionCredentialVaultStorage();

  @override
  Future<String?> read() async {
    try {
      return web.window.sessionStorage.getItem(_storageKey);
    } on Object {
      throw const CredentialVaultFailure();
    }
  }

  @override
  Future<void> write(String value) async {
    try {
      web.window.sessionStorage.setItem(_storageKey, value);
    } on Object {
      throw const CredentialVaultFailure();
    }
  }

  @override
  Future<void> delete() async {
    try {
      web.window.sessionStorage.removeItem(_storageKey);
    } on Object {
      throw const CredentialVaultFailure();
    }
  }
}

CredentialVault createPlatformCredentialVault() =>
    const SerializedCredentialVault(WebSessionCredentialVaultStorage());
