sealed class DecisionReason {
  const DecisionReason(this.code);

  static const playExplicit = KnownDecisionReason(
    'PLAY_EXPLICIT',
    'The Server committed the explicit queue head.',
  );
  static const playAlbum = KnownDecisionReason(
    'PLAY_ALBUM',
    'The Server selected the next album track.',
  );
  static const playSimilar = KnownDecisionReason(
    'PLAY_SIMILAR',
    'The Server selected a similar track.',
  );
  static const blockExplicit = KnownDecisionReason(
    'BLOCK_EXPLICIT',
    'The explicit head is unavailable and needs your action.',
  );
  static const stopModeOff = KnownDecisionReason(
    'STOP_MODE_OFF',
    'Automatic continuation is disabled.',
  );
  static const stopNoAlbum = KnownDecisionReason(
    'STOP_NO_ALBUM',
    'No album context is available.',
  );
  static const stopAlbumComplete = KnownDecisionReason(
    'STOP_ALBUM_COMPLETE',
    'The album has no remaining track.',
  );
  static const stopSimilarNoSignal = KnownDecisionReason(
    'STOP_SIMILAR_NO_SIGNAL',
    'No similarity signal is available.',
  );
  static const stopSimilarExhausted = KnownDecisionReason(
    'STOP_SIMILAR_EXHAUSTED',
    'Eligible similar tracks are exhausted.',
  );
  static const stopAutoFailureLimit = KnownDecisionReason(
    'STOP_AUTO_FAILURE_LIMIT',
    'Automatic start failures reached the Server limit.',
  );

  factory DecisionReason.parse(String value) => switch (value) {
        'PLAY_EXPLICIT' => playExplicit,
        'PLAY_ALBUM' => playAlbum,
        'PLAY_SIMILAR' => playSimilar,
        'BLOCK_EXPLICIT' => blockExplicit,
        'STOP_MODE_OFF' => stopModeOff,
        'STOP_NO_ALBUM' => stopNoAlbum,
        'STOP_ALBUM_COMPLETE' => stopAlbumComplete,
        'STOP_SIMILAR_NO_SIGNAL' => stopSimilarNoSignal,
        'STOP_SIMILAR_EXHAUSTED' => stopSimilarExhausted,
        'STOP_AUTO_FAILURE_LIMIT' => stopAutoFailureLimit,
        _ => UnknownDecisionReason(value),
      };

  final String code;
  String get explanation;
  bool get isKnown;
}

final class KnownDecisionReason extends DecisionReason {
  const KnownDecisionReason(super.code, this.explanation);

  @override
  final String explanation;

  @override
  bool get isKnown => true;
}

final class UnknownDecisionReason extends DecisionReason {
  const UnknownDecisionReason(super.code);

  @override
  String get explanation => 'The Server returned an unsupported decision.';

  @override
  bool get isKnown => false;
}
