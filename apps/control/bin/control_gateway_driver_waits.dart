part of 'control_gateway_driver.dart';

Future<void> _waitCatalog(HttpControlGateway gateway) async {
  final initial = await gateway.catalog();
  final events = await gateway.subscribe(watchedZones: const {});
  stdout.writeln(
    jsonEncode({'ready': 'catalog', 'revision': initial.revision}),
  );
  try {
    final update = await events.updates
        .firstWhere(
          (update) =>
              update.resource == ResourceKind.catalog &&
              update.value is CatalogView &&
              (update.value as CatalogView).revision > initial.revision,
        )
        .timeout(const Duration(seconds: 30));
    final catalog = update.value as CatalogView;
    final page = await gateway.browseCatalog(limit: 10);
    if (catalog.revision <= 0 ||
        catalog.trackCount <= 0 ||
        page.tracks.isEmpty) {
      throw StateError('Completed production scan did not publish tracks.');
    }
    stdout.writeln(
      jsonEncode({
        'scenario': 'catalog-scan',
        'catalog_revision': catalog.revision,
        'track_count': catalog.trackCount,
        'browse_count': page.tracks.length,
        'subscribed_before_scan': true,
      }),
    );
  } finally {
    await events.close();
  }
}

Future<void> _waitRenderer(HttpControlGateway gateway) async {
  final rendererId = RendererId(_requiredEnvironment('JASTREAMER_RENDERER_ID'));
  final expectedStatus =
      Platform.environment['JASTREAMER_RENDERER_STATUS'] ?? 'connected';
  final events = await gateway.subscribe();
  stdout.writeln(
    jsonEncode({'ready': 'renderer', 'renderer_id': rendererId.value}),
  );
  try {
    late final ZonesSnapshot inventory;
    if (Platform.environment['JASTREAMER_RENDERER_SIGNAL'] == 'stdin') {
      await stdin
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .first
          .timeout(const Duration(seconds: 90));
      inventory = await gateway.zones();
    } else {
      final update = await events.updates
          .firstWhere((update) {
            if (update.resource != ResourceKind.zones ||
                update.value is! ZonesSnapshot) {
              return false;
            }
            return (update.value as ZonesSnapshot).renderers.any(
              (renderer) =>
                  renderer.id == rendererId &&
                  renderer.status.wireValue == expectedStatus,
            );
          })
          .timeout(const Duration(seconds: 30));
      inventory = update.value as ZonesSnapshot;
    }
    final renderer = inventory.renderers.singleWhere(
      (item) => item.id == rendererId,
    );
    if (renderer.status.wireValue != expectedStatus) {
      throw StateError('Renderer did not reach $expectedStatus.');
    }
    stdout.writeln(
      jsonEncode({
        'scenario': 'renderer-connected',
        'renderer_id': renderer.id.value,
        'status': renderer.status.wireValue,
        'subscribed_before_renderer_change': true,
      }),
    );
  } finally {
    await events.close();
  }
}
