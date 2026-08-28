part of 'control_workflow.dart';

final class _AutomaticPreviewPanel extends StatelessWidget {
  const _AutomaticPreviewPanel({required this.preview, required this.decision});
  final PreviewView preview;
  final DecisionView decision;

  @override
  Widget build(BuildContext context) => _Surface(
        title: preview.active
            ? 'Automatic next preview'
            : 'Current Server decision',
        subtitle: preview.active
            ? 'Read-only Server decision; Control never computes or queues next locally.'
            : 'No automatic candidate is active; this is the Server decision currently in effect.',
        child: Semantics(
          liveRegion: true,
          container: true,
          child: preview.active
              ? DecoratedBox(
                  decoration: const BoxDecoration(
                    color: ControlColors.surfacePrimary,
                    border: Border(
                      left: BorderSide(
                        color: ControlColors.accentPrimary,
                        width: 4,
                      ),
                    ),
                  ),
                  child: Padding(
                    padding: const EdgeInsets.all(12),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          preview.committed
                              ? 'Committed by Server'
                              : 'Revocable preview',
                          style: Theme.of(context).textTheme.titleMedium,
                        ),
                        const SizedBox(height: 4),
                        Text(
                          'Candidate: ${preview.decision.trackId.isEmpty ? 'none' : preview.decision.trackId}',
                          key: Key(
                            'automatic-preview-track-${preview.decision.trackId.isEmpty ? 'none' : preview.decision.trackId}',
                          ),
                        ),
                        Text(
                          '${preview.decision.reason.code} · ${preview.decision.reason.explanation}',
                        ),
                        Text(
                          'Catalog revision ${preview.decision.catalogRevision} · policy revision ${preview.decision.policyRevision} · coverage ${preview.decision.signalCoverage}%',
                        ),
                      ],
                    ),
                  ),
                )
              : Column(
                  key: const Key('automatic-preview-empty'),
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Track: ${decision.trackId.isEmpty ? 'none' : decision.trackId}',
                      key: Key(
                        'current-decision-track-${decision.trackId.isEmpty ? 'none' : decision.trackId}',
                      ),
                    ),
                    Text(
                      '${decision.reason.code} · ${decision.reason.explanation}',
                    ),
                    Text(
                      'Source ${decision.source} · catalog revision ${decision.catalogRevision} · policy revision ${decision.policyRevision} · coverage ${decision.signalCoverage}%',
                    ),
                  ],
                ),
        ),
      );
}

final class _SyncBanner extends StatelessWidget {
  const _SyncBanner({required this.notice});
  final String notice;

  @override
  Widget build(BuildContext context) => Semantics(
        liveRegion: true,
        container: true,
        child: DecoratedBox(
          decoration: const BoxDecoration(
            color: ControlColors.surfaceElevated,
            borderRadius: BorderRadius.all(Radius.circular(12)),
            border: Border.fromBorderSide(
              BorderSide(color: ControlColors.statusWarning),
            ),
          ),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                const Icon(Icons.sync, color: ControlColors.statusWarning),
                const SizedBox(width: 12),
                Expanded(child: Text(notice)),
              ],
            ),
          ),
        ),
      );
}

final class _FailureBanner extends StatelessWidget {
  const _FailureBanner({
    required this.failure,
    required this.disabled,
    required this.recover,
  });
  final ControlFailure failure;
  final bool disabled;
  final VoidCallback recover;

  @override
  Widget build(BuildContext context) => Semantics(
        liveRegion: true,
        container: true,
        child: DecoratedBox(
          decoration: const BoxDecoration(
            color: ControlColors.surfaceDanger,
            borderRadius: BorderRadius.all(Radius.circular(12)),
          ),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: LayoutBuilder(
              builder: (context, constraints) {
                final message = Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      failure.code,
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                    if (failure.message != failure.code &&
                        failure is! TokenRevokedFailure)
                      Text(failure.message),
                    const SizedBox(height: 8),
                    Text(_guidance),
                  ],
                );
                final action = OutlinedButton(
                  onPressed: disabled ? null : recover,
                  child: Text(
                    failure is TokenRevokedFailure
                        ? 'Clear & pair again'
                        : 'Refresh Server truth',
                  ),
                );
                if (constraints.maxWidth < 560) {
                  return Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      const Align(
                        alignment: Alignment.centerLeft,
                        child: Icon(
                          Icons.error_outline,
                          color: ControlColors.statusError,
                        ),
                      ),
                      const SizedBox(height: 8),
                      message,
                      const SizedBox(height: 12),
                      action,
                    ],
                  );
                }
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Icon(
                      Icons.error_outline,
                      color: ControlColors.statusError,
                    ),
                    const SizedBox(width: 12),
                    Expanded(child: message),
                    const SizedBox(width: 8),
                    action,
                  ],
                );
              },
            ),
          ),
        ),
      );

  String get _guidance {
    if (failure is TokenRevokedFailure) {
      return 'This credential is revoked. Clear it and pair again.';
    }
    if (failure is CertificateIdentityChangedFailure) {
      return 'Stop and verify the Server identity before pairing again.';
    }
    if (failure is ServerOfflineFailure || failure is RendererOfflineFailure) {
      return 'No mutation was retried. Reconnect explicitly after checking Server truth.';
    }
    if (failure case final StaleRevisionFailure stale) {
      final intent = switch (stale.intent) {
        QueueMutationIntent value => value.command.name,
        TransportMutationIntent value => value.command.name,
        RendererAssignmentIntent() => 'assign Renderer',
        null => 'unknown mutation',
      };
      return 'Preserved intent: $intent. No mutation was retried. Refresh Server truth before deciding whether to submit it again.';
    }
    return 'No mutation was retried. Refresh Server truth before deciding whether to submit another command.';
  }
}
