import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_models.dart';

PolicyView parsePolicy(Map<String, Object?> body) => PolicyView(
      mode: WirePolicy.parse(requiredString(body, 'mode')),
      artistGap: requiredInteger(body, 'artist_gap'),
      albumGap: requiredInteger(body, 'album_gap'),
      sessionOverride: switch (body['session_override']) {
        final String value when value.isNotEmpty => WirePolicy.parse(value),
        _ => null,
      },
      revision: requiredInteger(body, 'revision'),
    );

DecisionView parseDecision(Map<String, Object?> body) => DecisionView(
      reason: DecisionReason.parse(requiredString(body, 'reason')),
      source: body['source'] is String ? requiredString(body, 'source') : '',
      trackId:
          body['track_id'] is String ? requiredString(body, 'track_id') : '',
      signalCoverage: requiredInteger(body, 'signal_coverage'),
      catalogRevision: requiredInteger(body, 'catalog_revision'),
      policyRevision: requiredInteger(body, 'policy_revision'),
    );

String requiredString(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! String) throw FormatException('$key must be a string');
  return field;
}

int requiredInteger(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! int) throw FormatException('$key must be an integer');
  return field;
}

bool requiredBoolean(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! bool) throw FormatException('$key must be a boolean');
  return field;
}

List<String> requiredStrings(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! List<Object?> || field.any((item) => item is! String)) {
    throw FormatException('$key must be a string array');
  }
  return field.whereType<String>().toList(growable: false);
}

List<int> requiredIntegers(Map<String, Object?> value, String key) {
  final field = value[key];
  if (field is! List<Object?> || field.any((item) => item is! int)) {
    throw FormatException('$key must be an integer array');
  }
  return field.whereType<int>().toList(growable: false);
}
