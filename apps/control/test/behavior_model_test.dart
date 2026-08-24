import 'package:flutter_test/flutter_test.dart';
import 'package:jstreamer_control/behavior_model.dart';

void main() {
  group('Given Todo13 control state', () {
    test(
      'When every advertised reason is parsed Then variants stay exhaustive',
      () {
        const reasons = <String>[
          'PLAY_EXPLICIT',
          'PLAY_ALBUM',
          'PLAY_SIMILAR',
          'BLOCK_EXPLICIT',
          'STOP_MODE_OFF',
          'STOP_NO_ALBUM',
          'STOP_ALBUM_COMPLETE',
          'STOP_SIMILAR_NO_SIGNAL',
          'STOP_SIMILAR_EXHAUSTED',
          'STOP_AUTO_FAILURE_LIMIT',
        ];

        expect(reasons.map(DecisionReason.parse), hasLength(10));
      },
    );

    test('When a stale save is rejected Then user intent survives refresh', () {
      final initial = ControlState.fixture;
      final selected = initial.reduce(const SelectPolicy(Policy.similar));
      final stale = selected.reduce(
        const PolicySaveRejected(serverRevision: 8),
      );
      final recovered = stale.reduce(
        const ServerPolicyRefreshed(policy: Policy.album, revision: 8),
      );

      expect(stale.desiredPolicy, Policy.similar);
      expect(stale.policyRevision, 8);
      expect(recovered.desiredPolicy, Policy.similar);
      expect(recovered.effectivePolicy, Policy.album);
      expect(recovered.hasStaleIntent, isTrue);
    });

    test(
      'When two saves succeed Then each Server revision becomes the next If-Match',
      () {
        final first = ControlState.fixture
            .reduce(const SelectPolicy(Policy.album))
            .markPolicySaved(policy: Policy.album, serverRevision: 8);
        final second = first
            .reduce(const SelectPolicy(Policy.similar))
            .markPolicySaved(policy: Policy.similar, serverRevision: 9);

        expect(first.policyRevision, 8);
        expect(first.effectivePolicy, Policy.album);
        expect(first.isPolicySaved, isTrue);
        expect(second.policyRevision, 9);
        expect(second.effectivePolicy, Policy.similar);
        expect(second.desiredPolicy, Policy.similar);
        expect(second.isPolicySaved, isTrue);
      },
    );

    test(
      'When stale fetch rebases Then desired intent retries on exact revision',
      () {
        final desired = ControlState.fixture.reduce(
          const SelectPolicy(Policy.similar),
        );
        final stale = desired.reduce(
          const PolicySaveRejected(serverRevision: 8),
        );
        final rebased = stale.reduce(
          const ServerPolicyRefreshed(policy: Policy.album, revision: 8),
        );

        expect(rebased.effectivePolicy, Policy.album);
        expect(rebased.desiredPolicy, Policy.similar);
        expect(rebased.policyRevision, 8);
        expect(rebased.isPolicySaved, isFalse);
        final retried = rebased.markPolicySaved(
          policy: Policy.similar,
          serverRevision: 9,
        );
        expect(retried.policyRevision, 9);
        expect(retried.isPolicySaved, isTrue);
      },
    );

    test('When cooldowns reset Then both counters are cleared together', () {
      final reset = ControlState.fixture.reduce(const ResetCooldowns());

      expect(reset.artistCooldown, 4);
      expect(reset.albumCooldown, 10);
    });

    test('When pairing return is verified Then device becomes paired', () {
      final paired = ControlState.fixture
          .reduce(const DiscoverServers())
          .reduce(const OpenPairing(ServerId('living-room')))
          .reduce(const PairingReturned(ServerId('living-room')));

      expect(paired.servers.single.pairing, PairingStatus.paired);
      expect(paired.servers.single.token, isNull);
      expect(paired.servers.single.pairingUrl.scheme, 'https');
    });
  });
}
