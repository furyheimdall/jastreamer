import 'dart:typed_data';
import 'package:crypto/crypto.dart';

String normalizeCertificateFingerprint(String value) => value
    .replaceFirst(RegExp(r'^sha-?256:', caseSensitive: false), '')
    .replaceAll(RegExp('[^0-9A-Fa-f]'), '')
    .toLowerCase();

bool constantTimeFingerprintEquals(String left, String right) {
  final normalizedLeft = normalizeCertificateFingerprint(left);
  final normalizedRight = normalizeCertificateFingerprint(right);
  if (normalizedLeft.length != normalizedRight.length ||
      normalizedLeft.isEmpty) {
    return false;
  }
  var difference = 0;
  for (var index = 0; index < normalizedLeft.length; index++) {
    difference |=
        normalizedLeft.codeUnitAt(index) ^ normalizedRight.codeUnitAt(index);
  }
  return difference == 0;
}

bool advertisedFingerprintsEqual(String left, String right) =>
    constantTimeFingerprintEquals(left, right);

Uri canonicalServerOrigin(Uri value) {
  final defaultPort = value.scheme == 'https' ? 443 : null;
  return Uri(
    scheme: value.scheme.toLowerCase(),
    host: value.host.toLowerCase(),
    port: value.hasPort && value.port != defaultPort ? value.port : null,
  );
}

bool certificateFingerprintMatches(
  Uint8List certificateDer,
  String advertised,
) {
  final actual = sha256.convert(certificateDer).bytes;
  final normalized = normalizeCertificateFingerprint(advertised);
  if (normalized.length != actual.length * 2) return false;
  var difference = 0;
  for (var index = 0; index < actual.length; index++) {
    final expected = int.tryParse(
      normalized.substring(index * 2, index * 2 + 2),
      radix: 16,
    );
    if (expected == null) return false;
    difference |= actual[index] ^ expected;
  }
  return difference == 0;
}
