import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_policy_state.dart';
import 'package:jastreamer_control/protocol_compatibility.dart';

void main() {
  test(
    'Given v1 and v2 Server majors When selected Then highest common wins',
    () {
      const serverMajors = <int>[1, 2];

      final selected = selectProtocolMajor(serverMajors);

      expect(controlSupportedProtocolMajors, [2, 1]);
      expect(selected, 2);
    },
  );

  test(
    'Given an unknown policy When applied Then behavioral state is unchanged',
    () {
      final state = ControlState.fixture.reduce(
        const SelectPolicy(Policy.similar),
      );
      const policy = PolicyView(
        mode: UnknownWirePolicy('future-mode'),
        artistGap: 99,
        albumGap: 99,
        sessionOverride: UnknownWirePolicy('future-override'),
        revision: 99,
      );

      final result = applyPolicyView(state, policy);

      expect(result, same(state));
      expect(result.desiredPolicy, Policy.similar);
    },
  );

  test(
    'Given additive fixture fields When parsed Then unknown values are explicit',
    () {
      final fixture = <String, Object?>{
        'protocolMajor': 1,
        'capabilities': ['play', 'future-capability'],
        'commandKind': 'future-command',
        'optionalMetadata': 'additive',
      };

      final parsed = CompatibilityFixture.parse(fixture);

      expect(parsed.protocolMajor, 1);
      expect(parsed.unknownCapabilities, ['future-capability']);
      expect(parsed.commandKind, isA<UnknownWireCommandKind>());
      expect(parsed.commandKind.wireValue, 'future-command');
    },
  );
}
