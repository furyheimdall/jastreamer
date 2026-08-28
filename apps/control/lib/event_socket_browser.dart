import 'package:jastreamer_control/control_events.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

EventSocketFactory createEventSocketFactory() =>
    const _BrowserEventSocketFactory();

final class _BrowserEventSocketFactory implements EventSocketFactory {
  const _BrowserEventSocketFactory();

  @override
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  }) async {
    // Browsers exclusively enforce their trust store. The fingerprint is not
    // inspected here and this transport must never be presented as pinned.
    final channel = WebSocketChannel.connect(uri);
    await channel.ready;
    return _BrowserEventSocket(channel);
  }
}

final class _BrowserEventSocket implements EventSocket {
  _BrowserEventSocket(this._channel);
  final WebSocketChannel _channel;

  @override
  Stream<Object?> get messages => _channel.stream;

  @override
  Future<void> close() => _channel.sink.close();
}
