const controlSupportedProtocolMajors = <int>[2, 1];

final class UnsupportedProtocolMajor implements Exception {
  const UnsupportedProtocolMajor({
    required this.controlMajors,
    required this.serverMajors,
  });

  final List<int> controlMajors;
  final List<int> serverMajors;
  String get code => 'UNSUPPORTED_PROTOCOL_MAJOR';

  @override
  String toString() => '$code: control=$controlMajors server=$serverMajors';
}

int selectProtocolMajor(List<int> serverMajors) {
  for (final major in controlSupportedProtocolMajors) {
    if (serverMajors.contains(major)) return major;
  }
  throw UnsupportedProtocolMajor(
    controlMajors: controlSupportedProtocolMajors,
    serverMajors: List<int>.unmodifiable(serverMajors),
  );
}

final class CompatibilityFixture {
  const CompatibilityFixture({
    required this.protocolMajor,
    required this.unknownCapabilities,
    required this.commandKind,
  });

  factory CompatibilityFixture.parse(Map<String, Object?> value) {
    final protocolMajor = value['protocolMajor'];
    final capabilities = value['capabilities'];
    final commandKind = value['commandKind'];
    if (protocolMajor is! int) {
      throw const FormatException('protocolMajor must be an integer');
    }
    if (capabilities is! List<Object?> ||
        capabilities.any((capability) => capability is! String)) {
      throw const FormatException('capabilities must be a string array');
    }
    if (commandKind is! String) {
      throw const FormatException('commandKind must be a string');
    }
    return CompatibilityFixture(
      protocolMajor: protocolMajor,
      unknownCapabilities: capabilities
          .whereType<String>()
          .where((capability) => !knownCapabilities.contains(capability))
          .toList(growable: false),
      commandKind: WireCommandKind.parse(commandKind),
    );
  }

  static const knownCapabilities = <String>{
    'catalog-status',
    'queue',
    'continuation-policy',
    'automatic-preview',
    'decision-explanation',
    'wss-state',
    'play',
  };

  final int protocolMajor;
  final List<String> unknownCapabilities;
  final WireCommandKind commandKind;
}

sealed class WireCommandKind {
  const WireCommandKind(this.wireValue);

  factory WireCommandKind.parse(String value) => switch (value) {
        'play' || 'pause' || 'stop' => KnownWireCommandKind(value),
        _ => UnknownWireCommandKind(value),
      };

  final String wireValue;
  String get state;
}

final class KnownWireCommandKind extends WireCommandKind {
  const KnownWireCommandKind(super.wireValue);

  @override
  String get state => 'known';
}

final class UnknownWireCommandKind extends WireCommandKind {
  const UnknownWireCommandKind(super.wireValue);

  @override
  String get state => 'unknown';
}
