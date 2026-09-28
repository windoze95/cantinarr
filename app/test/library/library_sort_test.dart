import 'package:cantinarr/core/models/library_sort.dart';
import 'package:cantinarr/core/utils/library_sort.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/logic/radarr_library_sort.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/logic/sonarr_library_sort.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/logic/chaptarr_library_sort.dart';
import 'package:cantinarr/features/lidarr/data/lidarr_models.dart';
import 'package:cantinarr/features/lidarr/logic/lidarr_library_sort.dart';
import 'package:flutter_test/flutter_test.dart';

typedef Json = Map<String, dynamic>;
typedef Pair = (Json, Json);
Pair values(String key, Object low, Object high) => ({key: low}, {key: high});
Pair count(String key) => values('statistics', {key: 2}, {key: 12});
Pair dates(String key) => values(key, '2026-01-01T01:00:00+02:00', '2026-01-01T00:00:00Z');
Pair release(String key) => values(key,
  {'releaseDate': '2025-01-01'}, {'releaseDate': '2026-01-01'});
Pair profiles(String key) => values(key, 20, 1);

const labels = LibrarySortLabels({
  LibrarySortLookup.qualityProfiles: {20: 'Alpha', 1: 'Zulu'},
  LibrarySortLookup.metadataProfiles: {20: 'Alpha', 1: 'Zulu'},
  LibrarySortLookup.tags: {20: 'Alpha', 1: 'Zulu', 3: 'Beta'},
});

List<int> ordered(String module, List<Json> records, LibrarySortSelection sort) =>
  switch (module) {
    'radarr' => sortRadarrLibrary(records.map(RadarrMovie.fromJson), sort, labels).map((r) => r.id).toList(),
    'sonarr' => sortSonarrLibrary(records.map(SonarrSeries.fromJson), sort, labels).map((r) => r.id).toList(),
    'chaptarr' => sortChaptarrLibrary(records.map(ChaptarrAuthor.fromJson), sort, labels).map((r) => r.id).toList(),
    _ => sortLidarrLibrary(records.map(LidarrArtist.fromJson), sort, labels).map((r) => r.id).toList(),
  };

Json record(int id, Json values, {String? name}) => {
  'id': id, 'title': name ?? (id == 1 ? 'Zulu' : 'Alpha'),
  'authorName': name ?? (id == 1 ? 'Zulu' : 'Alpha'),
  'artistName': name ?? (id == 1 ? 'Zulu' : 'Alpha'), ...values,
};

