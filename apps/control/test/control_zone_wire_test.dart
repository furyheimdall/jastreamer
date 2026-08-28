import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_zone_wire.dart';

void main() {
  test('Given Server fixture When parsed Then machine values remain exact', () {
    // Given
    final decoded = jsonDecode(
      File('../../contracts/control-api/v3/fixtures/zones-snapshot.json')
          .readAsStringSync(),
    );

    // When
    final snapshot = parseZonesSnapshot(decoded);

    // Then
    expect(snapshot.zones.first.id, const ZoneId('living'));
    expect(snapshot.zones.first.revision, 1);
    expect(snapshot.zones.first.rendererId, const RendererId('renderer-1'));
    expect(snapshot.zones.first.transport.known, LogicalTransport.idle);
    expect(snapshot.zones.last.rendererId, isNull);
    expect(snapshot.renderers.single.kind.known, RendererKind.custom);
    expect(snapshot.renderers.single.status.known, RendererStatus.connected);
    expect(snapshot.renderers.single.capabilities, [
      'command:pause',
      'command:play',
      'media:audio/flac',
    ]);
    expect(
      snapshot.renderers.single.lastSeenAt,
      DateTime.utc(2026, 8, 26),
    );
  });

  test(
      'Given valid Go timestamps When parsed Then offsets and fractions survive',
      () {
    // Given
    const zone = <String, Object?>{
      'zone_id': 'main',
      'name': 'Main',
      'revision': 0,
      'renderer_id': 'renderer-1',
      'transport': 'idle',
    };
    const timestamps = [
      '2024-02-29T23:59:59.123456789Z',
      '2026-08-26T00:00:00.1+05:30',
      '2026-08-26T00:00:00.000001-07:45',
    ];

    // When
    final parsed = timestamps
        .map(
          (timestamp) => parseZonesSnapshot({
            'zones': [zone],
            'renderers': [
              {
                'renderer_id': 'renderer-1',
                'name': 'Renderer',
                'kind': 'custom',
                'status': 'connected',
                'capabilities': <String>[],
                'last_seen_at': timestamp,
              },
            ],
          }).renderers.single.lastSeenAt,
        )
        .toList(growable: false);

    // Then
    expect(parsed, [
      DateTime.utc(2024, 2, 29, 23, 59, 59, 123, 456),
      DateTime.utc(2026, 8, 25, 18, 30, 0, 100),
      DateTime.utc(2026, 8, 26, 7, 45, 0, 0, 1),
    ]);
  });

  test('Given drifted zone wires When parsed Then every payload fails closed',
      () {
    // Given
    const renderer = <String, Object?>{
      'renderer_id': 'renderer-1',
      'name': 'Renderer',
      'kind': 'custom',
      'status': 'connected',
      'capabilities': ['command:play'],
      'last_seen_at': '2026-08-26T00:00:00Z',
    };
    const zone = <String, Object?>{
      'zone_id': 'main',
      'name': 'Main',
      'revision': 3,
      'renderer_id': 'renderer-1',
      'transport': 'playing',
    };
    final malformed = <Object?>[
      {
        'zones': [zone],
        'renderers': [renderer],
        'extra': true
      },
      {
        'zones': [
          {...zone}..remove('transport'),
        ],
        'renderers': [renderer],
      },
      {
        'zones': [
          {...zone, 'revision': -1},
        ],
        'renderers': [renderer],
      },
      {
        'zones': [
          {...zone, 'renderer_id': 7},
        ],
        'renderers': [renderer],
      },
      {
        'zones': [
          {...zone, 'transport': 'future-logical'},
        ],
        'renderers': [renderer],
      },
      {
        'zones': [zone],
        'renderers': [
          {...renderer, 'kind': 'future-kind'},
        ],
      },
      {
        'zones': [zone],
        'renderers': [
          {...renderer, 'status': 'future-status'},
        ],
      },
      {
        'zones': [zone],
        'renderers': [
          {...renderer, 'last_seen_at': null},
        ],
      },
      {
        'zones': [zone],
        'renderers': [
          {
            ...renderer,
            'capabilities': ['command:play', 'command:play']
          },
        ],
      },
      {
        'zones': [
          {...zone, 'zoneId': 'stale'}..remove('zone_id'),
        ],
        'renderers': [renderer],
      },
      {
        'zones': [
          zone,
          {...zone, 'name': 'Different zone'}
        ],
        'renderers': [renderer],
      },
      {
        'zones': [zone],
        'renderers': [
          renderer,
          {...renderer, 'name': 'Different Renderer'},
        ],
      },
      {
        'zones': [
          {...zone, 'renderer_id': 'renderer-missing'},
        ],
        'renderers': [renderer],
      },
      for (final timestamp in const [
        '2026-02-29T00:00:00Z',
        '2026-13-01T00:00:00Z',
        '2026-04-31T00:00:00Z',
        '2026-08-26T24:00:00Z',
        '2026-08-26T00:60:00Z',
        '2026-08-26T00:00:60Z',
        '2026-08-26T00:00:00+24:00',
        '2026-08-26T00:00:00+05:60',
        '2026-08-26T00:00:00',
        '2026-08-26 00:00:00Z',
        '2026-08-26T00:00:00.1234567890Z',
      ])
        {
          'zones': [zone],
          'renderers': [
            {...renderer, 'last_seen_at': timestamp},
          ],
        },
    ];

    // When
    final actions = malformed
        .map((value) => () => parseZonesSnapshot(value))
        .toList(growable: false);

    // Then
    for (var index = 0; index < malformed.length; index++) {
      expect(
        actions[index],
        throwsFormatException,
        reason: 'drift case $index',
      );
    }
  });
}
