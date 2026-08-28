import 'dart:io';

import 'package:jastreamer_control/control_events.dart';
import 'package:jastreamer_control/tls_fingerprint.dart';
import 'package:web_socket_channel/io.dart';

EventSocketFactory createEventSocketFactory() => const _IoEventSocketFactory();

final class _IoEventSocketFactory implements EventSocketFactory {
  const _IoEventSocketFactory();

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async {
    if (certificateSha256.trim().isEmpty) {
      throw const FormatException(
        'A Server certificate fingerprint is required.',
      );
    }
    final client =
        HttpClient(context: SecurityContext(withTrustedRoots: false));
    client.badCertificateCallback = (certificate, host, port) =>
        certificateFingerprintMatches(certificate.der, certificateSha256);
    try {
      final channel = IOWebSocketChannel.connect(uri, customClient: client);
      await channel.ready;
      return _IoEventSocket(channel, client);
    } catch (_) {
      client.close(force: true);
      rethrow;
    }
  }
}

final class _IoEventSocket implements EventSocket {
  _IoEventSocket(this._channel, this._client);
  final IOWebSocketChannel _channel;
  final HttpClient _client;

  @override
  Stream<Object?> get messages => _channel.stream;

  @override
  Future<void> close() async {
    await _channel.sink.close(WebSocketStatus.normalClosure);
    _client.close(force: true);
  }
}
