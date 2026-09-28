import '../../../core/models/library_sort.dart';
import '../../../core/utils/library_sort.dart';
import '../data/sonarr_models.dart';

int? _latestSeason(SonarrSeries series) {
  final seasons = series.seasons.map((s) => s.seasonNumber).where((n) => n > 0);
  return seasons.isEmpty ? null : seasons.reduce((a, b) => a > b ? a : b);
}

List<SonarrSeries> sortSonarrLibrary(Iterable<SonarrSeries> series,
    LibrarySortSelection selection, LibrarySortLabels labels) =>
  sortLibrary(series, selection, id: (s) => s.id,
    name: (s) => librarySortName(s.sortTitle, s.title),
    value: (s, field) => switch (field) {
      LibrarySortField.alphabetical => librarySortName(s.sortTitle, s.title),
      LibrarySortField.added => s.added,
      LibrarySortField.episodeCompletion => s.statistics == null ? null :
          LibraryCompletion(s.statistics!.episodeFileCount, s.statistics!.episodeCount),
      LibrarySortField.episodeCount => s.statistics?.totalEpisodeCount,
      LibrarySortField.latestSeason => _latestSeason(s),
      LibrarySortField.monitoredStatus => (s.monitored ? 2 : 0) +
          (s.status == 'continuing' ? 1 : 0),
      LibrarySortField.network => s.network,
      LibrarySortField.nextAiring => s.nextAiring,
      LibrarySortField.originalLanguage => s.originalLanguage,
      LibrarySortField.path => s.path,
      LibrarySortField.previousAiring => s.previousAiring,
      LibrarySortField.qualityProfile => labels.name(
          LibrarySortLookup.qualityProfiles, s.qualityProfileId),
      LibrarySortField.rating => s.rating,
      LibrarySortField.seasons => s.statistics?.seasonCount,
      LibrarySortField.size => s.statistics?.sizeOnDisk,
      LibrarySortField.tags => labels.tags(s.tags),
      LibrarySortField.type => s.seriesType,
      _ => throw ArgumentError.value(field, 'field', 'Not a Sonarr sort'),
    });
