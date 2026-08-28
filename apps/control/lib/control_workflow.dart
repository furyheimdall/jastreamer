import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_theme.dart';

part 'control_workflow_catalog.dart';
part 'control_workflow_transport.dart';
part 'control_workflow_queue.dart';
part 'control_workflow_feedback.dart';
part 'control_workflow_shared.dart';

final class ControlWorkflow extends StatefulWidget {
  const ControlWorkflow({
    required this.catalog,
    required this.inventory,
    required this.playback,
    required this.preview,
    required this.decision,
    required this.onSearch,
    required this.onSelectZone,
    required this.onAssignRenderer,
    required this.onQueue,
    required this.onTransport,
    required this.onRecover,
    this.failure,
    this.syncNotice,
    this.syncing = false,
    super.key,
  });

  final CatalogPage catalog;
  final ZonesSnapshot inventory;
  final PlaybackState playback;
  final PreviewView preview;
  final DecisionView decision;
  final ControlFailure? failure;
  final String? syncNotice;
  final bool syncing;
  final Future<void> Function(String query) onSearch;
  final Future<void> Function(ZoneId zoneId) onSelectZone;
  final Future<void> Function(RendererId? rendererId) onAssignRenderer;
  final Future<void> Function(QueueMutationIntent intent) onQueue;
  final Future<void> Function(TransportMutationIntent intent) onTransport;
  final Future<void> Function() onRecover;

  @override
  State<ControlWorkflow> createState() => _ControlWorkflowState();
}

final class _ControlWorkflowState extends State<ControlWorkflow> {
  final search = TextEditingController();
  final seekSeconds = TextEditingController(text: '0');
  bool submitting = false;

  Future<void> run(Future<void> Function() action) async {
    if (submitting) return;
    setState(() => submitting = true);
    try {
      await action();
    } finally {
      if (mounted) setState(() => submitting = false);
    }
  }

  @override
  void dispose() {
    search.dispose();
    seekSeconds.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
        builder: (context, constraints) {
          final mobile = constraints.maxWidth < 760;
          final catalog = _CatalogPanel(
            page: widget.catalog,
            search: search,
            disabled: submitting,
            submitSearch: () => run(() => widget.onSearch(search.text.trim())),
            add: (track) => run(
              () => widget
                  .onQueue(QueueMutationIntent.append(trackIds: [track.id])),
            ),
          );
          final playback = Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _ZoneTransportPanel(
                inventory: widget.inventory,
                playback: widget.playback,
                disabled: submitting,
                seekSeconds: seekSeconds,
                selectZone: (zone) => run(() => widget.onSelectZone(zone)),
                assign: (renderer) =>
                    run(() => widget.onAssignRenderer(renderer)),
                transport: (intent) => run(() => widget.onTransport(intent)),
              ),
              const SizedBox(height: 16),
              _ExplicitQueuePanel(
                playback: widget.playback,
                tracks: widget.catalog.tracks,
                disabled: submitting,
                mutate: (intent) => run(() => widget.onQueue(intent)),
              ),
              const SizedBox(height: 16),
              _AutomaticPreviewPanel(
                preview: widget.preview,
                decision: widget.decision,
              ),
            ],
          );
          final content = mobile
              ? Column(
                  key: const Key('workflow-mobile'),
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [catalog, const SizedBox(height: 16), playback],
                )
              : Row(
                  key: const Key('workflow-desktop'),
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(flex: 4, child: catalog),
                    const SizedBox(width: 16),
                    Expanded(flex: 6, child: playback),
                  ],
                );
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (widget.syncing || submitting)
                const LinearProgressIndicator(
                  semanticsLabel: 'Synchronizing with Server',
                ),
              if (widget.syncNotice case final notice?) ...[
                _SyncBanner(notice: notice),
                const SizedBox(height: 16),
              ],
              if (widget.failure case final failure?) ...[
                _FailureBanner(
                  failure: failure,
                  disabled: submitting,
                  recover: () => run(widget.onRecover),
                ),
                const SizedBox(height: 16),
              ],
              content,
            ],
          );
        },
      );
}
