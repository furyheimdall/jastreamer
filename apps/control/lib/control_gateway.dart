import 'dart:convert';
import 'package:http/http.dart' as http;
import 'package:jstreamer_control/control_models.dart';
import 'package:jstreamer_control/control_wire.dart';
import 'package:jstreamer_control/protocol_compatibility.dart';

final class ControlApiFailure implements Exception {
  const ControlApiFailure({required this.status, required this.code});
  final int status;
  final String code;
  @override
  String toString() => 'Control API request failed ($status, $code)';
}

final class StalePolicyFailure extends ControlApiFailure {
  const StalePolicyFailure({required this.serverRevision})
      : super(status: 412, code: 'STALE_POLICY_REVISION');
  final int serverRevision;
}

final class ServerIdentity {
  const ServerIdentity({
    required this.commonName,
    required this.certificateSha256,
    required this.pairingUrl,
  });
  final String commonName;
  final String certificateSha256;
  final Uri pairingUrl;
}

final class ControlEndpoint {
  const ControlEndpoint({required this.client, required this.origin});
  final http.Client client;
  final Uri origin;

  void close() => client.close();

  Future<ServerIdentity> identity() async {
    final response = await client.get(origin.resolve('/api/v1/identity'));
    final body = _object(response);
    return ServerIdentity(
      commonName: requiredString(body, 'common_name'),
      certificateSha256: requiredString(body, 'sha256_fingerprint'),
      pairingUrl: origin.resolve(requiredString(body, 'pairing_url')),
    );
  }

  HttpControlGateway authenticated(SessionToken token) =>
      HttpControlGateway(client: client, origin: origin, token: token);
}

void _requireSelectedProtocol(
  Map<String, Object?> body,
  int requestedMajor,
) {
  final selectedMajor = requiredInteger(body, 'protocol_major');
  if (selectedMajor != requestedMajor) {
    throw FormatException(
      'Server selected protocol $selectedMajor '
      'for requested major $requestedMajor.',
    );
  }
}

final class HttpControlGateway {
  HttpControlGateway({
    required this.client,
    required this.origin,
    required this.token,
  });
  final http.Client client;
  final Uri origin;
  final SessionToken token;
  int? _protocolMajor;

  int? get negotiatedProtocolMajor => _protocolMajor;

  Map<String, String> get _headers => <String, String>{
        'authorization': 'Bearer ${token.value}',
        'accept': 'application/json',
        'x-jake-supported-protocol-majors':
            controlSupportedProtocolMajors.join(','),
        if (_protocolMajor case final major?) 'x-jake-protocol-major': '$major',
      };

  Future<DiscoveryView> discovery() async {
    for (final requestedMajor in controlSupportedProtocolMajors) {
      final response = await _requestDiscovery(requestedMajor);
      if (response.statusCode == 426) continue;
      var body = _object(response);
      var serverMajors = requiredIntegers(
        body,
        'supported_protocol_majors',
      );
      final selectedMajor = selectProtocolMajor(serverMajors);
      _requireSelectedProtocol(body, selectedMajor);
      if (selectedMajor != requestedMajor) {
        body = _object(await _requestDiscovery(selectedMajor));
        serverMajors = requiredIntegers(
          body,
          'supported_protocol_majors',
        );
        if (selectProtocolMajor(serverMajors) != selectedMajor) {
          throw UnsupportedProtocolMajor(
            controlMajors: controlSupportedProtocolMajors,
            serverMajors: serverMajors,
          );
        }
        _requireSelectedProtocol(body, selectedMajor);
      }
      _protocolMajor = selectedMajor;
      return DiscoveryView(
        protocolMajor: selectedMajor,
        supportedProtocolMajors: serverMajors,
        pairingUrl: origin.resolve(requiredString(body, 'pairing_url')),
        certificateSha256: requiredString(body, 'certificate_sha256'),
        capabilities: requiredStrings(body, 'capabilities'),
        contractRevision: requiredString(body, 'contract_revision'),
        catalogRevision: requiredInteger(body, 'catalog_revision'),
      );
    }
    throw const UnsupportedProtocolMajor(
      controlMajors: controlSupportedProtocolMajors,
      serverMajors: <int>[],
    );
  }

