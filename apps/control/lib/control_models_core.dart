part of 'control_models.dart';

final class SessionToken {
  const SessionToken(this.value);

  final String value;

  @override
  bool operator ==(Object other) =>
      other is SessionToken && other.value == value;

  @override
  int get hashCode => value.hashCode;

  @override
  String toString() => '<redacted-control-credential>';
}

extension type const ZoneId(String value) {}
extension type const RendererId(String value) {}
extension type const TrackId(String value) {}
extension type const QueueEntryId(String value) {}
extension type const CommandId(String value) {}
extension type const CatalogCursor(String value) {}
extension type const IdempotencyKey(String value) {}

final class EntityTag {
  const EntityTag(this.revision);
  final int revision;

  static EntityTag? parse(String? value) {
    if (value == null) return null;
    final parsed = int.tryParse(value.replaceAll('"', '').trim());
    return parsed == null || parsed < 0 ? null : EntityTag(parsed);
  }
}

final class WireValue<T extends Enum> {
  const WireValue(this.wireValue, this.known);
  final String wireValue;
  final T? known;
  bool get isKnown => known != null;

  @override
  bool operator ==(Object other) =>
      other is WireValue<T> &&
      other.wireValue == wireValue &&
      other.known == known;
  @override
  int get hashCode => Object.hash(wireValue, known);
  @override
  String toString() => wireValue;
}

enum QueueEntryState { pending, reserved, playing, blocked }

enum LogicalTransport {
  idle,
  selecting,
  starting,
  playing,
  paused,
  blocked,
  suspended,
}

enum ObservedTransport { unknown, idle, playing, paused, stopped, error }

enum RendererKind { k17, custom }

enum RendererStatus { unavailable, available, connected, incompatible, revoked }

enum MediaRepresentationKind { original, l16 }

enum TransportMutationStatus { pending, received, terminal }

WireValue<T> parseWireValue<T extends Enum>(String value, List<T> values) {
  for (final candidate in values) {
    if (candidate.name == value) return WireValue<T>(value, candidate);
  }
  return WireValue<T>(value, null);
}

final class DiscoveryView {
  const DiscoveryView({
    required this.protocolMajor,
    required this.supportedProtocolMajors,
    required this.pairingUrl,
    required this.certificateSha256,
    required this.capabilities,
    required this.contractRevision,
    required this.catalogRevision,
  });
  final int protocolMajor;
  final List<int> supportedProtocolMajors;
  final Uri pairingUrl;
  final String certificateSha256;
  final List<String> capabilities;
  final String contractRevision;
  final int catalogRevision;
}

final class PolicyView {
  const PolicyView({
    required this.mode,
    required this.artistGap,
    required this.albumGap,
    required this.sessionOverride,
    required this.revision,
  });
  final WirePolicy mode;
  final int artistGap;
  final int albumGap;
  final WirePolicy? sessionOverride;
  final int revision;
}

final class PolicyWrite {
  const PolicyWrite({
    required this.mode,
    required this.artistGap,
    required this.albumGap,
    required this.sessionOverride,
  });
  final String mode;
  final int artistGap;
  final int albumGap;
  final String sessionOverride;
}
