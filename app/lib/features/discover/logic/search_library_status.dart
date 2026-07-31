import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../radarr/data/radarr_models.dart';
import '../../sonarr/data/sonarr_models.dart';
import '../data/tmdb_models.dart';

/// Library presence indicator for search results.
class LibraryStatus {
  final String label;
  final Color color;
  const LibraryStatus({required this.label, required this.color});
}

const _available = LibraryStatus(
  label: 'Available',
  color: AppTheme.available,
);
const _partial = LibraryStatus(
  label: 'Partially Available',
  color: AppTheme.requested,
);
const _requested = LibraryStatus(
  label: 'Requested',
  color: AppTheme.requested,
);

/// Availability chips for search results, in the requester's vocabulary
/// (Available / Partially Available / Requested) so they agree with what the
/// detail page will say — never library-manager jargon (Complete / Missing /
/// Unmonitored). A title that's in the library but has nothing on disk and
/// isn't being fetched gets no chip: to a requester it's simply not
/// available yet, same as a title that isn't in the library at all.
///
/// Chips are keyed by (media type, TMDB id) — TMDB movie and TV ids are
/// separate namespaces, so a bare-id map would let a library movie's chip
/// land on an unrelated show that happens to share the number. Matching is
/// by id wherever possible: Radarr stores each movie's TMDB id and Sonarr
/// (v4) stamps series with theirs. Series fall back to title + premiere year
/// only for libraries that predate the tmdbId field. A bare title is never
/// enough — same-named shows are distinct records (the 2003 "Tremors" series
/// and the 2018 reboot pilot, say), and a title-only match dresses the one
/// you can't have in the availability of the one you own.
Map<(MediaType, int), LibraryStatus> buildSearchLibraryStatus({
  required List<MediaItem> searchResults,
  required List<RadarrMovie> movies,
  required List<SonarrSeries> series,
}) {
  final map = <(MediaType, int), LibraryStatus>{};

  // Radarr: match by TMDB ID
  for (final movie in movies) {
    final tmdbId = movie.tmdbId;
    if (tmdbId == null) continue;
    if (movie.hasFile) {
      map[(MediaType.movie, tmdbId)] = _available;
    } else if (movie.monitored) {
      map[(MediaType.movie, tmdbId)] = _requested;
    }
  }

  if (series.isEmpty) return map;

  final byTmdbId = <int, SonarrSeries>{
    for (final s in series)
      if ((s.tmdbId ?? 0) > 0) s.tmdbId!: s,
  };
  final byTitleYear = <(String, int), SonarrSeries>{
    for (final s in series)
      if ((s.year ?? 0) > 0) (s.title.toLowerCase(), s.year!): s,
  };

  for (final item in searchResults) {
    if (item.mediaType != MediaType.tv) continue;
    final match = byTmdbId[item.id] ?? _titleYearMatch(byTitleYear, item);
    if (match == null) continue;
    // episodeTotals, not percentComplete: percentComplete only counts
    // monitored episodes, so a series with one downloaded season and
    // the rest unmonitored would read "Available" while most of it is
    // missing.
    final (:files, :total) = match.episodeTotals;
    if (total > 0 && files >= total) {
      map[(MediaType.tv, item.id)] = _available;
    } else if (files > 0) {
      map[(MediaType.tv, item.id)] = _partial;
    } else if (match.monitored) {
      map[(MediaType.tv, item.id)] = _requested;
    }
  }

  return map;
}

/// Same-titled series whose premiere year is within one of the result's. ±1
/// absorbs TMDB and TVDB dating the same premiere differently without
/// reaching the years-apart gap that means a different show; an unknown year
/// on either side matches nothing rather than guessing.
SonarrSeries? _titleYearMatch(
  Map<(String, int), SonarrSeries> byTitleYear,
  MediaItem item,
) {
  final year = tmdbPremiereYear(item.releaseDate);
  if (year == null) return null;
  final title = item.title.toLowerCase();
  return byTitleYear[(title, year)] ??
      byTitleYear[(title, year - 1)] ??
      byTitleYear[(title, year + 1)];
}
