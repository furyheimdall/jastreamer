import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_wire.dart';

ZonesSnapshot parseZonesSnapshot(Object? value) {
  final body = _object(value, 'zones snapshot');
  _requireExactKeys(body, const {'zones', 'renderers'}, 'zones snapshot');
  final zones =
      requiredObjects(body, 'zones').map(_parseZone).toList(growable: false);
  final renderers = requiredObjects(body, 'renderers')
      .map(_parseRenderer)
      .toList(growable: false);
  _requireUnambiguousInventory(zones, renderers);
  return ZonesSnapshot(zones: zones, renderers: renderers);
}

ZoneView _parseZone(Map<String, Object?> body) {
  _requireExactKeys(
    body,
    const {'zone_id', 'name', 'revision', 'renderer_id', 'transport'},
    'zone',
  );
  final revision = requiredInteger(body, 'revision');
  if (revision < 0) {
    throw const FormatException('revision must be non-negative');
  }
  return ZoneView(
    id: ZoneId(_nonEmptyString(body, 'zone_id')),
    name: _nonEmptyString(body, 'name'),
    revision: revision,
    rendererId: switch (body['renderer_id']) {
      null => null,
      final String value when value.isNotEmpty => RendererId(value),
      _ => throw const FormatException(
          'renderer_id must be a non-empty string or null',
        ),
    },
    transport: _knownWireValue(
      body,
      'transport',
      LogicalTransport.values,
    ),
  );
}

RendererView _parseRenderer(Map<String, Object?> body) {
  _requireExactKeys(
    body,
    const {
      'renderer_id',
      'name',
      'kind',
      'status',
      'capabilities',
      'last_seen_at',
    },
    'renderer',
  );
  final capabilities = requiredStrings(body, 'capabilities');
  if (capabilities.any((value) => value.isEmpty) ||
      capabilities.toSet().length != capabilities.length) {
    throw const FormatException(
      'capabilities must contain unique non-empty strings',
    );
  }
  final lastSeenAt = _parseGoServerTimestamp(
    requiredString(body, 'last_seen_at'),
  );
  return RendererView(
    id: RendererId(_nonEmptyString(body, 'renderer_id')),
    name: _nonEmptyString(body, 'name'),
    kind: _knownWireValue(body, 'kind', RendererKind.values),
    status: _knownWireValue(body, 'status', RendererStatus.values),
    capabilities: capabilities,
    lastSeenAt: lastSeenAt,
  );
}

void _requireUnambiguousInventory(
  List<ZoneView> zones,
  List<RendererView> renderers,
) {
  final rendererIds = <String>{};
  for (final renderer in renderers) {
    if (!rendererIds.add(renderer.id.value)) {
      throw const FormatException('renderer_id must be unique');
    }
  }
  final zoneIds = <String>{};
  for (final zone in zones) {
    if (!zoneIds.add(zone.id.value)) {
      throw const FormatException('zone_id must be unique');
    }
    final rendererId = zone.rendererId;
    if (rendererId != null && !rendererIds.contains(rendererId.value)) {
      throw const FormatException('renderer_id assignment must resolve');
    }
  }
}

DateTime _parseGoServerTimestamp(String value) {
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|([+-])(\d{2}):(\d{2}))$',
  ).firstMatch(value);
  if (match == null) {
    throw const FormatException('last_seen_at must be a Go RFC3339 time');
  }
  final year = int.parse(_capture(match, 1));
  final month = int.parse(_capture(match, 2));
  final day = int.parse(_capture(match, 3));
  final hour = int.parse(_capture(match, 4));
  final minute = int.parse(_capture(match, 5));
  final second = int.parse(_capture(match, 6));
  final maximumDay = switch (month) {
    2 => _isLeapYear(year) ? 29 : 28,
    4 || 6 || 9 || 11 => 30,
    >= 1 && <= 12 => 31,
    _ => 0,
  };
  final zone = _capture(match, 8);
  final offsetHour = zone == 'Z' ? 0 : int.parse(_capture(match, 10));
  final offsetMinute = zone == 'Z' ? 0 : int.parse(_capture(match, 11));
  final offsetValid = zone == 'Z' ||
      (offsetHour <= 23 &&
          offsetMinute <= 59 &&
          (offsetHour != 0 || offsetMinute != 0));
  if (day < 1 ||
      day > maximumDay ||
      hour > 23 ||
      minute > 59 ||
      second > 59 ||
      !offsetValid) {
    throw const FormatException('last_seen_at must be a Go RFC3339 time');
  }
  return DateTime.parse(value).toUtc();
}

bool _isLeapYear(int year) =>
    year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);

String _capture(RegExpMatch match, int group) {
  final value = match[group];
  if (value == null) {
    throw const FormatException('last_seen_at capture is missing');
  }
  return value;
}

Map<String, Object?> _object(Object? value, String name) {
  if (value is! Map<String, Object?>) {
    throw FormatException('$name must be an object');
  }
  return value;
}

void _requireExactKeys(
  Map<String, Object?> value,
  Set<String> expected,
  String name,
) {
  if (value.keys.toSet().difference(expected).isNotEmpty ||
      expected.difference(value.keys.toSet()).isNotEmpty) {
    throw FormatException('$name fields do not match protocol major 3');
  }
}

String _nonEmptyString(Map<String, Object?> body, String key) {
  final value = requiredString(body, key);
  if (value.isEmpty) {
    throw FormatException('$key must not be empty');
  }
  return value;
}

WireValue<T> _knownWireValue<T extends Enum>(
  Map<String, Object?> body,
  String key,
  List<T> values,
) {
  final parsed = parseWireValue(requiredString(body, key), values);
  if (!parsed.isKnown) {
    throw FormatException('$key is not supported');
  }
  return parsed;
}
