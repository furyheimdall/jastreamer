import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_theme.dart';
import 'package:jastreamer_control/status_panels.dart';

void main() {
  testWidgets(
      'Given analysis coverage Then progress uses subdued track and amber value',
      (tester) async {
    await tester.pumpWidget(const MaterialApp(
        home: CoveragePanel(
      catalog: CatalogView(
          revision: 1,
          trackCount: 10,
          complete: 6,
          queued: 4,
          failed: 0,
          coverage: 60),
    )));

    final indicators = tester.widgetList<LinearProgressIndicator>(
        find.byType(LinearProgressIndicator));
    expect(
        indicators.every((indicator) =>
            indicator.backgroundColor == ControlColors.progressTrack),
        isTrue);
    expect(
        indicators.every((indicator) =>
            indicator.valueColor?.value == ControlColors.accentPrimary),
        isTrue);
  });

  testWidgets('Given coverage update Then one composite live region is exposed',
      (tester) async {
    final semantics = tester.ensureSemantics();
    await tester.pumpWidget(const MaterialApp(
        home: CoveragePanel(
      catalog: CatalogView(
          revision: 1,
          trackCount: 10,
          complete: 6,
          queued: 4,
          failed: 0,
          coverage: 60),
    )));

    final metric = find.bySemanticsLabel(
        'Signal analysis, 60 percent, 6 complete · 4 queued · 0 failed');
    expect(metric, findsOneWidget);
    expect(
        tester
            .getSemantics(metric)
            .getSemanticsData()
            .flagsCollection
            .isLiveRegion,
        isTrue);
    semantics.dispose();
  });

  testWidgets(
      'Given blocked explicit head Then automatic fallback is forbidden',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: QueuePreviewPanel(
      queue: const QueueView(
          revision: 2,
          entries: [QueueEntryView(trackId: 'missing', state: 'blocked')]),
      preview: PreviewView(
        active: false,
        replaceable: true,
        committed: false,
        decision: _decision(DecisionReason.blockExplicit),
      ),
    )));

    expect(find.text('Unavailable explicit head'), findsOneWidget);
    expect(
        find.text('No automatic fallback while the explicit head is blocked.'),
        findsOneWidget);
    expect(find.text('Revocable preview'), findsOneWidget);
  });

  testWidgets(
      'Given committed no-signal preview Then both states are unmistakable',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: QueuePreviewPanel(
      queue: const QueueView(revision: 3, entries: []),
      preview: PreviewView(
        active: true,
        replaceable: false,
        committed: true,
        decision: _decision(DecisionReason.stopSimilarNoSignal),
      ),
    )));

    expect(find.text('Committed preview'), findsOneWidget);
    expect(find.text('No signal → Server stops playback.'), findsOneWidget);
  });
}

DecisionView _decision(DecisionReason reason) => DecisionView(
      reason: reason,
      source: '',
      trackId: '',
      signalCoverage: 61,
      catalogRevision: 7,
      policyRevision: 2,
    );
