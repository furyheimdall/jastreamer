part of 'control_gateway.dart';

final class LiveResourceUpdate {
  const LiveResourceUpdate({required this.resource, required this.value});
  final ResourceKind resource;
  final Object value;
}

final class ControlLiveSession {
  ControlLiveSession._({
    required HttpControlGateway gateway,
    required EventSocket socket,
    required Set<ZoneId> watchedZones,
    required int maxFullResyncs,
  })  : _gateway = gateway,
        _socket = socket,
        _watchedZones = {...watchedZones} {
    _events = StreamController<ControlEvent>.broadcast(
      onListen: _flushPendingEvents,
    );
    _updates = StreamController<LiveResourceUpdate>.broadcast(
      onListen: _flushPendingUpdates,
    );
    _coordinator = EventSyncCoordinator(
      refetchInvalidated: _refetchInvalidated,
      fullResync: _fullResync,
      maxFullResyncs: maxFullResyncs,
    );
    _subscription = socket.messages.listen(
      _enqueue,
      onError: _fail,
      onDone: _done,
    );
  }

  final HttpControlGateway _gateway;
  final EventSocket _socket;
  final Set<ZoneId> _watchedZones;
  late final EventSyncCoordinator _coordinator;
  late final StreamSubscription<Object?> _subscription;
  final _ready = Completer<void>();
  late final StreamController<ControlEvent> _events;
  late final StreamController<LiveResourceUpdate> _updates;
  final _pendingEvents = <ControlEvent>[];
  final _pendingEventErrors = <(Object, StackTrace?)>[];
  final _pendingUpdates = <LiveResourceUpdate>[];
  Future<void> _eventTail = Future<void>.value();
  bool _active = true;
  bool _closed = false;

  Future<void> get ready => _ready.future;
  bool get isActive => _active;
  int get fullResyncCount => _coordinator.fullResyncCount;
  String? get serverEpoch => _coordinator.serverEpoch;
  Stream<ControlEvent> get events => _events.stream;
  Stream<LiveResourceUpdate> get updates => _updates.stream;

  void watchZones(Set<ZoneId> zones) {
    _watchedZones
      ..clear()
      ..addAll(zones);
  }

  void _emitEvent(ControlEvent event) {
    if (_events.hasListener) {
      _events.add(event);
    } else {
      _pendingEvents.add(event);
    }
  }

  void _emitUpdate(LiveResourceUpdate update) {
    if (_updates.hasListener) {
      _updates.add(update);
    } else {
      _pendingUpdates.add(update);
    }
  }

  void _flushPendingEvents() {
    for (final event in _pendingEvents) {
      _events.add(event);
    }
    _pendingEvents.clear();
    for (final (error, stack) in _pendingEventErrors) {
      _events.addError(error, stack);
    }
    _pendingEventErrors.clear();
  }

  void _flushPendingUpdates() {
    for (final update in _pendingUpdates) {
      _updates.add(update);
    }
    _pendingUpdates.clear();
  }

  void _enqueue(Object? payload) {
    _eventTail = _eventTail.then((_) => _accept(payload));
  }

  Future<void> _accept(Object? payload) async {
    try {
      final event = parseControlEvent(payload);
      final resyncsBefore = _coordinator.fullResyncCount;
      await _coordinator.accept(event);
      if (!_events.isClosed) {
        if (_coordinator.fullResyncCount > resyncsBefore &&
            event is! ControlResyncRequiredEvent) {
          _emitEvent(
            ControlResyncRequiredEvent(
              serverEpoch: event.serverEpoch,
              sequence: event.sequence,
            ),
          );
        }
        _emitEvent(event);
      }
      if (!_ready.isCompleted) {
        if (event is! ControlSnapshotEvent) {
          throw const FormatException('first event must be a snapshot');
        }
        _ready.complete();
      }
    } catch (error, stack) {
      _fail(error, stack);
    }
  }

  Future<void> _refetchInvalidated(ControlInvalidationEvent event) async {
    final scopedZones = event.resourceId == null
        ? _watchedZones
        : _watchedZones.where((zone) => zone.value == event.resourceId);
    switch (event.resource.known) {
      case ResourceKind.catalog:
        _emitUpdate(
          LiveResourceUpdate(
            resource: ResourceKind.catalog,
            value: await _gateway.catalog(),
          ),
        );
      case ResourceKind.zones:
        _emitUpdate(
          LiveResourceUpdate(
            resource: ResourceKind.zones,
            value: await _gateway.zones(),
          ),
        );
      case ResourceKind.queue:
      case ResourceKind.transport:
        for (final zone in scopedZones) {
          _emitUpdate(
            LiveResourceUpdate(
              resource: event.resource.known!,
              value: await _gateway.playbackState(zone),
            ),
          );
        }
      case ResourceKind.continuationPolicy:
        for (final zone in scopedZones) {
          _emitUpdate(
            LiveResourceUpdate(
              resource: ResourceKind.continuationPolicy,
              value: await _gateway.policy(zone),
            ),
          );
        }
      case null:
        return;
    }
  }

  Future<void> _fullResync() async {
    _emitUpdate(
      LiveResourceUpdate(
        resource: ResourceKind.catalog,
        value: await _gateway.catalog(),
      ),
    );
    _emitUpdate(
      LiveResourceUpdate(
        resource: ResourceKind.zones,
        value: await _gateway.zones(),
      ),
    );
    for (final zone in _watchedZones) {
      _emitUpdate(
        LiveResourceUpdate(
          resource: ResourceKind.queue,
          value: await _gateway.playbackState(zone),
        ),
      );
      _emitUpdate(
        LiveResourceUpdate(
          resource: ResourceKind.continuationPolicy,
          value: await _gateway.policy(zone),
        ),
      );
    }
  }

  void _fail(Object error, [StackTrace? stack]) {
    if (!_ready.isCompleted) _ready.completeError(error, stack);
    if (_events.isClosed) return;
    if (_events.hasListener) {
      _events.addError(error, stack);
    } else {
      _pendingEventErrors.add((error, stack));
    }
  }

  void _done() {
    unawaited(_finishAfterEvents());
  }

  Future<void> _finishAfterEvents() async {
    await _eventTail;
    _active = false;
    if (!_ready.isCompleted) {
      _ready.completeError(
        const ServerOfflineFailure(
          message: 'Event stream closed before its snapshot.',
        ),
      );
    } else {
      _fail(
        const ServerOfflineFailure(
          message: 'Established event stream closed.',
        ),
      );
    }
  }

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _active = false;
    _gateway._subscriptions.remove(this);
    await _subscription.cancel();
    await _eventTail;
    await _socket.close();
    _pendingEvents.clear();
    _pendingEventErrors.clear();
    _pendingUpdates.clear();
    await _events.close();
    await _updates.close();
  }
}
