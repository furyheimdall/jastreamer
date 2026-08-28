import 'package:http/http.dart' as http;
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/credential_vault.dart';
import 'package:jastreamer_control/tls_client.dart';
import 'package:url_launcher/url_launcher.dart';

typedef ControlClientFactory = http.Client Function(String? certificateSha256);

abstract interface class ExternalLauncher {
  Future<bool> open(Uri uri);
}

final class SystemExternalLauncher implements ExternalLauncher {
  const SystemExternalLauncher();
  @override
  Future<bool> open(Uri uri) => launchUrl(
        uri,
        mode: LaunchMode.externalApplication,
        webOnlyWindowName: '_blank',
      );
}

final class ControlPlatform {
  ControlPlatform({
    ControlClientFactory? clientFactory,
    CredentialVault? vault,
    ExternalLauncher? launcher,
  })  : _clientFactory = clientFactory ?? _defaultClientFactory,
        vault = vault ?? createPlatformCredentialVault(),
        launcher = launcher ?? const SystemExternalLauncher();

  final ControlClientFactory _clientFactory;
  final CredentialVault vault;
  final ExternalLauncher launcher;
  final List<http.Client> _clients = <http.Client>[];

  ControlEndpoint probe(Uri origin) =>
      ControlEndpoint(client: _track(_clientFactory(null)), origin: origin);

  ControlEndpoint endpoint(DiscoveredServer server) => ControlEndpoint(
        client: _track(_clientFactory(server.certificateSha256)),
        origin: server.origin,
        certificateSha256: server.certificateSha256,
      );

  http.Client _track(http.Client client) {
    _clients.add(client);
    return client;
  }

  Future<void> dispose() async {
    for (final client in _clients) {
      client.close();
    }
  }

  static http.Client _defaultClientFactory(String? fingerprint) =>
      fingerprint == null
          ? http.Client()
          : createCertificateBoundClient(fingerprint);
}
