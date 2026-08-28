import 'package:http/browser_client.dart';
import 'package:http/http.dart' as http;

enum CertificateBindingMode { nativeFingerprint, browserTrust }

CertificateBindingMode get certificateBindingMode =>
    CertificateBindingMode.browserTrust;

http.Client createCertificateBoundClient(String certificateSha256) {
  // Browser TLS is validated only by the browser trust store. Dart cannot
  // inspect or override the peer certificate from this client.
  return BrowserClient();
}
