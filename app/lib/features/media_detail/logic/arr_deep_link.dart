import '../../radarr/data/radarr_models.dart';
import '../../sonarr/data/sonarr_models.dart';

/// A resolved deep link from a discovery detail screen into the backing *arr
/// module. Carries the matched library object plus the instance id needed to
/// push the (imperatively-navigated) arr detail screen.
///
/// Exactly one of [movie] / [series] is set — movies deep-link into Radarr, TV
/// into Sonarr. Books are never resolved here: [MediaDetailScreen] only ever
/// shows movies and TV, so Chaptarr is out of scope for this affordance.
class ArrDeepLink {
  final String instanceId;
  final RadarrMovie? movie;
  final SonarrSeries? series;

  const ArrDeepLink({required this.instanceId, this.movie, this.series});

  /// Human label for the owning module, used for "Open in Radarr" / "Open in
  /// Sonarr".
  String get moduleLabel => movie != null ? 'Radarr' : 'Sonarr';
}

/// Finds the Radarr movie in [movies] that corresponds to TMDB id [tmdbId], or
/// null when the title isn't in the Radarr library yet. The TMDB id is Radarr's
/// natural key for a movie, so this is an exact match.
RadarrMovie? matchRadarrMovie(List<RadarrMovie> movies, int tmdbId) {
  for (final m in movies) {
    if (m.tmdbId == tmdbId) return m;
  }
  return null;
}

/// Finds the Sonarr series in [series] that corresponds to the show, or null
/// when it isn't in the Sonarr library yet.
///
/// Matches on the TVDB id (Sonarr's natural key) when the discovery side
/// supplies one. A known [tvdbId] with no hit is treated as "not in the
/// library" rather than falling through to weaker keys, which avoids linking
/// two distinct shows that happen to share a title.
///
/// Without a TVDB id (TMDB has no mapping — unaired pilots, brand-new shows),
/// the [tmdbId] Sonarr v4 stamps on each series is the next-strongest key.
/// Last comes a case-insensitive title match gated on premiere [year] (±1 for
/// TMDB-vs-TVDB dating skew): a bare title is never identity, because
/// same-named shows are distinct records — the 2018 "Tremors" reboot pilot
/// must not deep link into the 2003 "Tremors" series just because the
/// library owns the latter. No year on either side means no match rather
/// than a guess.
SonarrSeries? matchSonarrSeries(
  List<SonarrSeries> series, {
  int? tvdbId,
  int? tmdbId,
  String? title,
  int? year,
}) {
  if (tvdbId != null) {
    for (final s in series) {
      if (s.tvdbId == tvdbId) return s;
    }
    return null;
  }
  if (tmdbId != null) {
    for (final s in series) {
      if (s.tmdbId == tmdbId) return s;
    }
  }
  if (title != null && title.isNotEmpty && year != null) {
    final lower = title.toLowerCase();
    for (final s in series) {
      final seriesYear = s.year ?? 0;
      if (seriesYear > 0 &&
          (seriesYear - year).abs() <= 1 &&
          s.title.toLowerCase() == lower) {
        return s;
      }
    }
  }
  return null;
}
