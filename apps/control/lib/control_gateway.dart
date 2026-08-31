import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:jastreamer_control/control_events.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_wire.dart';
import 'package:jastreamer_control/control_zone_wire.dart';
import 'package:jastreamer_control/event_socket.dart';
import 'package:jastreamer_control/protocol_compatibility.dart';
import 'package:jastreamer_control/tls_client.dart';
part 'control_gateway_discovery.dart';
part 'control_gateway_resources.dart';
part 'control_gateway_mutations.dart';
part 'control_gateway_transport.dart';
part 'control_gateway_views.dart';
part 'control_gateway_live.dart';
part 'control_gateway_parsing.dart';

final class ControlApiFailure extends ControlFailure {
  const ControlApiFailure({
    required super.status,
    required super.code,
    super.message = '',
    super.recoverable = false,
    super.details,
    super.intent,
  });
}

final class CredentialInvalidationFailure extends ControlFailure {
  const CredentialInvalidationFailure()
      : super(
          status: null,
          code: 'CREDENTIAL_INVALIDATION_FAILED',
          message: 'Secure credential invalidation failed.',
          recoverable: true,
        );
}

final class StalePolicyFailure extends ControlApiFailure {
  const StalePolicyFailure({required this.serverRevision})
      : super(status: 412, code: 'STALE_POLICY_REVISION', recoverable: true);
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
  ControlEndpoint({
    required this.client,
    required this.origin,
    this.certificateSha256 = '',
    this.eventSocketFactory,
    this.closeClient,
  });
  factory ControlEndpoint.certificateBound({
    required Uri origin,
    required String certificateSha256,
  }) =>
      ControlEndpoint(
        client: createCertificateBoundClient(certificateSha256),
        origin: origin,
        certificateSha256: certificateSha256,
      );

  final http.Client client;
  final Uri origin;
  final String certificateSha256;
  final EventSocketFactory? eventSocketFactory;
  final void Function()? closeClient;
  void close() {
    final release = closeClient;
    if (release == null) {
      client.close();
    } else {
      release();
    }
  }

  Future<ServerIdentity> identity() async {
    try {
      final response = await client.get(origin.resolve('/api/v1/identity'));
      final body = _object(response);
      return ServerIdentity(
        commonName: requiredString(body, 'common_name'),
        certificateSha256: requiredString(body, 'sha256_fingerprint'),
        pairingUrl: origin.resolve(requiredString(body, 'pairing_url')),
      );
    } catch (error) {
      throw _networkFailure(error);
    }
  }

  HttpControlGateway authenticated(
    SessionToken token, {
    String? certificateSha256,
    EventSocketFactory? eventSocketFactory,
    FutureOr<void> Function()? onCredentialInvalidated,
  }) =>
      HttpControlGateway(
        client: client,
        origin: origin,
        token: token,
        certificateSha256: certificateSha256 ?? this.certificateSha256,
        eventSocketFactory: eventSocketFactory ?? this.eventSocketFactory,
        onCredentialInvalidated: onCredentialInvalidated,
        closeClient: closeClient,
      );
}

void _requireSelectedProtocol(Map<String, Object?> body, int requestedMajor) {
  final selectedMajor = requiredInteger(body, 'protocol_major');
  if (selectedMajor != requestedMajor) {
    throw FormatException(
      'Server selected protocol $selectedMajor for requested major $requestedMajor.',
    );
  }
}

final class HttpControlGateway {
  HttpControlGateway({
    required this.client,
    required this.origin,
    required this.token,
    this.certificateSha256 = '',
    EventSocketFactory? eventSocketFactory,
    FutureOr<void> Function()? onCredentialInvalidated,
    void Function()? closeClient,
  })  : _eventSocketFactory = eventSocketFactory ?? createEventSocketFactory(),
        _onCredentialInvalidated = onCredentialInvalidated,
        _closeClient = closeClient;

  final http.Client client;
  final Uri origin;
  final SessionToken token;
  final String certificateSha256;
  final EventSocketFactory _eventSocketFactory;
  final FutureOr<void> Function()? _onCredentialInvalidated;
  final void Function()? _closeClient;
  int? _protocolMajor;
  bool _credentialInvalidated = false;
  bool _closed = false;
  Future<void>? _closing;
  final Set<ControlLiveSession> _subscriptions = {};

  int? get negotiatedProtocolMajor => _protocolMajor;

  Map<String, String> get _headers => <String, String>{
        'authorization': 'Bearer ${token.value}',
        'accept': 'application/json',
        'x-jake-supported-protocol-majors': controlSupportedProtocolMajors.join(
          ',',
        ),
        if (_protocolMajor case final major?) 'x-jake-protocol-major': '$major',
      };

  Future<void> close() {
    if (_closed) return Future<void>.value();
    final activeClose = _closing;
    if (activeClose != null) return activeClose;
    final closing = _close();
    _closing = closing;
    return closing.whenComplete(() {
      if (identical(_closing, closing)) _closing = null;
    });
  }

  Future<void> _close() async {
    await Future.wait(
      _subscriptions
          .toList(growable: false)
          .map((subscription) => subscription.close()),
    );
    final release = _closeClient;
    if (release == null) {
      client.close();
    } else {
      release();
    }
    _closed = true;
  }
}
