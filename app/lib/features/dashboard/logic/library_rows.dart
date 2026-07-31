import '../../radarr/data/radarr_models.dart';
import '../../sonarr/data/sonarr_models.dart';

/// The dashboard's "Recently Downloaded" movies, newest file first.
///
/// Recency means when the FILE landed, not when the movie record was created.
/// Radarr's movie-level `added` is set when the movie is added to the library
/// (a request, an import list, a manual add) and is never touched on import or
/// upgrade, so ordering by it buries a title that was requested months ago and
/// only downloaded today underneath the back catalogue — on exactly the day the
/// user wants to see it.
///
/// The fallback to `added` is deliberate: the movie list endpoint sometimes
/// omits movieFile even when hasFile is true, and keying off the file alone
/// would drop those movies out of the row entirely.
List<RadarrMovie> recentlyDownloadedMovies(
  List<RadarrMovie> movies, {
  int limit = 10,
}) {
  DateTime landedAt(RadarrMovie m) =>
      m.movieFile?.dateAdded ?? m.added ?? DateTime(0);

  final downloaded = movies.where((m) => m.hasFile).toList()
    ..sort((a, b) => landedAt(b).compareTo(landedAt(a)));
  return downloaded.take(limit).toList();
}

/// The dashboard's "Recently Downloaded" series, newest import first.
///
/// Sonarr's series record carries no import timestamp — `statistics` counts the
/// files but never says when one landed — so recency has to come from
/// [importHistory], which stamps every completed import with its date. The row
/// is series cards, so a season pack that imported twelve episodes is one
/// arrival of one show, dated by its newest file.
///
/// This replaces a sort by `percentComplete`, which has no time dimension at
/// all: it ranked the *most complete* series, so the row was a near-static list
/// of finished shows that never showed what had actually just downloaded.
///
/// Only records naming an import are counted, so a page that also carries
/// grabs, failures, or deletions cannot pass one off as a download. Series with
/// no import record are absent rather than appended: without a date there is no
/// honest place to put them in a list ordered by date. That also means an empty
/// history — cleared by an admin, or predating the library — yields an empty
/// row, which is the truthful answer to "what arrived lately".
List<SonarrSeries> recentlyDownloadedSeries(
  List<SonarrSeries> series,
  List<SonarrHistoryRecord> importHistory, {
  int limit = 10,
}) {
  final importedAt = <int, DateTime>{};
  for (final record in importHistory) {
    if (record.eventType != SonarrHistoryRecord.importedEventType) continue;
    final seriesId = record.seriesId;
    final date = record.date;
    if (seriesId == null || date == null) continue;
    final newest = importedAt[seriesId];
    if (newest == null || date.isAfter(newest)) importedAt[seriesId] = date;
  }

  DateTime landedAt(SonarrSeries s) => importedAt[s.id] ?? DateTime(0);

  final downloaded = series.where((s) => importedAt.containsKey(s.id)).toList()
    ..sort((a, b) {
      final byDate = landedAt(b).compareTo(landedAt(a));
      // A season pack stamps every episode within the same second; without a
      // tie-break the row would reshuffle on each fetch.
      return byDate != 0 ? byDate : b.id.compareTo(a.id);
    });
  return downloaded.take(limit).toList();
}

/// The dashboard's "Airing Next" series, soonest air date first.
///
/// [calendar] is Sonarr's raw calendar window. Each series is dated by its
/// earliest upcoming episode in that window, because that is the date the row
/// promises. Ordering matters more than it looks: the row shows ten cards, so
/// leaving it in library order (which is alphabetical) let a series airing in
/// six days push out one airing tomorrow.
List<SonarrSeries> airingNextSeries(
  List<SonarrSeries> series,
  List<Map<String, dynamic>> calendar, {
  int limit = 10,
}) {
  final airsAt = <int, DateTime>{};
  for (final entry in calendar) {
    final seriesId = entry['seriesId'] as int?;
    // An entry with no air date cannot be placed in a list ordered by air date.
    final airDate = DateTime.tryParse(entry['airDateUtc'] as String? ?? '');
    if (seriesId == null || airDate == null) continue;
    final soonest = airsAt[seriesId];
    if (soonest == null || airDate.isBefore(soonest)) airsAt[seriesId] = airDate;
  }

  DateTime airsOn(SonarrSeries s) => airsAt[s.id] ?? DateTime(0);

  final airing = series.where((s) => airsAt.containsKey(s.id)).toList()
    ..sort((a, b) {
      final byDate = airsOn(a).compareTo(airsOn(b));
      return byDate != 0 ? byDate : a.id.compareTo(b.id);
    });
  return airing.take(limit).toList();
}
