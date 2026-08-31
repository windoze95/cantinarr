import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/search_library_status.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  // The user's Sonarr library: the 2003 "Tremors" series, complete.
  const tremors2003 = SonarrSeries(
    id: 1,
    title: 'Tremors',
    tvdbId: 71262,
    tmdbId: 2664,
    year: 2003,
    monitored: true,
    statistics: SonarrStatistics(
      episodeFileCount: 13,
      episodeCount: 13,
      totalEpisodeCount: 13,
    ),
  );

  MediaItem tv(int id, String title, String? firstAirDate) => MediaItem(
        id: id,
        title: title,
        mediaType: MediaType.tv,
        releaseDate: firstAirDate,
      );

  group('buildSearchLibraryStatus', () {
    test('movie chips match by TMDB id and media type', () {
      const movies = [
        RadarrMovie(id: 1, title: 'Tremors', year: 1990, tmdbId: 9362, hasFile: true),
        RadarrMovie(id: 2, title: 'Wanted', year: 2008, tmdbId: 8909, monitored: true),
        RadarrMovie(id: 3, title: 'Idle', year: 2010, tmdbId: 777, monitored: false),
      ];
      final map = buildSearchLibraryStatus(
        searchResults: const [],
        movies: movies,
        series: const [],
      );
      expect(map[(MediaType.movie, 9362)]?.label, 'Available');
      expect(map[(MediaType.movie, 8909)]?.label, 'Requested');
      expect(map[(MediaType.movie, 777)], isNull);
    });

    test('series chip matches by the Sonarr-stamped tmdbId', () {
      final map = buildSearchLibraryStatus(
        searchResults: [tv(2664, 'Tremors', '2003-03-28')],
        movies: const [],
        series: const [tremors2003],
      );
      expect(map[(MediaType.tv, 2664)]?.label, 'Available');
    });

    test('a same-titled show years apart gets no chip', () {
      // The 2018 reboot pilot is a distinct TMDB record; owning the 2003
      // series must not dress it in an "Available" chip.
      final map = buildSearchLibraryStatus(
        searchResults: [tv(75977, 'Tremors', '2018-03-19')],
        movies: const [],
        series: const [tremors2003],
      );
      expect(map[(MediaType.tv, 75977)], isNull);
    });

    test('title+year fallback covers libraries without tmdbId', () {
      const legacy = SonarrSeries(
        id: 9,
        title: 'Tremors',
        tvdbId: 71262,
        year: 2003,
        monitored: true,
        statistics: SonarrStatistics(
          episodeFileCount: 13,
          episodeCount: 13,
          totalEpisodeCount: 13,
        ),
      );
      final map = buildSearchLibraryStatus(
        searchResults: [
          tv(2664, 'Tremors', '2003-03-28'),
          // ±1 absorbs TMDB-vs-TVDB dating skew on the same show.
          tv(2665, 'tremors', '2004-01-01'),
          tv(75977, 'Tremors', '2018-03-19'),
          tv(2666, 'Tremors', null),
        ],
        movies: const [],
        series: const [legacy],
      );
      expect(map[(MediaType.tv, 2664)]?.label, 'Available');
      expect(map[(MediaType.tv, 2665)]?.label, 'Available');
      expect(map[(MediaType.tv, 75977)], isNull);
      // No year truth on the result: no chip rather than a guess.
      expect(map[(MediaType.tv, 2666)], isNull);
    });

    test('a library movie id never chips a TV result', () {
      const movies = [
        RadarrMovie(id: 1, title: 'Some Film', year: 1999, tmdbId: 550, hasFile: true),
      ];
      final map = buildSearchLibraryStatus(
        searchResults: [tv(550, 'Some Show', '2015-01-01')],
        movies: movies,
        series: const [],
      );
      expect(map[(MediaType.movie, 550)]?.label, 'Available');
      expect(map[(MediaType.tv, 550)], isNull);
    });

    test('partial and requested series states map to requester vocabulary',
        () {
      const partial = SonarrSeries(
        id: 5,
        title: 'Halfway',
        tmdbId: 100,
        year: 2020,
        monitored: true,
        statistics: SonarrStatistics(
          episodeFileCount: 4,
          episodeCount: 8,
          totalEpisodeCount: 8,
        ),
      );
      const empty = SonarrSeries(
        id: 6,
        title: 'Nothing Yet',
        tmdbId: 200,
        year: 2024,
        monitored: true,
        statistics: SonarrStatistics(totalEpisodeCount: 10),
      );
      const abandoned = SonarrSeries(
        id: 7,
        title: 'Abandoned',
        tmdbId: 300,
        year: 2019,
        monitored: false,
        statistics: SonarrStatistics(totalEpisodeCount: 10),
      );
      final map = buildSearchLibraryStatus(
        searchResults: [
          tv(100, 'Halfway', '2020-01-01'),
          tv(200, 'Nothing Yet', '2024-01-01'),
          tv(300, 'Abandoned', '2019-01-01'),
        ],
        movies: const [],
        series: const [partial, empty, abandoned],
      );
      expect(map[(MediaType.tv, 100)]?.label, 'Partial');
      expect(map[(MediaType.tv, 100)]?.episodeSubtitle, '4/8 eps');
      expect(map[(MediaType.tv, 200)]?.label, 'Requested');
      expect(map[(MediaType.tv, 200)]?.episodeSubtitle, isNull);
      // In the library but unmonitored with nothing on disk: to a requester
      // it is simply not available — no chip.
      expect(map[(MediaType.tv, 300)], isNull);
    });

    test('episodeSubtitle prefers totalEpisodeCount over episodeCount', () {
      // Sonarr's "obtainable now" count (episodeCount) undercounts a
      // renewed-but-not-fully-aired season; the subtitle must agree with the
      // badge's own files < total verdict, which reads totalEpisodeCount.
      const renewed = SonarrSeries(
        id: 8,
        title: 'Renewed Show',
        tmdbId: 400,
        year: 2021,
        monitored: true,
        statistics: SonarrStatistics(
          episodeFileCount: 18,
          episodeCount: 20,
          totalEpisodeCount: 24,
        ),
      );
      final map = buildSearchLibraryStatus(
        searchResults: [tv(400, 'Renewed Show', '2021-01-01')],
        movies: const [],
        series: const [renewed],
      );
      expect(map[(MediaType.tv, 400)]?.label, 'Partial');
      expect(map[(MediaType.tv, 400)]?.episodeSubtitle, '18/24 eps');
    });

    test('movie statuses never carry an episodeSubtitle', () {
      const movies = [
        RadarrMovie(
            id: 1, title: 'Owned Film', year: 1999, tmdbId: 550, hasFile: true),
        RadarrMovie(
            id: 2,
            title: 'Wanted Film',
            year: 2008,
            tmdbId: 551,
            monitored: true),
      ];
      final map = buildSearchLibraryStatus(
        searchResults: const [],
        movies: movies,
        series: const [],
      );
      expect(map[(MediaType.movie, 550)]?.label, 'Available');
      expect(map[(MediaType.movie, 550)]?.episodeSubtitle, isNull);
      expect(map[(MediaType.movie, 551)]?.label, 'Requested');
      expect(map[(MediaType.movie, 551)]?.episodeSubtitle, isNull);
    });

    test('a series with files but an unknown episode total gets no subtitle',
        () {
      // files > 0 but total == 0: a real denominator does not exist yet, and
      // printing '3/0 eps' would be nonsense next to the badge.
      const unknownTotal = SonarrSeries(
        id: 9,
        title: 'Unknown Total',
        tmdbId: 500,
        year: 2022,
        monitored: true,
        statistics: SonarrStatistics(episodeFileCount: 3, episodeCount: 0),
      );
      final map = buildSearchLibraryStatus(
        searchResults: [tv(500, 'Unknown Total', '2022-01-01')],
        movies: const [],
        series: const [unknownTotal],
      );
      expect(map[(MediaType.tv, 500)]?.label, 'Partial');
      expect(map[(MediaType.tv, 500)]?.episodeSubtitle, isNull);
    });
  });
}
