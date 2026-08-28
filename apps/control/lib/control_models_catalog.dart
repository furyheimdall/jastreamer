part of 'control_models.dart';

final class CatalogView {
  const CatalogView({
    required this.revision,
    required this.trackCount,
    required this.complete,
    required this.queued,
    required this.failed,
    required this.coverage,
  });
  final int revision;
  final int trackCount;
  final int complete;
  final int queued;
  final int failed;
  final int coverage;
}

final class CatalogPage {
  const CatalogPage({
    required this.revision,
    required this.tracks,
    required this.nextCursor,
  });
  final int revision;
  final List<CatalogTrack> tracks;
  final CatalogCursor? nextCursor;
}

final class CatalogTrack {
  const CatalogTrack({
    required this.id,
    required this.title,
    required this.artists,
    required this.album,
    required this.albumArtist,
    required this.durationMs,
    required this.available,
    required this.representations,
  });
  final TrackId id;
  final String title;
  final List<String> artists;
  final String album;
  final String? albumArtist;
  final int? durationMs;
  final bool available;
  final List<MediaRepresentation> representations;
}

final class MediaRepresentation {
  const MediaRepresentation({
    required this.id,
    required this.kind,
    required this.mimeType,
    required this.codec,
    required this.sampleRateHz,
    required this.channels,
    required this.bitsPerSample,
    required this.seekable,
  });
  final String id;
  final WireValue<MediaRepresentationKind> kind;
  final String mimeType;
  final String? codec;
  final int? sampleRateHz;
  final int? channels;
  final int? bitsPerSample;
  final bool seekable;
}
