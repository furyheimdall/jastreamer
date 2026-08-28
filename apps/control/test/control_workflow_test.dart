import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_workflow.dart';

void main() {
  testWidgets(
    'explicit queue actions emit Server intents once and never expose preview as a row',
    (tester) async {
      _useTallView(tester);
      final pending = Completer<void>();
      final queueIntents = <QueueMutationIntent>[];
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ControlWorkflow(
              catalog: _catalog,
              inventory: _inventory,
              playback: _playback,
              preview: _preview,
              decision: _decision,
              onSearch: (_) async {},
              onSelectZone: (_) async {},
              onAssignRenderer: (_) async {},
              onQueue: (intent) {
                queueIntents.add(intent);
                return pending.future;
              },
              onTransport: (_) async {},
              onRecover: () async {},
            ),
          ),
        ),
      );

      expect(find.text('Explicit queue'), findsOneWidget);
      expect(find.text('Automatic next preview'), findsOneWidget);
      expect(
          find.byKey(const Key('preview-track-preview-track')), findsNothing);

      final add = find.byKey(const Key('catalog-add-track-a'));
      await tester.tap(add);
      await tester.tap(add);
      await tester.pump();
      expect(queueIntents, hasLength(1));
      expect(queueIntents.single.command, QueueMutationCommand.append);
      expect(queueIntents.single.trackIds, const [TrackId('track-a')]);
      expect(tester.widget<IconButton>(add).onPressed, isNull);

      pending.complete();
      await tester.pump();
    },
  );

  testWidgets(
    'inactive automatic preview keeps the explicit decision in its own surface',
    (tester) async {
      // Given
      _useTallView(tester);
      const explicitDecision = DecisionView(
        reason: DecisionReason.playExplicit,
        source: 'explicit',
        trackId: 'explicit-track',
        signalCoverage: 100,
        catalogRevision: 4,
        policyRevision: 2,
      );
      const inactivePreview = PreviewView(
        active: false,
        replaceable: false,
        committed: false,
        decision: explicitDecision,
      );

      // When
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ControlWorkflow(
              catalog: _catalog,
              inventory: _inventory,
              playback: _playback,
              preview: inactivePreview,
              decision: explicitDecision,
              onSearch: (_) async {},
              onSelectZone: (_) async {},
              onAssignRenderer: (_) async {},
              onQueue: (_) async {},
              onTransport: (_) async {},
              onRecover: () async {},
            ),
          ),
        ),
      );

      // Then
      expect(find.byKey(const Key('automatic-preview-empty')), findsOneWidget);
      expect(
        find.byKey(const Key('automatic-preview-track-explicit-track')),
        findsNothing,
      );
      expect(
        find.byKey(const Key('current-decision-track-explicit-track')),
        findsOneWidget,
      );
    },
  );

  testWidgets('queue reorder remove clear retry and skip stay explicit',
      (tester) async {
    _useTallView(tester);
    final intents = <QueueMutationIntent>[];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ControlWorkflow(
            catalog: _catalog,
            inventory: _inventory,
            playback: _playback,
            preview: _preview,
            decision: _decision,
            onSearch: (_) async {},
            onSelectZone: (_) async {},
            onAssignRenderer: (_) async {},
            onQueue: (intent) async => intents.add(intent),
            onTransport: (_) async {},
            onRecover: () async {},
          ),
        ),
      ),
    );

    await tester.tap(find.byTooltip('Move track-b earlier'));
    await tester.pump();
    await tester.tap(find.byTooltip('Remove First Light'));
    await tester.pump();
    await tester.tap(find.text('Clear'));
    await tester.pump();
    await tester.tap(find.text('Retry blocked track'));
    await tester.pump();
    await tester.tap(find.text('Skip blocked track'));
    await tester.pump();

    expect(intents.map((intent) => intent.command), [
      QueueMutationCommand.move,
      QueueMutationCommand.remove,
      QueueMutationCommand.clear,
      QueueMutationCommand.retryBlocked,
      QueueMutationCommand.skipBlocked,
    ]);
    expect(intents.first.entryId, const QueueEntryId('entry-b'));
    expect(intents.first.beforeEntryId, const QueueEntryId('entry-a'));
  });

  testWidgets(
    'transport exposes logical observed and pending truth and emits exact commands',
    (tester) async {
      _useTallView(tester);
      final commands = <TransportMutationIntent>[];
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ControlWorkflow(
              catalog: _catalog,
              inventory: _inventory,
              playback: _playback,
              preview: _preview,
              decision: _decision,
              onSearch: (_) async {},
              onSelectZone: (_) async {},
              onAssignRenderer: (_) async {},
              onQueue: (_) async {},
              onTransport: (intent) async => commands.add(intent),
              onRecover: () async {},
            ),
          ),
        ),
      );

      expect(find.text('Server intent: paused'), findsOneWidget);
      expect(find.text('Renderer observed: playing'), findsOneWidget);
      expect(find.text('Pending command: command-7'), findsOneWidget);

      for (final key in [
        'transport-previous',
        'transport-play',
        'transport-pause',
        'transport-resume',
      ]) {
        await tester.tap(find.byKey(Key(key)));
        await tester.pump();
      }
      await tester.enterText(find.byKey(const Key('seek-seconds')), '12');
      await tester.tap(find.byKey(const Key('transport-seek')));
      await tester.pump();
      for (final key in ['transport-next', 'transport-stop']) {
        await tester.tap(find.byKey(Key(key)));
        await tester.pump();
      }
      expect(commands.map((value) => value.command), [
        TransportCommand.previous,
        TransportCommand.start,
        TransportCommand.pause,
        TransportCommand.resume,
        TransportCommand.seek,
        TransportCommand.skip,
        TransportCommand.stop,
      ]);
      expect(commands[4].positionMs, 12000);
    },
  );

  testWidgets('recoverable conflict keeps exact message and explicit recovery',
      (tester) async {
    _useTallView(tester);
    var recovered = false;
    const failure = StaleRevisionFailure(
      status: 409,
      message: 'Queue revision 9 no longer matches.',
      serverRevision: 10,
      intent: QueueMutationIntent.clear(),
    );
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ControlWorkflow(
            catalog: _catalog,
            inventory: _inventory,
            playback: _playback,
            preview: _preview,
            decision: _decision,
            failure: failure,
            onSearch: (_) async {},
            onSelectZone: (_) async {},
            onAssignRenderer: (_) async {},
            onQueue: (_) async {},
            onTransport: (_) async {},
            onRecover: () async => recovered = true,
          ),
        ),
      ),
    );

    expect(find.text('STALE_REVISION'), findsOneWidget);
    expect(find.text('Queue revision 9 no longer matches.'), findsOneWidget);
    expect(
      find.textContaining('Preserved intent: clear. No mutation was retried.'),
      findsOneWidget,
    );
    await tester.tap(find.text('Refresh Server truth'));
    await tester.pump();
    expect(recovered, isTrue);
  });

  testWidgets('mobile reflows to one column without overflow', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: ControlWorkflow(
              catalog: _catalog,
              inventory: _inventory,
              playback: _playback,
              preview: _preview,
              decision: _decision,
              onSearch: (_) async {},
              onSelectZone: (_) async {},
              onAssignRenderer: (_) async {},
              onQueue: (_) async {},
              onTransport: (_) async {},
              onRecover: () async {},
            ),
          ),
        ),
      ),
    );
    expect(find.byKey(const Key('workflow-mobile')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

void _useTallView(WidgetTester tester) {
  tester.view.physicalSize = const Size(800, 2000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

const _catalog = CatalogPage(
  revision: 4,
  tracks: [
    CatalogTrack(
      id: TrackId('track-a'),
      title: 'First Light',
      artists: ['Fixture Artist'],
      album: 'Fixture Album',
      albumArtist: null,
      durationMs: 180000,
      available: true,
      representations: [],
    ),
  ],
  nextCursor: null,
);

final _inventory = ZonesSnapshot(
  zones: const [
    ZoneView(
      id: ZoneId('main'),
      name: 'Listening room',
      revision: 7,
      rendererId: RendererId('renderer-a'),
      transport: WireValue('paused', LogicalTransport.paused),
    ),
  ],
  renderers: [
    RendererView(
      id: const RendererId('renderer-a'),
      name: 'Desk Renderer',
      kind: const WireValue('custom', RendererKind.custom),
      status: const WireValue('connected', RendererStatus.connected),
      capabilities: const ['play', 'pause', 'seek'],
      lastSeenAt: DateTime.utc(2026, 8, 26),
    ),
  ],
);

const _playback = PlaybackState(
  zoneId: ZoneId('main'),
  revision: 7,
  etag: EntityTag(7),
  logicalTransport: WireValue('paused', LogicalTransport.paused),
  observedTransport: WireValue('playing', ObservedTransport.playing),
  pendingCommandId: CommandId('command-7'),
  entries: [
    PlaybackQueueEntry(
      id: QueueEntryId('entry-a'),
      trackId: TrackId('track-a'),
      state: WireValue('pending', QueueEntryState.pending),
      position: 0,
      blockedError: null,
    ),
    PlaybackQueueEntry(
      id: QueueEntryId('entry-b'),
      trackId: TrackId('track-b'),
      state: WireValue('blocked', QueueEntryState.blocked),
      position: 1,
      blockedError: ControlFailure(
        status: 409,
        code: 'TRACK_UNAVAILABLE',
        message: 'The source file is unavailable.',
        recoverable: true,
      ),
    ),
  ],
);

const _decision = DecisionView(
  reason: DecisionReason.stopSimilarExhausted,
  source: 'explicit',
  trackId: 'preview-track',
  signalCoverage: 100,
  catalogRevision: 4,
  policyRevision: 2,
);

const _preview = PreviewView(
  active: true,
  replaceable: true,
  committed: false,
  decision: _decision,
);
