import 'package:http/browser_client.dart';
import 'package:http/http.dart' as http;

http.Client createCertificateBoundClient(String certificateSha256) {
  if (certificateSha256.trim().isEmpty) {
    throw const FormatException(
      'A Server certificate fingerprint is required.',
    );
  }
  return BrowserClient();
}
