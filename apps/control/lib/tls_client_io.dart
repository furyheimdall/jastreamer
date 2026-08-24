import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';
import 'package:jstreamer_control/tls_fingerprint.dart';

http.Client createCertificateBoundClient(String certificateSha256) {
  if (certificateSha256.trim().isEmpty) {
    throw const FormatException(
      'A Server certificate fingerprint is required.',
    );
  }
  final native = HttpClient(context: SecurityContext(withTrustedRoots: false));
  native.badCertificateCallback = (certificate, host, port) =>
      certificateFingerprintMatches(certificate.der, certificateSha256);
  return IOClient(native);
}
