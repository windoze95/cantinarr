import '../../../core/models/library_sort.dart';
import '../../../core/utils/library_sort.dart';
import '../data/radarr_models.dart';

List<RadarrMovie> sortRadarrLibrary(Iterable<RadarrMovie> movies,
    LibrarySortSelection selection, LibrarySortLabels labels) =>
  sortLibrary(movies, selection, id: (m) => m.id,
    name: (m) => librarySortName(m.sortTitle, m.title),
    value: (m, field) => switch (field) {
      LibrarySortField.alphabetical => librarySortName(m.sortTitle, m.title),
      LibrarySortField.certification => m.certification,
      LibrarySortField.added => m.added,
      LibrarySortField.digitalRelease => m.digitalRelease,
      LibrarySortField.imdbRating => m.imdbRating,
      LibrarySortField.inCinemas => m.inCinemas,
      LibrarySortField.minimumAvailability => m.minimumAvailability,
      LibrarySortField.monitoredStatus => (m.monitored ? 4 : 0) +
          switch (m.status) { 'announced' => 1, 'inCinemas' => 2, 'released' => 3, _ => 0 },
      LibrarySortField.originalLanguage => m.originalLanguage,
      LibrarySortField.originalTitle => m.originalTitle,
      LibrarySortField.path => m.path,
      LibrarySortField.physicalRelease => m.physicalRelease,
      LibrarySortField.popularity => m.popularity,
      LibrarySortField.qualityProfile => labels.name(
          LibrarySortLookup.qualityProfiles, m.qualityProfileId),
      LibrarySortField.releaseDate => m.releaseDate,
      LibrarySortField.rottenTomatoesRating => m.rottenTomatoesRating,
      LibrarySortField.runtime => m.runtime > 0 ? m.runtime : null,
      LibrarySortField.size => m.sizeOnDisk > 0 ? m.sizeOnDisk : (m.movieFile?.size ?? 0),
      LibrarySortField.studio => m.studio,
      LibrarySortField.tags => labels.tags(m.tags),
      LibrarySortField.tmdbRating => m.tmdbRating,
      LibrarySortField.traktRating => m.traktRating,
      LibrarySortField.year => m.year > 0 ? m.year : null,
      _ => throw ArgumentError.value(field, 'field', 'Not a Radarr sort'),
    });
