import 'radarr_models.dart';

/// One dated release of a movie on the Radarr calendar.
///
/// A movie carries up to three release dates (cinema, digital, physical); the
/// calendar shows one row per date inside the fetched window, each labelled
/// with its release type so a date is never shown without what kind of release
/// it is.
class RadarrCalendarRelease {
  final RadarrMovie movie;

  /// Calendar date of the release, at midnight. Built from the arr date's own
  /// components — release dates are calendar dates, so converting time zones
  /// would shift a midnight-UTC date into the prior day.
  final DateTime date;

  /// 'In cinemas', 'Digital' or 'Physical'.
  final String label;

  const RadarrCalendarRelease({
    required this.movie,
    required this.date,
    required this.label,
  });
}

/// Expands calendar [movies] into labelled per-date rows within [start]..[end],
/// date ascending. A movie none of whose dates lands in the window (time-zone
/// edges, clock skew) still yields one row — its soonest upcoming date relative
/// to [now], else its most recent past one — so nothing Radarr returned is
/// silently dropped.
List<RadarrCalendarRelease> radarrCalendarReleases(
  Iterable<RadarrMovie> movies, {
  required DateTime start,
  required DateTime end,
  DateTime? now,
}) {
  DateTime dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);
  final startDay = dateOnly(start);
  final endDay = dateOnly(end);
  final today = dateOnly(now ?? DateTime.now());

  final rows = <RadarrCalendarRelease>[];
  for (final movie in movies) {
    final candidates = <RadarrCalendarRelease>[
      if (movie.inCinemas != null)
        RadarrCalendarRelease(
            movie: movie,
            date: dateOnly(movie.inCinemas!),
            label: 'In cinemas'),
      if (movie.digitalRelease != null)
        RadarrCalendarRelease(
            movie: movie,
            date: dateOnly(movie.digitalRelease!),
            label: 'Digital'),
      if (movie.physicalRelease != null)
        RadarrCalendarRelease(
            movie: movie,
            date: dateOnly(movie.physicalRelease!),
            label: 'Physical'),
    ];
    if (candidates.isEmpty) continue;

    final inWindow = candidates
        .where((c) => !c.date.isBefore(startDay) && !c.date.isAfter(endDay))
        .toList();
    if (inWindow.isNotEmpty) {
      rows.addAll(inWindow);
      continue;
    }
    candidates.sort((a, b) => a.date.compareTo(b.date));
    final upcoming = candidates.where((c) => !c.date.isBefore(today)).toList();
    rows.add(upcoming.isNotEmpty ? upcoming.first : candidates.last);
  }
  rows.sort((a, b) => a.date.compareTo(b.date));
  return rows;
}
