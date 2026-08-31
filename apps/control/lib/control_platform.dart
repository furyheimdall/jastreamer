import 'package:http/http.dart' as http;
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_events.dart';
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
    EventSocketFactory? eventSocketFactory,
    CredentialVault? vault,
    ExternalLauncher? launcher,
  })  : _clientFactory = clientFactory ?? _defaultClientFactory,
        _eventSocketFactory = eventSocketFactory,
        vault = vault ?? createPlatformCredentialVault(),
        launcher = launcher ?? const SystemExternalLauncher();

  final ControlClientFactory _clientFactory;
  final EventSocketFactory? _eventSocketFactory;
  final CredentialVault vault;
  final ExternalLauncher launcher;
  final Set<http.Client> _clients = <http.Client>{};

  ControlEndpoint probe(Uri origin) {
    final client = _track(_clientFactory(null));
    return ControlEndpoint(
      client: client,
      origin: origin,
      closeClient: () => _release(client),
    );
  }

  ControlEndpoint endpoint(DiscoveredServer server) {
    final client = _track(_clientFactory(server.certificateSha256));
    return ControlEndpoint(
      client: client,
      origin: server.origin,
      certificateSha256: server.certificateSha256,
      eventSocketFactory: _eventSocketFactory,
      closeClient: () => _release(client),
    );
  }

  http.Client _track(http.Client client) {
    _clients.add(client);
    return client;
  }

  void _release(http.Client client) {
    if (_clients.remove(client)) client.close();
  }

  Future<void> dispose() async {
    for (final client in _clients.toList(growable: false)) {
      client.close();
    }
    _clients.clear();
  }

  static http.Client _defaultClientFactory(String? fingerprint) =>
      fingerprint == null
          ? http.Client()
          : createCertificateBoundClient(fingerprint);
}