void main() {
  final common = <LibrarySortField, Pair>{
    LibrarySortField.added: dates('added'),
    LibrarySortField.path: values('path', '/a', '/z'),
    LibrarySortField.monitoredStatus: values('monitored', false, true),
    LibrarySortField.qualityProfile: profiles('qualityProfileId'),
    LibrarySortField.tags: values('tags', [3, 20], [1]),
  };
  final cases = <String, Map<LibrarySortField, Pair>>{
    'radarr': {...common,
      LibrarySortField.alphabetical: values('sortTitle', 'Alpha', 'zulu'),
      LibrarySortField.certification: values('certification', 'PG', 'R'),
      LibrarySortField.digitalRelease: dates('digitalRelease'),
      LibrarySortField.imdbRating: values('ratings', {'imdb': {'value': 2}}, {'imdb': {'value': 9.1}}),
      LibrarySortField.inCinemas: dates('inCinemas'),
      LibrarySortField.minimumAvailability: values('minimumAvailability', 'announced', 'released'),
      LibrarySortField.originalLanguage: values('originalLanguage', {'name': 'English'}, {'name': 'French'}),
      LibrarySortField.originalTitle: values('originalTitle', 'A film', 'Z film'),
      LibrarySortField.physicalRelease: dates('physicalRelease'),
      LibrarySortField.popularity: values('popularity', 2, 12.5),
      LibrarySortField.releaseDate: dates('releaseDate'),
      LibrarySortField.rottenTomatoesRating: values('ratings', {'rottenTomatoes': {'value': 2}}, {'rottenTomatoes': {'value': 90}}),
      LibrarySortField.runtime: values('runtime', 2, 120),
      LibrarySortField.size: values('sizeOnDisk', 2, 120),
      LibrarySortField.studio: values('studio', 'Alpha', 'zulu'),
      LibrarySortField.tmdbRating: values('ratings', {'tmdb': {'value': 2}}, {'tmdb': {'value': 9.1}}),
      LibrarySortField.traktRating: values('ratings', {'trakt': {'value': 2}}, {'trakt': {'value': 90}}),
      LibrarySortField.year: values('year', 1998, 2026),
    },
    'sonarr': {...common,
      LibrarySortField.alphabetical: values('sortTitle', 'Alpha', 'zulu'),
      LibrarySortField.episodeCompletion: values('statistics',
        {'episodeCount': 10, 'episodeFileCount': 2}, {'episodeCount': 5, 'episodeFileCount': 4}),
      LibrarySortField.episodeCount: count('totalEpisodeCount'),
      LibrarySortField.latestSeason: values('seasons',
        [{'seasonNumber': 0}, {'seasonNumber': 2}], [{'seasonNumber': 12}, {'seasonNumber': 3}]),
      LibrarySortField.network: values('network', 'Alpha', 'zulu'),
      LibrarySortField.nextAiring: dates('nextAiring'),
      LibrarySortField.previousAiring: dates('previousAiring'),
      LibrarySortField.originalLanguage: values('originalLanguage', {'name': 'English'}, {'name': 'French'}),
      LibrarySortField.rating: values('ratings', {'value': 2}, {'value': 9.1}),
      LibrarySortField.seasons: count('seasonCount'),
      LibrarySortField.size: count('sizeOnDisk'),
      LibrarySortField.type: values('seriesType', 'daily', 'standard'),
    },
    'chaptarr': {
      LibrarySortField.alphabetical: values('sortName', 'Alpha', 'zulu'),
      LibrarySortField.lastName: values('sortNameLastFirst', 'Alpha, Z', 'Zulu, A'),
      LibrarySortField.audiobookQualityProfile: profiles('audiobookQualityProfileId'),
      LibrarySortField.audiobookMetadataProfile: profiles('audiobookMetadataProfileId'),
      LibrarySortField.ebookQualityProfile: profiles('ebookQualityProfileId'),
      LibrarySortField.ebookMetadataProfile: profiles('ebookMetadataProfileId'),
      LibrarySortField.bookCompletion: values('statistics',
        {'bookCount': 10, 'availableBookCount': 2, 'bookFileCount': 100},
        {'bookCount': 5, 'availableBookCount': 4, 'bookFileCount': 4}),
      LibrarySortField.books: count('bookCount'),
      LibrarySortField.added: dates('added'),
      LibrarySortField.nextBook: release('nextBook'),
      LibrarySortField.lastBook: release('lastBook'),
      LibrarySortField.path: values('path', '/a', '/z'),
      LibrarySortField.size: count('sizeOnDisk'),
      LibrarySortField.monitoredStatus: values('monitored', false, true),
    },
    'lidarr': {...common,
      LibrarySortField.alphabetical: values('sortName', 'Alpha', 'zulu'),
      LibrarySortField.albums: count('albumCount'),
      LibrarySortField.nextAlbum: release('nextAlbum'),
      LibrarySortField.lastAlbum: release('lastAlbum'),
      LibrarySortField.metadataProfile: profiles('metadataProfileId'),
      LibrarySortField.size: count('sizeOnDisk'),
      LibrarySortField.trackCompletion: values('statistics',
        {'trackCount': 10, 'trackFileCount': 2}, {'trackCount': 5, 'trackFileCount': 4}),
      LibrarySortField.trackCount: count('totalTrackCount'),
      LibrarySortField.type: values('artistType', 'Group', 'Person'),
    },
  };

  for (final module in cases.keys) {
    test('$module menu has exactly the implemented fields', () {
      expect(librarySortFields(module).toSet(), cases[module]!.keys.toSet());
    });
    for (final entry in cases[module]!.entries) {
      test('$module ${entry.key.name} reads its own field in both directions', () {
        final records = [record(2, entry.value.$2), record(1, entry.value.$1)];
        expect(ordered(module, records, LibrarySortSelection(field: entry.key)), [1, 2]);
        expect(ordered(module, records, LibrarySortSelection(field: entry.key, ascending: false)), [2, 1]);
        // Every field also retains records and has deterministic ties.
        final tied = [record(2, entry.value.$1, name: 'Same'), record(1, entry.value.$1, name: 'Same')];
        expect(ordered(module, tied, LibrarySortSelection(field: entry.key)), [1, 2]);
      });
    }
    test('$module invalid and missing dates stay last in either direction', () {
      final records = [record(3, {'added': 'invalid'}), record(4, {}),
        record(2, {'added': '2026-01-01'}), record(1, {'added': '2025-01-01'})];
      expect(ordered(module, records, const LibrarySortSelection(field: LibrarySortField.added)), [1, 2, 3, 4]);
      expect(ordered(module, records, const LibrarySortSelection(field: LibrarySortField.added, ascending: false)), [2, 1, 3, 4]);
    });
    test('$module alphabetic fallback and case-insensitive ties retain identities', () {
      final records = [record(3, {}, name: 'beta'), record(2, {}, name: 'ALPHA'), record(1, {}, name: 'Alpha')];
      expect(ordered(module, records, const LibrarySortSelection()), [1, 2, 3]);
    });
  }

  test('sort copies input and puts null, blank and nonfinite values last', () {
    final input = [2, 1, 3, 4];
    final result = sortLibrary(input, const LibrarySortSelection(ascending: false),
      value: (id, _) => switch (id) {1 => 2, 2 => 12, 3 => null, _ => double.nan},
      name: (_) => 'same', id: (id) => id);
    expect(result, [2, 1, 3, 4]);
    expect(input, [2, 1, 3, 4]);
    expect(identical(result, input), isFalse);
    expect(sortLibrary([1, 2], const LibrarySortSelection(), value: (id, _) =>
      id == 1 ? ' ' : 'Alpha', name: (_) => '', id: (id) => id), [2, 1]);
  });
  test('Chaptarr explicit null profiles do not inherit another format or legacy profile', () {
    final legacy = ChaptarrAuthor.fromJson({'qualityProfileId': 20, 'metadataProfileId': 20});
    expect(legacy.audiobookQualityProfileId, 20);
    expect(legacy.ebookMetadataProfileId, 20);
    final author = ChaptarrAuthor.fromJson({'qualityProfileId': 20,
      'metadataProfileId': 20, 'ebookQualityProfileId': null,
      'ebookMetadataProfileId': null, 'audiobookQualityProfileId': 1});
    expect(author.ebookQualityProfileId, isNull);
    expect(author.ebookMetadataProfileId, isNull);
    expect(author.audiobookQualityProfileId, 1);
    expect(author.toJson().containsKey('ebookQualityProfileId'), isFalse);
  });
  test('Chaptarr last names use supplied names and never guess', () {
    final records = [record(1, {}, name: 'Amy Adams'),
      record(2, {'authorNameLastFirst': 'Zulu, Amy'}, name: 'Amy Zulu')];
    expect(ordered('chaptarr', records, const LibrarySortSelection(field: LibrarySortField.lastName)), [2, 1]);
  });
  test('Radarr disk size accepts statistics and movie file variants', () {
    final records = [record(2, {'movieFile': {'id': 1, 'size': 120}}),
      record(1, {'statistics': {'sizeOnDisk': 2}})];
    expect(ordered('radarr', records, const LibrarySortSelection(field: LibrarySortField.size)), [1, 2]);
  });
  test('known zero completion and equal ratios follow native count ordering', () {
    expect(const LibraryCompletion(0, 0).progress, 1);
    expect(const LibraryCompletion(1, 2).compareTo(const LibraryCompletion(2, 4)), lessThan(0));
    expect(const LibraryCompletion(1, 2).compareTo(const LibraryCompletion(3, 4)), lessThan(0));
  });
  test('tag sequence is lexical, preserves prefix order, and treats unknown ids as unknown', () {
    expect(labels.tags([20])!.compareTo(labels.tags([3, 20])!), lessThan(0));
    expect(labels.tags([20, 999]), isNull);
    expect(labels.tags([]), isNull);
  });
}
