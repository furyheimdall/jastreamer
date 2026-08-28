part of 'app.dart';

extension _ControlAppMutations on _ControlHomeState {
  Future<void> searchCatalog(String query) => _workflowGuard(() async {
        final page = await gateway!.browseCatalog(
          query: query.isEmpty ? null : query,
          limit: 100,
        );
        if (mounted) _update(() => catalogPage = page);
      });

  Future<void> selectZone(ZoneId zoneId) => _workflowGuard(() async {
        final active = gateway!;
        final oldSession = liveSession;
        final nextSession = await active.subscribe(watchedZones: {zoneId});
        final nextPlayback = await active.playbackState(zoneId);
        final nextPreview = await active.preview(zoneId);
        final nextDecision = await active.explanation(zoneId);
        await oldSession?.close();
        if (!mounted) {
          await nextSession.close();
          return;
        }
        _update(() {
          selectedZone = zoneId;
          liveSession = nextSession;
          playback = nextPlayback;
          preview = nextPreview;
          decision = nextDecision;
        });
        await _listenToLiveUpdates(nextSession);
      });

  Future<void> assignRenderer(RendererId? rendererId) =>
      _workflowGuard(() async {
        final active = gateway!;
        final session = liveSession!;
        final current = inventory!.zones.firstWhere(
          (zone) => zone.id == selectedZone,
        );
        final confirmation = _LiveConfirmation<ZonesSnapshot>(
          session.updates,
          (update) => update.resource == ResourceKind.zones
              ? update.value as ZonesSnapshot
              : null,
        );
        try {
          final result = await active.assignRenderer(
            zoneId: selectedZone,
            expectedRevision: current.revision,
            idempotencyKey: _idempotencyKey('renderer'),
            intent: RendererAssignmentIntent(rendererId),
            subscription: session,
          );
          await confirmation.waitUntil(
            (snapshot) => snapshot.zones.any(
              (zone) =>
                  zone.id == selectedZone &&
                  zone.rendererId == rendererId &&
                  zone.revision >= result.revision,
            ),
          );
        } finally {
          await confirmation.close();
        }
      });

  Future<void> mutateQueue(QueueMutationIntent intent) =>
      _workflowGuard(() async {
        final active = gateway!;
        final session = liveSession!;
        final confirmation = _playbackConfirmation(session);
        try {
          final result = await active.mutateQueue(
            zoneId: selectedZone,
            expectedRevision: playback!.revision,
            idempotencyKey: _idempotencyKey('queue-${intent.command.name}'),
            intent: intent,
            subscription: session,
          );
          final confirmed = await confirmation.waitUntil(
            (value) => value.revision >= result.revision,
          );
          if (mounted) _update(() => playback = confirmed);
        } finally {
          await confirmation.close();
        }
      });

  Future<void> mutateTransport(TransportMutationIntent intent) =>
      _workflowGuard(() async {
        final active = gateway!;
        final session = liveSession!;
        final confirmation = _playbackConfirmation(session);
        try {
          final result = await active.mutateTransport(
            zoneId: selectedZone,
            expectedRevision: playback!.revision,
            idempotencyKey: _idempotencyKey('transport-${intent.command.name}'),
            intent: intent,
            subscription: session,
          );
          final confirmed = await confirmation.waitUntil(
            (value) =>
                value.revision >= result.revision &&
                value.pendingCommandId != result.commandId,
          );
          if (mounted) _update(() => playback = confirmed);
        } finally {
          await confirmation.close();
        }
      });

  _LiveConfirmation<PlaybackState> _playbackConfirmation(
    ControlLiveSession session,
  ) =>
      _LiveConfirmation<PlaybackState>(session.updates, (update) {
        if (update.resource != ResourceKind.queue &&
            update.resource != ResourceKind.transport) {
          return null;
        }
        final value = update.value as PlaybackState;
        return value.zoneId == selectedZone ? value : null;
      });

  Future<void> recoverWorkflow() async {
    if (workflowFailure is TokenRevokedFailure) {
      await _forgetCredential();
      if (!mounted) return;
      _update(() {
        catalogPage = null;
        inventory = null;
        playback = null;
        workflowFailure = null;
      });
      return;
    }
    await refreshServerViews();
  }

  Future<void> _workflowGuard(Future<void> Function() action) async {
    if (busy) return;
    _update(() {
      busy = true;
      workflowFailure = null;
    });
    try {
      await action();
    } on Object catch (failure) {
      if (mounted) {
        _update(() => workflowFailure = _asControlFailure(failure));
      }
    } finally {
      if (mounted) _update(() => busy = false);
    }
  }

  ControlFailure _asControlFailure(Object failure) => failure is ControlFailure
      ? failure
      : ControlApiFailure(
          status: null,
          code: 'CONTROL_FAILURE',
          message: failure.toString(),
          recoverable: true,
        );
}
