part of 'control_models.dart';

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
