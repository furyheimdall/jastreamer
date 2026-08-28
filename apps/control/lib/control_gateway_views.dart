part of 'control_gateway.dart';

extension HttpControlGatewayViews on HttpControlGateway {
  Future<PreviewView> preview([ZoneId zoneId = const ZoneId('main')]) async {
    final body = _object(
      await _get('/api/v1/zones/${_segment(zoneId.value)}/automatic-preview'),
    );
    final raw = body['decision'];
    if (raw is! Map<String, Object?>) {
      throw const FormatException('decision must be an object');
    }
    return PreviewView(
      active: requiredBoolean(body, 'active'),
      replaceable: requiredBoolean(body, 'replaceable'),
      committed: requiredBoolean(body, 'committed'),
      decision: parseDecision(raw),
    );
  }

  Future<DecisionView> explanation([
    ZoneId zoneId = const ZoneId('main'),
  ]) async =>
      parseDecision(
        _object(
          await _get(
            '/api/v1/zones/${_segment(zoneId.value)}/decision-explanation',
          ),
        ),
      );
}
