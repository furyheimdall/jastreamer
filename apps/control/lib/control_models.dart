import 'package:jstreamer_control/behavior_model.dart';

extension type const SessionToken(String value) {}

final class DiscoveryView {
  const DiscoveryView({
    required this.pairingUrl,
    required this.certificateSha256,
    required this.capabilities,
    required this.contractRevision,
    required this.catalogRevision,
  });
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
  final Policy mode;
  final int artistGap;
  final int albumGap;
  final Policy? sessionOverride;
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

Policy policyFromWire(String value) => switch (value) {
      'stop' => Policy.stop,
      'album' => Policy.album,
      'similar' => Policy.similar,
      _ => throw FormatException('Unknown Todo13 policy: $value'),
    };

Policy? optionalPolicyFromWire(String value) =>
    value.isEmpty ? null : policyFromWire(value);
