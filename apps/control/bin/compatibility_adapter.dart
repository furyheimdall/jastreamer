import 'dart:convert';
import 'dart:io';

import 'package:jastreamer_control/protocol_compatibility.dart';

void main(List<String> arguments) {
  try {
    final fixtureIndex = arguments.indexOf('--fixture');
    final majorsIndex = arguments.indexOf('--server-majors');
    if (fixtureIndex >= 0 && fixtureIndex + 1 < arguments.length) {
      final decoded =
          jsonDecode(File(arguments[fixtureIndex + 1]).readAsStringSync());
      if (decoded is! Map<String, Object?>) {
        throw const FormatException('fixture must be an object');
      }
      final fixture = CompatibilityFixture.parse(decoded);
      final serverMajors =
          majorsIndex >= 0 && majorsIndex + 1 < arguments.length
              ? arguments[majorsIndex + 1]
                  .split(',')
                  .map(int.parse)
                  .toList(growable: false)
              : <int>[fixture.protocolMajor];
      final selected = selectProtocolMajor(serverMajors);
      stdout.writeln(jsonEncode(<String, Object>{
        'controlSupportedMajors': controlSupportedProtocolMajors,
        'selectedMajor': selected,
        'unknownCapabilities': fixture.unknownCapabilities,
        'commandKind': <String, String>{
          'state': fixture.commandKind.state,
          'wireValue': fixture.commandKind.wireValue,
        },
      }));
      return;
    }
    if (majorsIndex >= 0 && majorsIndex + 1 < arguments.length) {
      final majors = arguments[majorsIndex + 1]
          .split(',')
          .map(int.parse)
          .toList(growable: false);
      stdout.writeln(selectProtocolMajor(majors));
      return;
    }
    stderr.writeln(
      'usage: compatibility_adapter --fixture <file> | '
      '--server-majors <comma-separated>',
    );
    exitCode = 64;
  } on UnsupportedProtocolMajor catch (failure) {
    stderr.writeln(failure);
    exitCode = 65;
  } on FormatException catch (failure) {
    stderr.writeln('INVALID_FIXTURE: ${failure.message}');
    exitCode = 65;
  } on FileSystemException catch (failure) {
    stderr.writeln('INVALID_FIXTURE: ${failure.message}');
    exitCode = 65;
  }
}
