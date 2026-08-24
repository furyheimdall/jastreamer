import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/tls_fingerprint.dart';

void main() {
  test(
    'Given matching DER When formatted fingerprint differs Then pin accepts',
    () {
      final der = Uint8List.fromList(utf8.encode('fixture-certificate'));

      expect(
        certificateFingerprintMatches(
          der,
          'SHA256:BF:84:8F:B7:6F:15:65:48:D3:7A:E7:48:F9:63:4F:19:FD:B3:19:3A:23:56:54:B7:57:0F:BB:CC:86:EF:3C:5C',
        ),
        isTrue,
      );
    },
  );

  test(
    'Given equivalent advertised formats Then constant-time text comparison accepts',
    () {
      expect(advertisedFingerprintsEqual('sha256:AA:bb:01', 'aabb01'), isTrue);
      expect(advertisedFingerprintsEqual('aabb01', 'aabb02'), isFalse);
    },
  );

  test('Given mismatching DER Then pin rejects without prefix shortcuts', () {
    final der = Uint8List.fromList(utf8.encode('fixture-certificate'));

    expect(
      certificateFingerprintMatches(
        der,
        '120408107151E84C464479AFA176E0F7DA6A3BEB2B2BEE146182ED953744402B',
      ),
      isFalse,
    );
  });
}
