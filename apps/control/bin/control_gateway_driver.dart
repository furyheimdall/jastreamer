import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:jastreamer_control/control_events.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/control_models.dart';
part 'control_gateway_driver_waits.dart';
part 'control_gateway_driver_happy.dart';
part 'control_gateway_driver_events.dart';
part 'control_gateway_driver_failures.dart';

var _idempotencySequence = 0;

Future<void> main(List<String> arguments) async {
  final origin = _requiredEnvironment('JASTREAMER_SERVER_URL');
  final token = _requiredEnvironment('JASTREAMER_CONTROL_TOKEN');
  final fingerprint = _requiredEnvironment('JASTREAMER_CERTIFICATE_SHA256');
  final zoneId = ZoneId(Platform.environment['JASTREAMER_ZONE_ID'] ?? 'main');
  final mode = arguments.singleOrNull ?? '--happy';
  var credentialClears = 0;

  final endpoint = ControlEndpoint.certificateBound(
    origin: Uri.parse(origin),
    certificateSha256: fingerprint,
  );
  final gateway = endpoint.authenticated(
    SessionToken(token),
    onCredentialInvalidated: () => credentialClears++,
  );
  try {
    switch (mode) {
      case '--happy':
        await _happy(gateway, zoneId);
      case '--wait-catalog':
        await _waitCatalog(gateway);
      case '--wait-renderer':
        await _waitRenderer(gateway);
      case '--event-gap':
        await _eventGap(gateway, zoneId);
      case '--unknown-enum':
        await _unknownEnum(gateway);
      case '--offline':
        await _offline(gateway, zoneId);
      case '--revoked':
        await _expectReadFailure<TokenRevokedFailure>(
          gateway,
          'TOKEN_REVOKED',
          credentialClears: () => credentialClears,
        );
      case '--certificate-mismatch':
        await _expectReadFailure<CertificateIdentityChangedFailure>(
          gateway,
          'CERTIFICATE_MISMATCH',
          credentialClears: () => credentialClears,
        );
      default:
        throw FormatException('Unknown driver mode: $mode');
    }
  } finally {
    await gateway.close();
  }
}

String _requiredEnvironment(String name) {
  final value = Platform.environment[name];
  if (value == null || value.isEmpty) {
    throw FormatException('$name is required.');
  }
  return value;
}

String _newIdempotencyKey() {
  _idempotencySequence++;
  final run = Platform.environment['JASTREAMER_RUN_ID'] ?? 'gateway';
  return 'task15-$run-${_idempotencySequence.toString().padLeft(3, '0')}';
}
