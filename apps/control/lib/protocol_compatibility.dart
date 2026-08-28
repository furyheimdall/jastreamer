const controlSupportedProtocolMajors = <int>[3, 2];
const supportedProtocolMajorsHeader = 'X-Jake-Supported-Protocol-Majors';
const selectedProtocolMajorHeader = 'X-Jake-Selected-Protocol-Major';

const _controlV2Capabilities = <String>['control-api'];
const _controlV3Capabilities = <String>[
  'control-api',
  'catalog-browse',
  'queue-mutation',
  'transport',
  'zones',
  'renderer-assignment',
  'event-invalidations',
];

final class UnsupportedProtocolMajor implements Exception {
  const UnsupportedProtocolMajor({
    required this.controlMajors,
    required this.serverMajors,
  });

  final List<int> controlMajors;
  final List<int> serverMajors;
  int get httpStatus => 426;
  String get code => 'UNSUPPORTED_PROTOCOL_MAJOR';

  @override
  String toString() => '$code: control=$controlMajors server=$serverMajors';
}

final class ControlProtocolSession {
  const ControlProtocolSession({
    required this.selectedMajor,
    required this.capabilities,
  });

  final int selectedMajor;
  final List<String> capabilities;
}

ControlProtocolSession negotiateControlProtocol(List<int> serverMajors) {
  for (final major in controlSupportedProtocolMajors) {
    if (serverMajors.contains(major)) {
      return ControlProtocolSession(
        selectedMajor: major,
        capabilities:
            major == 3 ? _controlV3Capabilities : _controlV2Capabilities,
      );
    }
  }
  throw UnsupportedProtocolMajor(
    controlMajors: controlSupportedProtocolMajors,
    serverMajors: List<int>.unmodifiable(serverMajors),
  );
}

int selectProtocolMajor(List<int> serverMajors) =>
    negotiateControlProtocol(serverMajors).selectedMajor;

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
    if (capabilities is! List ||
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
