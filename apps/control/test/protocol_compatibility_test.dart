import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_policy_state.dart';
import 'package:jastreamer_control/protocol_compatibility.dart';

void main() {
  test(
    'Given v3 and v2 Server majors When selected Then major three wins',
    () {
      const serverMajors = <int>[2, 3];

      final selected = selectProtocolMajor(serverMajors);

      expect(controlSupportedProtocolMajors, [3, 2]);
      expect(selected, 3);
    },
  );

  test(
    'Given only v2 When selected Then v3 capabilities are not claimed',
    () {
      final session = negotiateControlProtocol(const <int>[2]);

      expect(session.selectedMajor, 2);
      expect(session.capabilities, isNot(contains('catalog-browse')));
    },
  );

  test(
    'Given no common major When selected Then upgrade required is typed',
    () {
      expect(
        () => negotiateControlProtocol(const <int>[1]),
        throwsA(
          isA<UnsupportedProtocolMajor>()
              .having((failure) => failure.httpStatus, 'httpStatus', 426),
        ),
      );
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
