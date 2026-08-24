import 'package:jastreamer_control/behavior_model.dart';

extension type const SessionToken(String value) {}

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

final class CatalogView {
  const CatalogView({
    required this.revision,
    required this.trackCount,
    required this.complete,
    required this.queued,
    required this.failed,
    required this.coverage,
  });
  final int revision;
  final int trackCount;
  final int complete;
  final int queued;
  final int failed;
  final int coverage;
}

final class QueueEntryView {
  const QueueEntryView({required this.trackId, required this.state});
  final String trackId;
  final String state;
  bool get isUnavailableHead => state == 'blocked';
}

final class QueueView {
  const QueueView({required this.revision, required this.entries});
  final int revision;
  final List<QueueEntryView> entries;
}

final class DecisionView {
  const DecisionView({
    required this.reason,
    required this.source,
    required this.trackId,
    required this.signalCoverage,
    required this.catalogRevision,
    required this.policyRevision,
  });
  final DecisionReason reason;
  final String source;
  final String trackId;
  final int signalCoverage;
  final int catalogRevision;
  final int policyRevision;
}

final class PreviewView {
  const PreviewView({
    required this.active,
    required this.replaceable,
    required this.committed,
    required this.decision,
  });
  final bool active;
  final bool replaceable;
  final bool committed;
  final DecisionView decision;
}

sealed class WirePolicy {
  const WirePolicy(this.wireValue);

  factory WirePolicy.parse(String value) => switch (value) {
        'stop' => const KnownWirePolicy(Policy.stop, 'stop'),
        'album' => const KnownWirePolicy(Policy.album, 'album'),
        'similar' => const KnownWirePolicy(Policy.similar, 'similar'),
        _ => UnknownWirePolicy(value),
      };

  final String wireValue;
  Policy? get known;
}

final class KnownWirePolicy extends WirePolicy {
  const KnownWirePolicy(this.value, super.wireValue);

  final Policy value;

  @override
  Policy get known => value;
}

final class UnknownWirePolicy extends WirePolicy {
  const UnknownWirePolicy(super.wireValue);

  @override
  Policy? get known => null;
}
