import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:flutter_test/flutter_test.dart';

/// The language, streaming, keyword, and studio filters: how they ride in a
/// browse URL, how they count, and how an empty grid speaks them.
void main() {
  group('BrowseQuery with the extended filters', () {
    test('every filter survives the round trip through its URL', () {
      const query = BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.discover,
        filters: BrowseFilters(
          language: 'ko',
          providerIds: [8, 9],
          watchRegion: 'GB',
          keywords: [TaggedId(id: 1, name: 'heist')],
          companies: [TaggedId(id: 2, name: 'A24')],
        ),
      );

      final uri = Uri.parse(query.toLocation());
      expect(uri.queryParameters['lang'], 'ko');
      expect(uri.queryParameters['prov'], '8,9');
      expect(uri.queryParameters['region'], 'GB');
      expect(uri.queryParameters['kw'], '1');
      expect(uri.queryParameters['co'], '2');

      final parsed = BrowseQuery.tryParse(uri)!.filters;
      expect(parsed.language, 'ko');
      expect(parsed.providerIds, [8, 9]);
      expect(parsed.watchRegion, 'GB');
      // Names do not travel: a link knows its keywords and studios by id.
      expect(parsed.keywords, [const TaggedId(id: 1)]);
      expect(parsed.companies, [const TaggedId(id: 2)]);
    });

    test('the region rides only with services', () {
      const filters = BrowseFilters(watchRegion: 'GB');
      const query = BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.discover,
        filters: filters,
      );
      expect(query.toLocation(), '/browse/movie/discover');
      expect(filters.isEmpty, isTrue);
      expect(filters.count, 0);
    });

    test('junk in the new keys drops the value, never the link', () {
      final parsed = BrowseQuery.tryParse(Uri.parse(
        '/browse/movie/discover?lang=k0&prov=abc,0,8&region=gbr&kw=x&co=-2',
      ))!;
      expect(parsed.filters.language, isNull);
      expect(parsed.filters.providerIds, [8]);
      expect(parsed.filters.watchRegion, isNull);
      expect(parsed.filters.keywords, isEmpty);
      expect(parsed.filters.companies, isEmpty);

      final lower = BrowseQuery.tryParse(
        Uri.parse('/browse/movie/discover?prov=8&region=gb&lang=KO'),
      )!;
      expect(lower.filters.watchRegion, 'GB');
      expect(lower.filters.language, 'ko');
    });
  });

  group('BrowseFilters', () {
    test('describes every group, naming what it can', () {
      const filters = BrowseFilters(
        genreIds: [28, 35],
        yearFrom: 2010,
        yearTo: 2019,
        minRating: 7,
        language: 'ko',
        providerIds: [8, 15],
        keywords: [TaggedId(id: 1, name: 'heist')],
        companies: [TaggedId(id: 2, name: 'A24')],
      );
      expect(
        filters.describe(
          {28: 'Action', 35: 'Comedy'},
          languageNames: {'ko': 'Korean'},
          providerNames: {8: 'Netflix', 15: 'Hulu'},
        ),
        'Action, Comedy · 2010 to 2019 · rated 7+ · in Korean · on Netflix, Hulu · about heist · from A24',
      );
    });

    test('speaks an unnamed value by its id rather than dropping it', () {
      const filters = BrowseFilters(
        language: 'ko',
        providerIds: [8],
        keywords: [TaggedId(id: 1)],
        companies: [TaggedId(id: 2)],
      );
      expect(filters.describe({}),
          'in ko · on service 8 · about keyword 1 · from studio 2');
    });

    test('counts seven groups at most, region excluded', () {
      const filters = BrowseFilters(
        genreIds: [1],
        yearTo: 2000,
        minRating: 6,
        language: 'fr',
        providerIds: [8],
        watchRegion: 'FR',
        keywords: [TaggedId(id: 1)],
        companies: [TaggedId(id: 2)],
      );
      expect(filters.count, 7);
      expect(filters.copyWith(providerIds: const []).count, 6);
      expect(filters.copyWith(language: () => null).language, isNull);
    });
  });

  group('watchRegionFor', () {
    test('uppercases a two-letter country and falls back to the US', () {
      expect(watchRegionFor('gb'), 'GB');
      expect(watchRegionFor('US'), 'US');
      expect(watchRegionFor(null), 'US');
      expect(watchRegionFor(''), 'US');
      // Locale('es', '419') has a numeric region.
      expect(watchRegionFor('419'), 'US');
    });
  });
}