  Future<http.Response> _requestDiscovery(int major) => client.get(
        origin.resolve('/api/v1/discovery'),
        headers: <String, String>{
          ..._headers,
          'x-jake-protocol-major': '$major',
        },
      );

  Future<PolicyView> policy() async => parsePolicy(
        _object(
          await client.get(
            origin.resolve('/api/v1/zones/main/continuation-policy'),
            headers: _headers,
          ),
        ),
      );

  Future<PolicyView> updatePolicy(
    PolicyWrite write,
    int expectedRevision,
  ) async {
    final response = await client.patch(
      origin.resolve('/api/v1/zones/main/continuation-policy'),
      headers: <String, String>{
        ..._headers,
        'content-type': 'application/json',
        'if-match': '$expectedRevision',
      },
      body: jsonEncode(<String, Object>{
        'mode': write.mode,
        'artist_gap': write.artistGap,
        'album_gap': write.albumGap,
        'session_override': write.sessionOverride,
      }),
    );
    if (response.statusCode == 412) {
      final etag = response.headers['etag']?.replaceAll('"', '');
      throw StalePolicyFailure(
        serverRevision: int.tryParse(etag ?? '') ?? expectedRevision,
      );
    }
    return parsePolicy(_object(response));
  }

  Future<CatalogView> catalog() async {
    final body = _object(
      await client.get(
        origin.resolve('/api/v1/catalog/status'),
        headers: _headers,
      ),
    );
    return CatalogView(
      revision: requiredInteger(body, 'catalog_revision'),
      trackCount: requiredInteger(body, 'track_count'),
      complete: requiredInteger(body, 'analysis_complete'),
      queued: requiredInteger(body, 'analysis_queued'),
      failed: requiredInteger(body, 'analysis_failed'),
      coverage: requiredInteger(body, 'analysis_coverage'),
    );
  }

  Future<QueueView> queue() async {
    final body = _object(
      await client.get(
        origin.resolve('/api/v1/zones/main/queue'),
        headers: _headers,
      ),
    );
    final rawEntries = body['queue'];
    if (rawEntries is! List<Object?>) {
      throw const FormatException('queue must be an array');
    }
    return QueueView(
      revision: requiredInteger(body, 'revision'),
      entries: rawEntries.map((raw) {
        if (raw is! Map<String, Object?>) {
          throw const FormatException('queue entry must be an object');
        }
        return QueueEntryView(
          trackId: requiredString(raw, 'track_id'),
          state: requiredString(raw, 'state'),
        );
      }).toList(growable: false),
    );
  }

  Future<PreviewView> preview() async {
    final body = _object(
      await client.get(
        origin.resolve('/api/v1/zones/main/automatic-preview'),
        headers: _headers,
      ),
    );
    final raw = body['decision'];
    if (raw is! Map<String, Object?>) {
      throw const FormatException('decision must be an object');
    }
    return PreviewView(
      active: requiredBoolean(body, 'active'),
      replaceable: requiredBoolean(body, 'replaceable'),
      committed: requiredBoolean(body, 'committed'),
      decision: parseDecision(raw),
    );
  }

  Future<DecisionView> explanation() async => parseDecision(
        _object(
          await client.get(
            origin.resolve('/api/v1/zones/main/decision-explanation'),
            headers: _headers,
          ),
        ),
      );
}

Map<String, Object?> _object(http.Response response) {
  final decoded = jsonDecode(response.body);
  if (decoded is! Map<String, Object?>) {
    throw const FormatException('response must be an object');
  }
  if (response.statusCode < 200 || response.statusCode >= 300) {
    throw ControlApiFailure(
      status: response.statusCode,
      code: requiredString(decoded, 'code'),
    );
  }
  return decoded;
}
