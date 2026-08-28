import 'dart:async';
import 'dart:convert';

import 'package:jastreamer_control/control_models.dart';

final class WireResource {
  const WireResource._(this.wireValue, this.known);
  factory WireResource.parse(String value) => switch (value) {
        'catalog' ||
        'catalog_scan' =>
          WireResource._(value, ResourceKind.catalog),
        'zones' || 'renderers' => WireResource._(value, ResourceKind.zones),
        'queue' => queue,
        'transport' => transport,
        'continuation-policy' => continuationPolicy,
        _ => WireResource._(value, null),
      };

  static const queue = WireResource._('queue', ResourceKind.queue);
  static const transport = WireResource._('transport', ResourceKind.transport);
  static const continuationPolicy = WireResource._(
    'continuation-policy',
    ResourceKind.continuationPolicy,
  );

  final String wireValue;
  final ResourceKind? known;

  @override
  bool operator ==(Object other) =>
      other is WireResource && other.wireValue == wireValue;
  @override
  int get hashCode => wireValue.hashCode;
}

enum ResourceKind { catalog, zones, queue, transport, continuationPolicy }

final class ResourceRevision {
  const ResourceRevision({required this.resource, required this.revision});
  final WireResource resource;
  final int revision;
}

sealed class ControlEvent {
  const ControlEvent({required this.serverEpoch, required this.sequence});
  final String serverEpoch;
  final int sequence;
}

final class ControlSnapshotEvent extends ControlEvent {
  const ControlSnapshotEvent({
    required super.serverEpoch,
    required super.sequence,
    required this.resources,
  });
  final List<ResourceRevision> resources;
}

final class ControlInvalidationEvent extends ControlEvent {
  const ControlInvalidationEvent({
    required super.serverEpoch,
    required super.sequence,
    required this.resource,
    required this.revision,
    this.resourceId,
  });
  final WireResource resource;
  final String? resourceId;
  final int revision;
}

final class ControlResyncRequiredEvent extends ControlEvent {
  const ControlResyncRequiredEvent({
    required super.serverEpoch,
    required super.sequence,
  });
}

final class UnknownControlEvent extends ControlEvent {
  const UnknownControlEvent({
    required super.serverEpoch,
    required super.sequence,
    required this.wireType,
  });
  final String wireType;
}

ControlEvent parseControlEvent(Object? payload) {
  final Object? decoded = payload is String ? jsonDecode(payload) : payload;
  if (decoded is! Map<String, Object?>) {
    throw const FormatException('event must be an object');
  }
  final type = _eventString(decoded, 'type');
  final epoch = decoded['server_epoch']?.toString();
  if (epoch == null || epoch.isEmpty) {
    throw const FormatException('server_epoch must be present');
  }
  final sequence = decoded['sequence'];
  if (sequence is! int || sequence < 0) {
    throw const FormatException('sequence must be a non-negative integer');
  }
  return switch (type) {
    'snapshot' => ControlSnapshotEvent(
        serverEpoch: epoch,
        sequence: sequence,
        resources: _resourceRevisions(decoded['resources']),
      ),
    'invalidation' => ControlInvalidationEvent(
        serverEpoch: epoch,
        sequence: sequence,
        resource: WireResource.parse(_eventString(decoded, 'resource')),
        resourceId: decoded['resource_id'] is String
            ? decoded['resource_id'] as String
            : null,
        revision: _eventInteger(decoded, 'revision'),
      ),
    'resync_required' => ControlResyncRequiredEvent(
        serverEpoch: epoch,
        sequence: sequence,
      ),
    _ => UnknownControlEvent(
        serverEpoch: epoch,
        sequence: sequence,
        wireType: type,
      ),
  };
}

String _eventString(Map<String, Object?> value, String key) {
  final result = value[key];
  if (result is! String || result.isEmpty) {
    throw FormatException('$key must be a non-empty string');
  }
  return result;
}

int _eventInteger(Map<String, Object?> value, String key) {
  final result = value[key];
  if (result is! int || result < 0) {
    throw FormatException('$key must be a non-negative integer');
  }
  return result;
}

List<ResourceRevision> _resourceRevisions(Object? raw) {
  if (raw == null) return const [];
  if (raw is! List) {
    throw const FormatException('resources must be an array');
  }
  return raw.map((item) {
    if (item is! Map<String, Object?>) {
      throw const FormatException('resource revision must be an object');
    }
    return ResourceRevision(
      resource: WireResource.parse(_eventString(item, 'resource')),
      revision: _eventInteger(item, 'revision'),
    );
  }).toList(growable: false);
}

final class EventSyncCoordinator {
  EventSyncCoordinator({
    required Future<void> Function(ControlInvalidationEvent event)
        refetchInvalidated,
    required Future<void> Function() fullResync,
    this.maxFullResyncs = 3,
  })  : _refetchInvalidated = refetchInvalidated,
        _fullResync = fullResync;

  final Future<void> Function(ControlInvalidationEvent event)
      _refetchInvalidated;
  final Future<void> Function() _fullResync;
  final int maxFullResyncs;
  String? _epoch;
  int? _sequence;
  int _fullResyncs = 0;

  int get fullResyncCount => _fullResyncs;

  Future<void> accept(ControlEvent event) async {
    if (event is ControlSnapshotEvent) {
      _epoch = event.serverEpoch;
      _sequence = event.sequence;
      return;
    }
    final hasGap = _epoch == null ||
        _sequence == null ||
        event.serverEpoch != _epoch ||
        event.sequence != _sequence! + 1 ||
        event is ControlResyncRequiredEvent;
    _epoch = event.serverEpoch;
    _sequence = event.sequence;
    if (hasGap) {
      if (_fullResyncs >= maxFullResyncs) {
        throw const ResyncLimitFailure();
      }
      _fullResyncs++;
      await _fullResync();
      return;
    }
    if (event is ControlInvalidationEvent && event.resource.known != null) {
      await _refetchInvalidated(event);
    }
  }
}

abstract interface class EventSocket {
  Stream<Object?> get messages;
  Future<void> close();
}

abstract interface class EventSocketFactory {
  Future<EventSocket> connect({
    required Uri uri,
    required String certificateSha256,
  });
}
