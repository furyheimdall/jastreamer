part of 'app.dart';

extension _ControlAppLive on _ControlHomeState {
  Future<void> refreshServerViews() => _guard(() async {
        final active = gateway;
        if (active == null) {
          throw const FormatException(
              'Pair this Control before refreshing state.');
        }
        final loadedCatalog = await active.catalog();
        final loadedQueue = await active.queue(selectedZone);
        final loadedPreview = await active.preview(selectedZone);
        final loadedDecision = await active.explanation(selectedZone);
        final loadedPage =
            catalogPage == null ? null : await active.browseCatalog(limit: 100);
        final loadedInventory = inventory == null ? null : await active.zones();
        final loadedPlayback =
            playback == null ? null : await active.playbackState(selectedZone);
        if (!mounted) return;
        _update(() {
          catalog = loadedCatalog;
          queue = loadedQueue;
          preview = loadedPreview;
          decision = loadedDecision;
          catalogPage = loadedPage;
          inventory = loadedInventory;
          playback = loadedPlayback;
          workflowFailure = null;
        });
      });

  Future<void> _listenToLiveUpdates(ControlLiveSession session) async {
    final previousUpdates = liveUpdates;
    final previousEvents = liveEvents;
    liveUpdates = null;
    liveEvents = null;
    unawaited(() async {
      try {
        await previousUpdates?.cancel();
        await previousEvents?.cancel();
      } on Object catch (failure) {
        if (mounted) {
          _update(() => workflowFailure = _asControlFailure(failure));
        }
      }
    }());
    liveUpdates = session.updates.listen(
      (update) {
        if (!mounted) return;
        _update(() {
          switch (update.resource) {
            case ResourceKind.catalog:
              catalog = update.value as CatalogView;
            case ResourceKind.zones:
              inventory = update.value as ZonesSnapshot;
            case ResourceKind.queue:
            case ResourceKind.transport:
              final next = update.value as PlaybackState;
              if (next.zoneId == selectedZone) playback = next;
            case ResourceKind.continuationPolicy:
              state = applyPolicyView(state, update.value as PolicyView);
          }
        });
      },
      onError: (Object failure) {
        if (mounted) {
          _update(() => workflowFailure = _asControlFailure(failure));
        }
      },
    );
    var observedFullResyncs = session.fullResyncCount;
    liveEvents = session.events.listen(
      (event) {
        if (session.fullResyncCount > observedFullResyncs) {
          observedFullResyncs = session.fullResyncCount;
          if (mounted) {
            _update(() {
              syncNotice =
                  'Event gap recovered · full Server state resynchronized';
            });
          }
        }
      },
      onError: (Object failure) {
        if (mounted) {
          _update(() => workflowFailure = _asControlFailure(failure));
        }
        unawaited(_recoverEventStream(session));
      },
      onDone: () => unawaited(_recoverEventStream(session)),
    );
  }

  Future<void> _recoverEventStream(ControlLiveSession staleSession) async {
    if (liveSession != staleSession ||
        recoveringEventSession == staleSession ||
        gateway == null) {
      return;
    }
    recoveringEventSession = staleSession;
    if (mounted) {
      _update(() {
        syncNotice = 'Event gap detected · resynchronizing from Server truth';
      });
    }
    try {
      final replacement = await gateway!.subscribe(
        watchedZones: {selectedZone},
      );
      if (!mounted || liveSession != staleSession) {
        await replacement.close();
        return;
      }
      _update(() {
        liveSession = replacement;
        syncNotice = 'Event stream replaced · reloading Server truth';
      });
      await _listenToLiveUpdates(replacement);
      unawaited(() async {
        try {
          await staleSession.close();
        } on Object catch (failure) {
          if (mounted) {
            _update(() => workflowFailure = _asControlFailure(failure));
          }
        }
      }());

      final loadedCatalog = await gateway!.catalog();
      final loadedInventory = await gateway!.zones();
      if (loadedInventory.zones.isEmpty) {
        throw const FormatException('Server has no playback zones.');
      }
      final loadedZone =
          loadedInventory.zones.any((zone) => zone.id == selectedZone)
              ? selectedZone
              : loadedInventory.zones.first.id;
      replacement.watchZones({loadedZone});
      final loadedPlayback = await gateway!.playbackState(loadedZone);
      final loadedPolicy = await gateway!.policy(loadedZone);
      if (!mounted || liveSession != replacement) return;
      _update(() {
        catalog = loadedCatalog;
        inventory = loadedInventory;
        playback = loadedPlayback;
        state = applyPolicyView(state, loadedPolicy);
        selectedZone = loadedZone;
        syncNotice = 'Event gap recovered · full Server state resynchronized';
      });
    } on Object catch (failure) {
      if (mounted) {
        _update(() => workflowFailure = _asControlFailure(failure));
      }
    } finally {
      if (recoveringEventSession == staleSession) {
        recoveringEventSession = null;
      }
    }
  }

  IdempotencyKey _idempotencyKey(String operation) =>
      IdempotencyKey('control-$operation-${++mutationSequence}');
}

final class _LiveConfirmation<T extends Object> {
  _LiveConfirmation(
    Stream<LiveResourceUpdate> updates,
    T? Function(LiveResourceUpdate update) select,
  ) {
    subscription = updates.listen((update) {
      final selected = select(update);
      if (selected == null) return;
      latest = selected;
      final active = waiter;
      if (active != null && !active.isCompleted && predicate!(selected)) {
        active.complete(selected);
      }
    });
  }

  late final StreamSubscription<LiveResourceUpdate> subscription;
  T? latest;
  Completer<T>? waiter;
  bool Function(T value)? predicate;

  Future<T> waitUntil(bool Function(T value) matches) {
    final current = latest;
    if (current != null && matches(current)) return Future.value(current);
    predicate = matches;
    final next = Completer<T>();
    waiter = next;
    return next.future.timeout(
      const Duration(seconds: 10),
      onTimeout: () => throw const ServerOfflineFailure(
        message: 'Server did not confirm the mutation by invalidation.',
      ),
    );
  }

  Future<void> close() => subscription.cancel();
}
