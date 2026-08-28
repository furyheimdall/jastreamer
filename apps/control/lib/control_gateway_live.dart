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
        _watchedZones = Set.unmodifiable(watchedZones) {
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
  final _events = StreamController<ControlEvent>.broadcast();
  final _updates = StreamController<LiveResourceUpdate>.broadcast();
  Future<void> _eventTail = Future<void>.value();
  bool _active = true;

  Future<void> get ready => _ready.future;
  bool get isActive => _active;
  int get fullResyncCount => _coordinator.fullResyncCount;
  Stream<ControlEvent> get events => _events.stream;
  Stream<LiveResourceUpdate> get updates => _updates.stream;

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
          _events.add(
            ControlResyncRequiredEvent(
              serverEpoch: event.serverEpoch,
              sequence: event.sequence,
            ),
          );
        }
        _events.add(event);
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
    switch (event.resource.known) {
      case ResourceKind.catalog:
        _updates.add(
          LiveResourceUpdate(
            resource: ResourceKind.catalog,
            value: await _gateway.catalog(),
          ),
        );
      case ResourceKind.zones:
        _updates.add(
          LiveResourceUpdate(
            resource: ResourceKind.zones,
            value: await _gateway.zones(),
          ),
        );
      case ResourceKind.queue:
      case ResourceKind.transport:
        for (final zone in _watchedZones) {
          _updates.add(
            LiveResourceUpdate(
              resource: event.resource.known!,
              value: await _gateway.playbackState(zone),
            ),
          );
        }
      case ResourceKind.continuationPolicy:
        for (final zone in _watchedZones) {
          _updates.add(
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
    _updates.add(
      LiveResourceUpdate(
        resource: ResourceKind.catalog,
        value: await _gateway.catalog(),
      ),
    );
    _updates.add(
      LiveResourceUpdate(
        resource: ResourceKind.zones,
        value: await _gateway.zones(),
      ),
    );
    for (final zone in _watchedZones) {
      _updates.add(
        LiveResourceUpdate(
          resource: ResourceKind.queue,
          value: await _gateway.playbackState(zone),
        ),
      );
      _updates.add(
        LiveResourceUpdate(
          resource: ResourceKind.continuationPolicy,
          value: await _gateway.policy(zone),
        ),
      );
    }
  }

  void _fail(Object error, [StackTrace? stack]) {
    if (!_ready.isCompleted) _ready.completeError(error, stack);
    if (!_events.isClosed) _events.addError(error, stack);
  }

  void _done() {
    unawaited(_finishAfterEvents());
  }

  Future<void> _finishAfterEvents() async {
    await _eventTail;
    _active = false;
    _gateway._subscriptions.remove(this);
    if (!_ready.isCompleted) {
      _ready.completeError(
        const ServerOfflineFailure(
          message: 'Event stream closed before its snapshot.',
        ),
      );
    }
  }

  Future<void> close() async {
    if (!_active) return;
    _active = false;
    _gateway._subscriptions.remove(this);
    await _subscription.cancel();
    await _eventTail;
    await _socket.close();
    await _events.close();
    await _updates.close();
  }
}
