import 'package:jastreamer_control/credential_vault.dart';

CredentialVault createPlatformCredentialVault() =>
    SerializedCredentialVault(MemoryCredentialVaultStorage());
