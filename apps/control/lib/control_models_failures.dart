part of 'control_models.dart';

class ControlFailure implements Exception {
  const ControlFailure({
    required this.status,
    required this.code,
    required this.message,
    required this.recoverable,
    this.details = const {},
    this.intent,
  });
  final int? status;
  final String code;
  final String message;
  final bool recoverable;
  final Map<String, Object?> details;
  final MutationIntent? intent;
  @override
  String toString() => '$code${status == null ? '' : ' ($status)'}: $message';
}

final class TokenRevokedFailure extends ControlFailure {
  const TokenRevokedFailure({required super.message})
      : super(status: 401, code: 'TOKEN_REVOKED', recoverable: true);
}

final class ServerOfflineFailure extends ControlFailure {
  const ServerOfflineFailure({required super.message, super.intent})
      : super(status: null, code: 'SERVER_OFFLINE', recoverable: true);
}

final class CertificateIdentityChangedFailure extends ControlFailure {
  const CertificateIdentityChangedFailure({required super.message})
      : super(status: null, code: 'CERTIFICATE_MISMATCH', recoverable: true);
}

final class StaleRevisionFailure extends ControlFailure {
  const StaleRevisionFailure({
    required super.status,
    required super.message,
    required this.serverRevision,
    required MutationIntent super.intent,
  }) : super(code: 'STALE_REVISION', recoverable: true);
  final int? serverRevision;
}

final class RendererOfflineFailure extends ControlFailure {
  const RendererOfflineFailure({
    required super.message,
    required MutationIntent super.intent,
  }) : super(status: 409, code: 'RENDERER_OFFLINE', recoverable: true);
}

final class SubscriptionRequiredFailure extends ControlFailure {
  const SubscriptionRequiredFailure()
      : super(
          status: null,
          code: 'SUBSCRIPTION_REQUIRED',
          message: 'Subscribe to Server invalidations before mutating state.',
          recoverable: true,
        );
}

final class ResyncLimitFailure extends ControlFailure {
  const ResyncLimitFailure()
      : super(
          status: null,
          code: 'RESYNC_LIMIT_REACHED',
          message: 'The bounded full-resync budget was exhausted.',
          recoverable: true,
        );
}
