import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('BrowseQuery', () {
    test('a full Browse query survives the round trip through its URL', () {
      const query = BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.discover,
        title: 'Action',
        sort: BrowseSort.topRated,
        filters: BrowseFilters(
          genreIds: [28, 12],
          yearFrom: 2010,
          yearTo: 2019,
          minRating: 7,
        ),
      );

      final uri = Uri.parse(query.toLocation());
      expect(uri.path, '/browse/movie/discover');
      expect(uri.queryParameters['genres'], '28,12');
      expect(uri.queryParameters['sort'], 'top-rated');

      final parsed = BrowseQuery.tryParse(uri)!;
      expect(parsed.type, MediaType.movie);
      expect(parsed.feed, BrowseFeed.discover);
      expect(parsed.title, 'Action');
      expect(parsed.sort, BrowseSort.topRated);
      expect(parsed.filters.genreIds, [28, 12]);
      expect(parsed.filters.yearFrom, 2010);
      expect(parsed.filters.yearTo, 2019);
      expect(parsed.filters.minRating, 7);
    });

    test('a bare feed carries no query string', () {
      const query = BrowseQuery(type: MediaType.tv, feed: BrowseFeed.featured);
      expect(query.toLocation(), '/browse/tv/featured');
      final parsed = BrowseQuery.tryParse(Uri.parse('/browse/tv/featured'))!;
      expect(parsed.sort, BrowseSort.popular);
      expect(parsed.filters.isEmpty, isTrue);
      expect(parsed.title, isNull);
    });

    test('an anchored feed keeps its id', () {
      const query = BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.recommendations,
        id: 603,
        title: 'Recommended',
      );
      final parsed = BrowseQuery.tryParse(Uri.parse(query.toLocation()))!;
      expect(parsed.id, 603);
      expect(parsed.feed, BrowseFeed.recommendations);
    });

    test('rejects links that name nothing the app can page', () {
      for (final path in [
        '/browse/podcast/top-rated',
        '/browse/movie/bogus',
        '/browse/movie/recommendations',
        '/browse/movie/similar?id=0',
        '/browse/tv/now-playing',
        '/browse/movie/on-the-air',
        '/browse/movie',
        '/detail/movie/1',
      ]) {
        expect(BrowseQuery.tryParse(Uri.parse(path)), isNull, reason: path);
      }
    });

    test('drops junk in the query string rather than the whole link', () {
      final parsed = BrowseQuery.tryParse(Uri.parse(
        '/browse/movie/discover?genres=28,abc,-1&sort=weird&rating=11&from=99&title=%20',
      ))!;
      expect(parsed.filters.genreIds, [28]);
      expect(parsed.sort, BrowseSort.popular);
      expect(parsed.filters.minRating, isNull);
      expect(parsed.filters.yearFrom, isNull);
      expect(parsed.title, isNull);
    });
  });

  group('BrowseFilters', () {
    test('describes itself in words for an empty grid', () {
      const filters = BrowseFilters(
        genreIds: [28, 35],
        yearFrom: 2010,
        yearTo: 2019,
        minRating: 7,
      );
      expect(
        filters.describe({28: 'Action', 35: 'Comedy'}),
        'Action, Comedy · 2010 to 2019 · rated 7+',
      );
      expect(const BrowseFilters(yearFrom: 2020).describe({}), '2020 onward');
      expect(const BrowseFilters(yearTo: 1999).describe({}), 'up to 1999');
      expect(const BrowseFilters(yearFrom: 2001, yearTo: 2001).describe({}),
          '2001');
    });

    test('counts filter groups, not values', () {
      expect(BrowseFilters.none.count, 0);
      expect(
        const BrowseFilters(genreIds: [1, 2, 3], yearTo: 2000, minRating: 6)
            .count,
        3,
      );
    });
  });

  group('BrowseSort', () {
    test('maps to the TMDB sort the server validates for each type', () {
      expect(BrowseSort.newest.tmdbSortBy(MediaType.movie),
          'primary_release_date.desc');
      expect(BrowseSort.newest.tmdbSortBy(MediaType.tv), 'first_air_date.desc');
      expect(BrowseSort.oldest.tmdbSortBy(MediaType.movie),
          'primary_release_date.asc');
      expect(BrowseSort.titleAz.tmdbSortBy(MediaType.movie), 'title.asc');
      expect(BrowseSort.titleAz.tmdbSortBy(MediaType.tv), 'name.asc');
      expect(BrowseSort.topRated.tmdbSortBy(MediaType.tv), 'vote_average.desc');
      expect(BrowseSort.popular.tmdbSortBy(MediaType.tv), 'popularity.desc');
    });
  });

  group('BrowseFeed', () {
    test('feeds follow the routes the server has for each type', () {
      expect(BrowseFeed.topRated.supports(MediaType.tv), isTrue);
      expect(BrowseFeed.upcoming.supports(MediaType.tv), isTrue);
      expect(BrowseFeed.nowPlaying.supports(MediaType.tv), isFalse);
      expect(BrowseFeed.onTheAir.supports(MediaType.movie), isFalse);
      expect(BrowseFeed.onTheAir.supports(MediaType.tv), isTrue);
      expect(BrowseFeed.popular.supports(MediaType.tv), isTrue);
      expect(BrowseFeed.featured.supports(MediaType.tv), isTrue);
    });

    test('the on-the-air feed has its own slug', () {
      const query = BrowseQuery(type: MediaType.tv, feed: BrowseFeed.onTheAir);
      expect(query.toLocation(), '/browse/tv/on-the-air');
      expect(BrowseQuery.tryParse(Uri.parse('/browse/tv/on-the-air'))?.feed,
          BrowseFeed.onTheAir);
    });
  });
}
