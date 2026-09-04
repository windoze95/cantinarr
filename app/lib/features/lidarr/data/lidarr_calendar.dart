import 'lidarr_models.dart';

/// One dated album release on the Lidarr calendar.
///
/// An album carries exactly one release date (the MusicBrainz release-group
/// date) — a calendar date with no meaningful time-of-day, so [date] is built
/// from the arr date's own components rather than converted between zones,
/// which would shift a midnight date into the prior day.
class LidarrCalendarRelease {
  final LidarrAlbum album;
  final DateTime date;

  const LidarrCalendarRelease({required this.album, required this.date});
}

/// Maps calendar [albums] into dated rows within [start]..[end], date
/// ascending. An album whose date misses the window (time-zone edges, clock
/// skew) still yields its row — nothing Lidarr returned is silently dropped —
/// and only an album with no release date at all is skipped, since it has no
/// place on a calendar.
List<LidarrCalendarRelease> lidarrCalendarReleases(
  Iterable<LidarrAlbum> albums, {
  required DateTime start,
  required DateTime end,
}) {
  DateTime dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);

  final rows = <LidarrCalendarRelease>[];
  for (final album in albums) {
    final release = album.releaseDate;
    if (release == null) continue;
    rows.add(LidarrCalendarRelease(album: album, date: dateOnly(release)));
  }
  rows.sort((a, b) => a.date.compareTo(b.date));
  return rows;
}
