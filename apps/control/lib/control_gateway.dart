import 'dart:convert';
import 'package:http/http.dart' as http;
import 'package:jstreamer_control/behavior_model.dart';
import 'package:jstreamer_control/control_models.dart';

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
      commonName: _string(body, 'common_name'),
      certificateSha256: _string(body, 'sha256_fingerprint'),
      pairingUrl: origin.resolve(_string(body, 'pairing_url')),
    );
  }

  HttpControlGateway authenticated(SessionToken token) =>
      HttpControlGateway(client: client, origin: origin, token: token);
}

final class HttpControlGateway {
  const HttpControlGateway({
    required this.client,
    required this.origin,
    required this.token,
  });
  final http.Client client;
  final Uri origin;
  final SessionToken token;

  Map<String, String> get _headers => <String, String>{
        'authorization': 'Bearer ${token.value}',
        'accept': 'application/json',
      };

  Future<DiscoveryView> discovery() async {
    final response = await client.get(
      origin.resolve('/api/v1/discovery'),
      headers: <String, String>{..._headers, 'x-jake-protocol-major': '2'},
    );
    final body = _object(response);
    return DiscoveryView(
      pairingUrl: origin.resolve(_string(body, 'pairing_url')),
      certificateSha256: _string(body, 'certificate_sha256'),
      capabilities: _strings(body, 'capabilities'),
      contractRevision: _string(body, 'contract_revision'),
      catalogRevision: _integer(body, 'catalog_revision'),
    );
  }

  Future<PolicyView> policy() async => _policy(
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
    return _policy(_object(response));
  }

  Future<CatalogView> catalog() async {
    final body = _object(
      await client.get(
        origin.resolve('/api/v1/catalog/status'),
        headers: _headers,
      ),
    );
    return CatalogView(
      revision: _integer(body, 'catalog_revision'),
      trackCount: _integer(body, 'track_count'),
      complete: _integer(body, 'analysis_complete'),
      queued: _integer(body, 'analysis_queued'),
      failed: _integer(body, 'analysis_failed'),
      coverage: _integer(body, 'analysis_coverage'),
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
      revision: _integer(body, 'revision'),
      entries: rawEntries.map((raw) {
        if (raw is! Map<String, Object?>) {
          throw const FormatException('queue entry must be an object');
        }
        return QueueEntryView(
          trackId: _string(raw, 'track_id'),
          state: _string(raw, 'state'),
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
      active: _boolean(body, 'active'),
      replaceable: _boolean(body, 'replaceable'),
      committed: _boolean(body, 'committed'),
      decision: _decision(raw),
    );
  }

  Future<DecisionView> explanation() async => _decision(
        _object(
          await client.get(
            origin.resolve('/api/v1/zones/main/decision-explanation'),
            headers: _headers,
          ),
        ),
      );
}

PolicyView _policy(Map<String, Object?> body) => PolicyView(
      mode: policyFromWire(_string(body, 'mode')),
      artistGap: _integer(body, 'artist_gap'),
      albumGap: _integer(body, 'album_gap'),
      sessionOverride: optionalPolicyFromWire(
        body['session_override'] is String
            ? _string(body, 'session_override')
            : '',
      ),
      revision: _integer(body, 'revision'),
    );

DecisionView _decision(Map<String, Object?> body) => DecisionView(
      reason: DecisionReason.parse(_string(body, 'reason')),
      source: body['source'] is String ? _string(body, 'source') : '',
      trackId: body['track_id'] is String ? _string(body, 'track_id') : '',
      signalCoverage: _integer(body, 'signal_coverage'),
      catalogRevision: _integer(body, 'catalog_revision'),
      policyRevision: _integer(body, 'policy_revision'),
    );

Map<String, Object?> _object(http.Response response) {
  final decoded = jsonDecode(response.body);
  if (decoded is! Map<String, Object?>) {
    throw const FormatException('response must be an object');
  }
  if (response.statusCode < 200 || response.statusCode >= 300) {
    throw ControlApiFailure(
      status: response.statusCode,
      code: _string(decoded, 'code'),
    );
  }
  return decoded;
}

String _string(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! String) throw FormatException('$key must be a string');
  return field;
}

int _integer(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! int) throw FormatException('$key must be an integer');
  return field;
}

bool _boolean(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! bool) throw FormatException('$key must be a boolean');
  return field;
}

List<String> _strings(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! List<Object?> || field.any((item) => item is! String)) {
    throw FormatException('$key must be a string array');
  }
  return field.whereType<String>().toList(growable: false);
}
