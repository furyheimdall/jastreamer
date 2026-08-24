import 'package:http/http.dart' as http;
import 'package:jstreamer_control/behavior_model.dart';
import 'package:jstreamer_control/control_gateway.dart';
import 'package:jstreamer_control/control_models.dart';
import 'package:jstreamer_control/tls_client.dart';
import 'package:url_launcher/url_launcher.dart';

typedef ControlClientFactory = http.Client Function(String? certificateSha256);

abstract interface class TokenVault {
  Uri? get callbackUri;
  Future<SessionToken?> read();
  Future<void> store(SessionToken token);
  Future<void> clear();
}

final class MemoryTokenVault implements TokenVault {
  SessionToken? _token;
  @override
  Uri? get callbackUri => null;
  @override
  Future<SessionToken?> read() async => _token;
  @override
  Future<void> store(SessionToken token) async {
    _token = token;
  }

  @override
  Future<void> clear() async {
    _token = null;
  }
}

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
    TokenVault? vault,
    ExternalLauncher? launcher,
  })  : _clientFactory = clientFactory ?? _defaultClientFactory,
        vault = vault ?? MemoryTokenVault(),
        launcher = launcher ?? const SystemExternalLauncher();

  final ControlClientFactory _clientFactory;
  final TokenVault vault;
  final ExternalLauncher launcher;
  final List<http.Client> _clients = <http.Client>[];

  ControlEndpoint probe(Uri origin) =>
      ControlEndpoint(client: _track(_clientFactory(null)), origin: origin);

  ControlEndpoint endpoint(DiscoveredServer server) => ControlEndpoint(
        client: _track(_clientFactory(server.certificateSha256)),
        origin: server.origin,
      );

  http.Client _track(http.Client client) {
    _clients.add(client);
    return client;
  }

  Future<void> dispose() async {
    await vault.clear();
    for (final client in _clients) {
      client.close();
    }
  }

  static http.Client _defaultClientFactory(String? fingerprint) =>
      fingerprint == null
          ? http.Client()
          : createCertificateBoundClient(fingerprint);
}
