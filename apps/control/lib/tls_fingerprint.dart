import 'dart:typed_data';
import 'package:crypto/crypto.dart';

String _normalizeFingerprint(String value) => value
    .replaceFirst(RegExp(r'^sha-?256:', caseSensitive: false), '')
    .replaceAll(RegExp('[^0-9A-Fa-f]'), '')
    .toLowerCase();

bool advertisedFingerprintsEqual(String left, String right) {
  final normalizedLeft = _normalizeFingerprint(left);
  final normalizedRight = _normalizeFingerprint(right);
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

bool certificateFingerprintMatches(
  Uint8List certificateDer,
  String advertised,
) {
  final actual = sha256.convert(certificateDer).bytes;
  final normalized = _normalizeFingerprint(advertised);
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
