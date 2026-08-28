part of 'control_gateway.dart';

extension HttpControlGatewayDiscovery on HttpControlGateway {
  Future<DiscoveryView> discovery() async {
    try {
      for (final requestedMajor in controlSupportedProtocolMajors) {
        final response = await _requestDiscovery(requestedMajor);
        if (response.statusCode == 426) continue;
        var body = _object(response);
        var serverMajors = requiredIntegers(body, 'supported_protocol_majors');
        final selectedMajor = selectProtocolMajor(serverMajors);
        _requireSelectedProtocol(body, selectedMajor);
        if (selectedMajor != requestedMajor) {
          body = _object(await _requestDiscovery(selectedMajor));
          serverMajors = requiredIntegers(body, 'supported_protocol_majors');
          if (selectProtocolMajor(serverMajors) != selectedMajor) {
            throw UnsupportedProtocolMajor(
              controlMajors: controlSupportedProtocolMajors,
              serverMajors: serverMajors,
            );
          }
          _requireSelectedProtocol(body, selectedMajor);
        }
        _protocolMajor = selectedMajor;
        return DiscoveryView(
          protocolMajor: selectedMajor,
          supportedProtocolMajors: serverMajors,
          pairingUrl: origin.resolve(requiredString(body, 'pairing_url')),
          certificateSha256: requiredString(body, 'certificate_sha256'),
          capabilities: requiredStrings(body, 'capabilities'),
          contractRevision: requiredString(body, 'contract_revision'),
          catalogRevision: requiredInteger(body, 'catalog_revision'),
        );
      }
      throw const UnsupportedProtocolMajor(
        controlMajors: controlSupportedProtocolMajors,
        serverMajors: <int>[],
      );
    } catch (error) {
      if (error is UnsupportedProtocolMajor ||
          error is FormatException ||
          error is ControlFailure) {
        rethrow;
      }
      throw _networkFailure(error);
    }
  }

  Future<http.Response> _requestDiscovery(int major) async {
    late final http.Response response;
    try {
      response = await client.get(
        origin.resolve('/api/v1/discovery'),
        headers: <String, String>{
          ..._headers,
          'x-jake-protocol-major': '$major',
        },
      );
    } catch (error) {
      await _invalidateForCertificateFailure(error);
      throw _networkFailure(error);
    }
    await _observeCredentialFailure(response);
    return response;
  }
}
