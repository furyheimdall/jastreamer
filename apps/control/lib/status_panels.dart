import 'package:flutter/material.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_theme.dart';

part 'status_primitives.dart';

final class CoveragePanel extends StatelessWidget {
  const CoveragePanel({required this.catalog, super.key});
  final CatalogView catalog;

  @override
  Widget build(BuildContext context) => Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Semantics(
                header: true,
                child: Text(
                  'Indexing & analysis',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
              ),
              const SizedBox(height: 12),
              _Metric(
                label: 'Catalog indexed',
                value: catalog.trackCount == 0 ? 0 : 100,
                detail:
                    '${catalog.trackCount} tracks · catalog revision ${catalog.revision}',
              ),
              const SizedBox(height: 12),
              _Metric(
                label: 'Signal analysis',
                value: catalog.coverage,
                detail:
                    '${catalog.complete} complete · ${catalog.queued} queued · ${catalog.failed} failed',
              ),
              const SizedBox(height: 12),
              Text(
                catalog.coverage < 100
                    ? 'Analysis incomplete — the Server may use metadata fallback; Control never calculates a recommendation.'
                    : 'Analysis complete — recommendations still remain exclusively Server-authored.',
              ),
            ],
          ),
        ),
      );
}

final class DecisionPanel extends StatelessWidget {
  const DecisionPanel({required this.decision, super.key});
  final DecisionView decision;

  @override
  Widget build(BuildContext context) => SizedBox(
        width: double.infinity,
        child: Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Semantics(
                  header: true,
                  child: Text(
                    'Persisted decision reason',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                const SizedBox(height: 8),
                Text(
                  decision.reason.code,
                  style: const TextStyle(
                    color: ControlColors.accentPrimary,
                    fontFamily: 'monospace',
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 4),
                Text(decision.reason.explanation),
                const SizedBox(height: 8),
                Text(
                  'Server explanation · catalog revision ${decision.catalogRevision} · policy revision ${decision.policyRevision} · signal coverage ${decision.signalCoverage}%',
                ),
              ],
            ),
          ),
        ),
      );
}

final class QueuePreviewPanel extends StatelessWidget {
  const QueuePreviewPanel({
    required this.queue,
    required this.preview,
    super.key,
  });
  final QueueView queue;
  final PreviewView preview;

  @override
  Widget build(BuildContext context) {
    final blocked = preview.decision.reason == DecisionReason.blockExplicit;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Semantics(
              header: true,
              child: Text(
                'Explicit queue',
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ),
            const SizedBox(height: 4),
            const Text(
              'User-authored order. The Server will not skip an unavailable head.',
            ),
            const SizedBox(height: 12),
            if (blocked) ...[
              Semantics(
                label:
                    'No automatic fallback while the explicit head is blocked.',
                child: const ExcludeSemantics(
                  child: Text(
                    'No automatic fallback while the explicit head is blocked.',
                    style: TextStyle(color: ControlColors.statusError),
                  ),
                ),
              ),
              const SizedBox(height: 8),
            ],
            if (queue.entries.isEmpty)
              const _StateRow(
                icon: Icons.queue_music,
                title: 'Explicit queue empty',
                detail: 'No user-authored tracks are waiting',
              )
            else
              _StateRow(
                icon: blocked ? Icons.block : Icons.queue_music,
                title: blocked ? 'Unavailable explicit head' : 'Explicit head',
                detail:
                    '${queue.entries.first.trackId} · ${preview.decision.reason.code}',
                danger: blocked,
              ),
            const Divider(height: 32),
            Semantics(
              header: true,
              child: Text(
                'Automatic next preview',
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ),
            const SizedBox(height: 4),
            const Text(
              'Server-authored preview. It is never inserted into or presented as the explicit queue.',
            ),
            const SizedBox(height: 12),
            _StateRow(
              icon: preview.committed ? Icons.lock_outline : Icons.auto_awesome,
              title:
                  preview.committed ? 'Committed preview' : 'Revocable preview',
              detail: preview.committed
                  ? 'No longer replaceable · explicit additions remain behind it'
                  : '${preview.replaceable ? 'Replaceable before commit' : 'Awaiting Server state'} · ${preview.decision.reason.code}',
            ),
            const SizedBox(height: 12),
            Text(_fallback(preview.decision)),
          ],
        ),
      ),
    );
  }

  String _fallback(DecisionView decision) => switch (decision.reason) {
        DecisionReason.stopSimilarNoSignal =>
          'No signal → Server stops playback.',
        DecisionReason.stopSimilarExhausted =>
          'Exhaustion → Server stops playback.',
        _ when decision.signalCoverage < 100 =>
          'Analysis incomplete → Server metadata fallback may apply.',
        _ => 'Server signal is complete for this persisted decision.',
      };
}
