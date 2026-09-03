part of 'control_gateway_driver.dart';

Future<void> _offline(HttpControlGateway gateway, ZoneId zoneId) async {
  final events = await gateway.subscribe(watchedZones: {zoneId});
  try {
    final initial = await gateway.playbackState(zoneId);
    const intent = TransportMutationIntent.resume();
    var attempts = 0;
    RendererOfflineFailure? failure;
    try {
      attempts++;
      await gateway.mutateTransport(
        zoneId: zoneId,
        expectedRevision: initial.revision,
        idempotencyKey: IdempotencyKey(_newIdempotencyKey()),
        intent: intent,
        subscription: events,
      );
    } on RendererOfflineFailure catch (value) {
      failure = value;
    }
    final after = await gateway.playbackState(zoneId);
    if (failure == null ||
        !identical(failure.intent, intent) ||
        attempts != 1 ||
        after.revision != initial.revision) {
      throw StateError('Offline Renderer failure mutated or retried.');
    }
    stdout.writeln(
      jsonEncode({
        'scenario': 'offline-renderer',
        'code': failure.code,
        'recoverable': failure.recoverable,
        'intent_preserved': identical(failure.intent, intent),
        'attempts': attempts,
        'revision_unchanged': after.revision == initial.revision,
      }),
    );
  } finally {
    await events.close();
  }
}

Future<void> _expectReadFailure<T extends ControlFailure>(
  HttpControlGateway gateway,
  String expectedCode, {
  required int Function() credentialClears,
}) async {
  var attempts = 0;
  ControlFailure? captured;
  try {
    attempts++;
    await gateway.discovery();
  } on ControlFailure catch (failure) {
    captured = failure;
  }
  if (captured is! T ||
      captured.code != expectedCode ||
      attempts != 1 ||
      credentialClears() != 1) {
    throw StateError('Expected $T/$expectedCode once with credential clear.');
  }
  stdout.writeln(
    jsonEncode({
      'scenario': expectedCode.toLowerCase(),
      'code': captured.code,
      'recoverable': captured.recoverable,
      'attempts': attempts,
      'credential_cleared': credentialClears() == 1,
    }),
  );
}

final class _PlaybackUpdates {
  _PlaybackUpdates(ControlLiveSession events) {
    _subscription = events.updates.listen(_accept, onError: _fail);
  }

  late final StreamSubscription<LiveResourceUpdate> _subscription;
  final _buffer = <LiveResourceUpdate>[];
  final _waiters = <_PlaybackWaiter>[];

  Future<PlaybackState> wait(
    ResourceKind resource, {
    required int minimumRevision,
    bool Function(PlaybackState state)? where,
  }) {
    final predicate = where ?? (_) => true;
    for (var index = 0; index < _buffer.length; index++) {
      final update = _buffer[index];
      if (_matches(update, resource, minimumRevision, predicate)) {
        _buffer.removeAt(index);
        return Future.value(update.value as PlaybackState);
      }
    }
    final waiter = _PlaybackWaiter(resource, minimumRevision, predicate);
    _waiters.add(waiter);
    return waiter.completer.future.timeout(const Duration(seconds: 30));
  }

  void _accept(LiveResourceUpdate update) {
    for (var index = 0; index < _waiters.length; index++) {
      final waiter = _waiters[index];
      if (_matches(
        update,
        waiter.resource,
        waiter.minimumRevision,
        waiter.where,
      )) {
        _waiters.removeAt(index);
        waiter.completer.complete(update.value as PlaybackState);
        return;
      }
    }
    _buffer.add(update);
  }

  bool _matches(
    LiveResourceUpdate update,
    ResourceKind resource,
    int minimumRevision,
    bool Function(PlaybackState) predicate,
  ) =>
      update.resource == resource &&
      update.value is PlaybackState &&
      (update.value as PlaybackState).revision >= minimumRevision &&
      predicate(update.value as PlaybackState);

  void _fail(Object error, StackTrace stack) {
    for (final waiter in _waiters) {
      waiter.completer.completeError(error, stack);
    }
    _waiters.clear();
  }

  Future<void> close() => _subscription.cancel();
}

final class _PlaybackWaiter {
  _PlaybackWaiter(this.resource, this.minimumRevision, this.where);
  final ResourceKind resource;
  final int minimumRevision;
  final bool Function(PlaybackState) where;
  final completer = Completer<PlaybackState>();
}
