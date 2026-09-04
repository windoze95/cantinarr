import '../../lidarr/data/lidarr_models.dart';
import '../../radarr/data/radarr_models.dart';
import '../../sonarr/data/sonarr_models.dart';

/// Whether a [ReleaseEvent] represents a movie (Radarr), a TV episode
/// (Sonarr), or an album (Lidarr).
enum ReleaseMediaType { movie, tv, music }

/// A single dated entry on the dashboard "Releases" timeline — either a movie
/// becoming available (Radarr calendar) or a TV episode airing (Sonarr
/// calendar). Both sources are normalised into this shape so the list and
/// calendar views can render them uniformly.
class ReleaseEvent {
  /// When the title drops. Movie dates are calendar dates (no meaningful time);
  /// episode dates carry the local air time.
  final DateTime date;
  final String title;

  /// Secondary line, e.g. "Digital" for a movie or "S02E07 • The Ep" for an
  /// episode.
  final String? subtitle;
  final String? posterUrl;
  final ReleaseMediaType mediaType;

  /// TMDB id used to open the media detail screen, when known (movie/tv).
  final int? tmdbId;

  /// Album identity for the music detail screen: the MusicBrainz
  /// release-group id plus the Lidarr instance it was read from. Null for
  /// movie/tv events.
  final String? foreignId;
  final String? instanceId;

  /// Whether the file already exists in the library.
  final bool hasFile;

  const ReleaseEvent({
    required this.date,
    required this.title,
    required this.mediaType,
    this.subtitle,
    this.posterUrl,
    this.tmdbId,
    this.foreignId,
    this.instanceId,
    this.hasFile = false,
  });

  /// The release date truncated to midnight, used for grouping and calendar
  /// markers.
  DateTime get day => DateTime(date.year, date.month, date.day);
}

DateTime _dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);

/// Builds a movie [ReleaseEvent] from a Radarr calendar entry, or null when the
/// entry carries no usable release date.
///
/// A movie can have cinema, digital and physical dates; we surface the soonest
/// upcoming one (relative to [now]) so the timeline reflects when it actually
/// becomes available, falling back to the most recent past date.
ReleaseEvent? releaseEventFromRadarr(
  Map<String, dynamic> json, {
  DateTime? now,
}) {
  final movie = RadarrMovie.fromJson(json);
  final candidates = <({DateTime date, String label})>[
    if (movie.inCinemas != null) (date: movie.inCinemas!, label: 'In cinemas'),
    if (movie.digitalRelease != null)
      (date: movie.digitalRelease!, label: 'Digital'),
    if (movie.physicalRelease != null)
      (date: movie.physicalRelease!, label: 'Physical'),
  ]..sort((a, b) => a.date.compareTo(b.date));

  if (candidates.isEmpty) return null;

  final today = _dateOnly(now ?? DateTime.now());
  final upcoming = candidates
      .where((c) => !_dateOnly(c.date).isBefore(today))
      .toList();
  final chosen = upcoming.isNotEmpty ? upcoming.first : candidates.last;

  // Movie dates are calendar dates; read the components directly rather than
  // converting time zones, which would shift a midnight date to the prior day.
  final d = chosen.date;
  return ReleaseEvent(
    date: DateTime(d.year, d.month, d.day),
    title: movie.title,
    subtitle: chosen.label,
    posterUrl: movie.posterUrl,
    mediaType: ReleaseMediaType.movie,
    tmdbId: movie.tmdbId,
    hasFile: movie.hasFile,
  );
}

/// Builds a TV [ReleaseEvent] from a Sonarr calendar entry, joining to
/// [seriesById] for the series title, poster and TMDB id. Returns null when the
/// entry has no air date.
ReleaseEvent? releaseEventFromSonarr(
  Map<String, dynamic> json,
  Map<int, SonarrSeries> seriesById,
) {
  final air = DateTime.tryParse(json['airDateUtc'] as String? ?? '');
  if (air == null) return null;

  final seriesId = json['seriesId'] as int?;
  final series = seriesId != null ? seriesById[seriesId] : null;
  final seriesTitle = series?.title ??
      (json['series'] as Map<String, dynamic>?)?['title'] as String? ??
      'Unknown series';

  final season = json['seasonNumber'] as int? ?? 0;
  final episode = json['episodeNumber'] as int? ?? 0;
  final se = 'S${season.toString().padLeft(2, '0')}'
      'E${episode.toString().padLeft(2, '0')}';
  final episodeTitle = json['title'] as String?;
  final subtitle = (episodeTitle != null && episodeTitle.isNotEmpty)
      ? '$se • $episodeTitle'
      : se;

  return ReleaseEvent(
    // Episodes air at a real instant — show it in the viewer's local time.
    date: air.toLocal(),
    title: seriesTitle,
    subtitle: subtitle,
    posterUrl: series?.posterUrl,
    mediaType: ReleaseMediaType.tv,
    tmdbId: series?.tmdbId,
    hasFile: json['hasFile'] as bool? ?? false,
  );
}

/// Builds an album [ReleaseEvent] from a Lidarr calendar entry, or null when
/// the album carries no release date or no addressable MusicBrainz id.
///
/// Album release dates are calendar dates — read the components directly, the
/// movie convention, so a midnight date never shifts a day. The poster is the
/// external metadata-provider cover only: an arr-relative path must never be
/// handed to this widget tree, which loads plain public URLs.
ReleaseEvent? releaseEventFromLidarr(
  LidarrAlbum album, {
  required String instanceId,
}) {
  final release = album.releaseDate;
  final foreignId = album.foreignAlbumId;
  if (release == null || foreignId == null || foreignId.isEmpty) return null;

  String? externalCover;
  for (final image in album.images) {
    final remote = image.remoteUrl;
    if (remote != null && remote.startsWith('http')) {
      externalCover = remote;
      break;
    }
  }
  externalCover ??= (album.remoteCover?.startsWith('http') ?? false)
      ? album.remoteCover
      : null;

  return ReleaseEvent(
    date: DateTime(release.year, release.month, release.day),
    title: album.title,
    subtitle: album.artistName.isEmpty ? 'Album' : album.artistName,
    posterUrl: externalCover,
    mediaType: ReleaseMediaType.music,
    foreignId: foreignId,
    instanceId: instanceId,
    hasFile: album.hasFiles,
  );
}

/// Groups events by calendar day, returning entries ordered by day ascending
/// with each day's events ordered by time ascending.
List<MapEntry<DateTime, List<ReleaseEvent>>> groupReleasesByDay(
  Iterable<ReleaseEvent> events,
) {
  final byDay = <DateTime, List<ReleaseEvent>>{};
  for (final event in events) {
    byDay.putIfAbsent(event.day, () => []).add(event);
  }
  final entries = byDay.entries.toList()
    ..sort((a, b) => a.key.compareTo(b.key));
  for (final entry in entries) {
    entry.value.sort((a, b) => a.date.compareTo(b.date));
  }
  return entries;
}
