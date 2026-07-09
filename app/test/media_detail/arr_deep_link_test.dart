import 'package:cantinarr/features/media_detail/logic/arr_deep_link.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('matchRadarrMovie', () {
    final movies = [
      const RadarrMovie(id: 10, title: 'Alpha', year: 2001, tmdbId: 111),
      const RadarrMovie(id: 20, title: 'Beta', year: 2002, tmdbId: 222),
    ];

    test('returns the movie whose tmdbId matches', () {
      final match = matchRadarrMovie(movies, 222);
      expect(match, isNotNull);
      expect(match!.id, 20);
    });

    test('returns null when no movie has that tmdbId', () {
      expect(matchRadarrMovie(movies, 999), isNull);
    });

    test('returns null for an empty library', () {
      expect(matchRadarrMovie(const [], 111), isNull);
    });
  });

  group('matchSonarrSeries', () {
    final series = [
      const SonarrSeries(id: 1, title: 'The Wire', tvdbId: 79126),
      const SonarrSeries(id: 2, title: 'Breaking Bad', tvdbId: 81189),
      // A library entry Sonarr couldn't stamp with a TVDB id.
      const SonarrSeries(id: 3, title: 'Homegrown Show'),
    ];

    test('matches on tvdbId when the discovery side supplies one', () {
      final match = matchSonarrSeries(series, tvdbId: 81189, title: 'Breaking Bad');
      expect(match, isNotNull);
      expect(match!.id, 2);
    });

    test('a known tvdbId with no hit is "not in library" (no title fallback)',
        () {
      // tvdbId is authoritative here: even though the title matches id 1, an
      // unknown tvdbId must not link to a different show.
      final match = matchSonarrSeries(series, tvdbId: 55555, title: 'The Wire');
      expect(match, isNull);
    });

    test('falls back to a case-insensitive title match when tvdbId is null', () {
      final match =
          matchSonarrSeries(series, tvdbId: null, title: 'homegrown show');
      expect(match, isNotNull);
      expect(match!.id, 3);
    });

    test('returns null when neither tvdbId nor title matches', () {
      final match =
          matchSonarrSeries(series, tvdbId: null, title: 'Unknown Series');
      expect(match, isNull);
    });

    test('returns null when tvdbId is null and title is empty', () {
      expect(matchSonarrSeries(series, tvdbId: null, title: ''), isNull);
    });
  });

  group('ArrDeepLink', () {
    test('labels a movie link as Radarr', () {
      const link = ArrDeepLink(
        instanceId: 'r1',
        movie: RadarrMovie(id: 1, title: 'A', year: 2000, tmdbId: 1),
      );
      expect(link.moduleLabel, 'Radarr');
    });

    test('labels a series link as Sonarr', () {
      const link = ArrDeepLink(
        instanceId: 's1',
        series: SonarrSeries(id: 1, title: 'B', tvdbId: 1),
      );
      expect(link.moduleLabel, 'Sonarr');
    });
  });
}
