part of 'control_gateway.dart';

extension HttpControlGatewayTransport on HttpControlGateway {
  Future<http.Response> _mutation({
    required String path,
    required int expectedRevision,
    required IdempotencyKey idempotencyKey,
    required MutationIntent intent,
  }) =>
      _send(
        'POST',
        path,
        headers: {
          'if-match': '$expectedRevision',
          'idempotency-key': idempotencyKey.value,
        },
        body: intent.toJson(),
        intent: intent,
      );

  void _requireSubscription(ControlLiveSession? value) {
    if (value == null ||
        value._gateway != this ||
        !value.isActive ||
        !_subscriptions.contains(value)) {
      throw const SubscriptionRequiredFailure();
    }
  }

  Future<http.Response> _get(String path) => _getUri(origin.resolve(path));

  Future<http.Response> _getUri(Uri uri) async {
    late final http.Response response;
    try {
      response = await client.get(uri, headers: _headers);
    } catch (error) {
      await _invalidateForCertificateFailure(error);
      throw _networkFailure(error);
    }
    await _observeCredentialFailure(response);
    return response;
  }

  Future<http.Response> _send(
    String method,
    String path, {
    Map<String, String> headers = const {},
    Map<String, Object?>? body,
    MutationIntent? intent,
  }) async {
    final request = http.Request(method, origin.resolve(path));
    request.headers.addAll({..._headers, ...headers});
    if (body != null) {
      request.headers['content-type'] = 'application/json';
      request.body = jsonEncode(body);
    }
    late final http.Response response;
    try {
      response = await http.Response.fromStream(await client.send(request));
    } catch (error) {
      await _invalidateForCertificateFailure(error);
      throw _networkFailure(error, intent: intent);
    }
    await _observeCredentialFailure(response);
    return response;
  }

  Future<void> _observeCredentialFailure(http.Response response) async {
    if (response.statusCode != 401) return;
    try {
      final decoded = jsonDecode(response.body);
      if (decoded is Map<String, Object?> &&
          decoded['code'] == 'TOKEN_REVOKED') {
        await _invalidateCredential();
      }
    } on FormatException {
      // The response parser reports malformed JSON without exposing its body.
    }
  }

  Future<void> _invalidateForCertificateFailure(Object error) async {
    final message = error.toString().toLowerCase();
    if (error is CertificateIdentityChangedFailure ||
        message.contains('certificate') ||
        message.contains('handshake')) {
      await _invalidateCredential();
    }
  }

  Future<void> _invalidateCredential() async {
    if (_credentialInvalidated) return;
    try {
      await _onCredentialInvalidated?.call();
      _credentialInvalidated = true;
    } on Object {
      throw const CredentialInvalidationFailure();
    }
  }
}
