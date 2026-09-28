import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/discover/data/discover_api_service.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/browse_grid_notifier.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:cantinarr/features/discover/logic/browse_session_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Answers each request from [respond] (which may fail with a status) and
/// records every URI asked for.
class _RecordingAdapter implements HttpClientAdapter {
  _RecordingAdapter(this.respond);

  FutureOr<Object> Function(Uri uri) respond;
  final List<Uri> requested = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requested.add(options.uri);
    final body = await respond(options.uri);
    if (body is int) {
      return ResponseBody.fromString('{"error":"nope"}', body, headers: {
        'content-type': ['application/json'],
      });
    }
    return ResponseBody.fromString(jsonEncode(body), 200, headers: {
      'content-type': ['application/json'],
    });
  }

  @override
  void close({bool force = false}) {}
}

Map<String, Object?> _movie(int id) => {
      'id': id,
      'title': 'Movie $id',
      'poster_path': null,
      'release_date': null,
      'vote_average': 0,
    };

Map<String, Object?> _page(int page, List<int> ids, {int totalPages = 1}) => {
      'page': page,
      'results': [for (final id in ids) _movie(id)],
      'total_pages': totalPages,
      'total_results': 0,
    };

int _pageOf(Uri uri) => int.parse(uri.queryParameters['page'] ?? '1');

({BrowseGridNotifier notifier, _RecordingAdapter adapter}) _harness(
  BrowseQuery query,
  FutureOr<Object> Function(Uri uri) respond,
) {
  final adapter = _RecordingAdapter(respond);
  final dio = Dio(BaseOptions(baseUrl: 'http://cantinarr.test'))
    ..httpClientAdapter = adapter;
  return (
    notifier: BrowseGridNotifier(DiscoverApiService(backendDio: dio), query),
    adapter: adapter,
  );
}

List<int> _ids(List<MediaItem> items) => [for (final i in items) i.id];

void main() {
  test('each feed pages its own endpoint', () async {
    const cases = <BrowseQuery, String>{
      BrowseQuery(type: MediaType.movie, feed: BrowseFeed.featured):
          '/api/discover/movies/featured',
      BrowseQuery(type: MediaType.tv, feed: BrowseFeed.featured):
          '/api/discover/tv/featured',
      BrowseQuery(type: MediaType.movie, feed: BrowseFeed.popular):
          '/api/discover/movies/popular',
      BrowseQuery(type: MediaType.tv, feed: BrowseFeed.popular):
          '/api/discover/tv/popular',
      BrowseQuery(type: MediaType.movie, feed: BrowseFeed.topRated):
          '/api/discover/movies/top-rated',
      BrowseQuery(type: MediaType.movie, feed: BrowseFeed.upcoming):
          '/api/discover/movies/upcoming',
      BrowseQuery(type: MediaType.movie, feed: BrowseFeed.nowPlaying):
          '/api/discover/movies/now-playing',
      BrowseQuery(type: MediaType.tv, feed: BrowseFeed.topRated):
          '/api/discover/tv/top-rated',
      BrowseQuery(type: MediaType.tv, feed: BrowseFeed.upcoming):
          '/api/discover/tv/upcoming',
      BrowseQuery(type: MediaType.tv, feed: BrowseFeed.onTheAir):
          '/api/discover/tv/on-the-air',
      BrowseQuery(type: MediaType.movie, feed: BrowseFeed.discover):
          '/api/discover/movies',
      BrowseQuery(type: MediaType.tv, feed: BrowseFeed.discover):
          '/api/discover/tv',
      BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.recommendations,
        id: 603,
      ): '/api/media/movie/603/recommendations',
      BrowseQuery(type: MediaType.tv, feed: BrowseFeed.similar, id: 603):
          '/api/media/tv/603/similar',
    };
    for (final entry in cases.entries) {
      final h = _harness(entry.key, (_) => _page(1, [1]));
      await h.notifier.load();
      expect(h.adapter.requested.single.path, entry.value,
          reason: '${entry.key.feed} for ${entry.key.type}');
      expect(h.adapter.requested.single.queryParameters['page'], '1');
      expect(_ids(h.notifier.items), [1]);
    }
  });

  test('the anticipated feed pages Trakt for the right type', () async {
    final h = _harness(
      const BrowseQuery(type: MediaType.tv, feed: BrowseFeed.anticipated),
      (_) => [
        {
          'show': {
            'title': 'Soon',
            'year': 2027,
            'ids': {'trakt': 1, 'tmdb': 77},
          },
        },
      ],
    );
    await h.notifier.load();
    final uri = h.adapter.requested.single;
    expect(uri.path, '/api/trakt/anticipated');
    expect(uri.queryParameters['type'], 'shows');
    expect(_ids(h.notifier.items), [77]);
  });

  test('the Browse feed sends its filters and sort as TMDB parameters',
      () async {
    final h = _harness(
      const BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.discover,
        sort: BrowseSort.topRated,
        filters: BrowseFilters(
          genreIds: [28, 12],
          yearFrom: 2010,
          yearTo: 2019,
          minRating: 7,
        ),
      ),
      (_) => _page(1, [1]),
    );
    await h.notifier.load();
    final q = h.adapter.requested.single.queryParameters;
    expect(q['with_genres'], '28,12');
    expect(q['sort_by'], 'vote_average.desc');
    expect(q['primary_release_date.gte'], '2010-01-01');
    expect(q['primary_release_date.lte'], '2019-12-31');
    expect(q['vote_average.gte'], '7.0');
    expect(q['vote_count.gte'], '${BrowseGridNotifier.ratedMinVotes}');
  });

  test('a TV browse uses the air-date keys, and an unrated sort no vote floor',
      () async {
    final h = _harness(
      const BrowseQuery(
        type: MediaType.tv,
        feed: BrowseFeed.discover,
        sort: BrowseSort.titleAz,
        filters: BrowseFilters(yearFrom: 2020),
      ),
      (_) => _page(1, [1]),
    );
    await h.notifier.load();
    final q = h.adapter.requested.single.queryParameters;
    expect(q['sort_by'], 'name.asc');
    expect(q['first_air_date.gte'], '2020-01-01');
    expect(q.containsKey('first_air_date.lte'), isFalse);
    expect(q.containsKey('vote_count.gte'), isFalse);
    expect(q.containsKey('with_genres'), isFalse);
  });

  test('loadMore appends the next page without repeating a title', () async {
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.topRated),
      (uri) => _pageOf(uri) == 1
          ? _page(1, [1, 2], totalPages: 2)
          : _page(2, [2, 3], totalPages: 2),
    );
    await h.notifier.load();
    expect(_ids(h.notifier.items), [1, 2]);
    expect(h.notifier.hasMore, isTrue);

    await h.notifier.loadMore();
    expect(_ids(h.notifier.items), [1, 2, 3]);
    expect(h.notifier.hasMore, isFalse);

    await h.notifier.loadMore();
    expect(h.adapter.requested.map(_pageOf), [1, 2]);
  });

  test('changing the query drops the page a superseded load was awaiting',
      () async {
    final stalled = Completer<Object>();
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.discover),
      (uri) => uri.queryParameters['with_genres'] == '28'
          ? _page(1, [28])
          : stalled.future,
    );

    final first = h.notifier.load();
    await h.notifier.setQuery(h.notifier.query.copyWith(
      filters: const BrowseFilters(genreIds: [28]),
    ));
    expect(_ids(h.notifier.items), [28]);

    stalled.complete(_page(1, [1, 2, 3]));
    await first;
    expect(_ids(h.notifier.items), [28],
        reason: 'the stale unfiltered page must not replace the filtered one');
  });

  test('an unreadable feed is an error, never an empty grid', () async {
    var broken = true;
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.topRated),
      (_) => broken ? 503 : _page(1, [1]),
    );
    await h.notifier.load();
    expect(h.notifier.items, isEmpty);
    expect(h.notifier.error, isNotNull);
    expect(h.notifier.isLoading, isFalse);

    broken = false;
    await h.notifier.load();
    expect(_ids(h.notifier.items), [1]);
    expect(h.notifier.error, isNull);
  });

  test('an anchored feed pages its title\'s endpoint', () async {
    final h = _harness(
      const BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.recommendations,
        id: 603,
      ),
      (uri) => _page(_pageOf(uri), [_pageOf(uri)], totalPages: 2),
    );
    await h.notifier.load();
    await h.notifier.loadMore();
    expect(
      [for (final u in h.adapter.requested) '${u.path}?page=${_pageOf(u)}'],
      [
        '/api/media/movie/603/recommendations?page=1',
        '/api/media/movie/603/recommendations?page=2',
      ],
    );
    expect(_ids(h.notifier.items), [1, 2]);
  });

  test('language, services, keywords and studios travel as TMDB parameters',
      () async {
    final h = _harness(
      const BrowseQuery(
        type: MediaType.movie,
        feed: BrowseFeed.discover,
        filters: BrowseFilters(
          language: 'ko',
          providerIds: [8, 9],
          watchRegion: 'GB',
          keywords: [TaggedId(id: 1), TaggedId(id: 2)],
          companies: [TaggedId(id: 3), TaggedId(id: 4)],
        ),
      ),
      (_) => _page(1, [1]),
    );
    await h.notifier.load();
    final q = h.adapter.requested.single.queryParameters;
    expect(q['with_original_language'], 'ko');
    expect(q['with_watch_providers'], '8|9');
    expect(q['watch_region'], 'GB');
    expect(q['with_keywords'], '1,2');
    expect(q['with_companies'], '3|4');
  });

  test('services without a region are asked for in the US', () async {
    final h = _harness(
      const BrowseQuery(
        type: MediaType.tv,
        feed: BrowseFeed.discover,
        filters: BrowseFilters(providerIds: [8]),
      ),
      (_) => _page(1, [1]),
    );
    await h.notifier.load();
    final q = h.adapter.requested.single.queryParameters;
    expect(q['with_watch_providers'], '8');
    expect(q['watch_region'], 'US');
    expect(q.containsKey('with_keywords'), isFalse);
    expect(q.containsKey('with_companies'), isFalse);
    expect(q.containsKey('with_original_language'), isFalse);
  });

  test('the headline feed remembers which source answered', () async {
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.featured),
      (_) => {..._page(1, [1]), 'source': 'trakt_trending'},
    );
    await h.notifier.load();
    expect(h.notifier.featuredSource, 'trakt_trending');
  });
  test('refresh commits the complete loaded window and retains it on a failed page', () async {
    var changed = false;
    var failPageTwo = false;
    final pending = Completer<Object>();
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.popular),
      (uri) {
        final page = _pageOf(uri);
        if (failPageTwo && page == 2) return 503;
        if (changed && page == 1) return pending.future;
        return _page(page, [page + (changed ? 10 : 0)], totalPages: 3);
      },
    );
    await h.notifier.load();
    await h.notifier.loadMore();
    h.notifier.scrollOffset = 200;
    changed = true;
    failPageTwo = true;
    final first = h.notifier.refresh();
    final second = h.notifier.refresh();
    expect(_ids(h.notifier.items), [1, 2]);
    expect(h.notifier.isLoading, isFalse);
    await h.notifier.loadMore();
    pending.complete(_page(1, [11], totalPages: 3));
    await Future.wait([first, second]);
    expect(_ids(h.notifier.items), [1, 2]);
    expect(h.notifier.error, isNotNull);
    expect(h.notifier.scrollOffset, 200);
    expect(h.adapter.requested.map(_pageOf), [1, 2, 1, 2]);
    failPageTwo = false;
    await h.notifier.refresh();
    expect(_ids(h.notifier.items), [11, 12]);
    expect(h.notifier.error, isNull);
    await h.notifier.loadMore();
    expect(_ids(h.notifier.items), [11, 12, 13]);
  });

  test('refresh fences a pending pagination result', () async {
    final pending = Completer<Object>();
    var changed = false;
    final h = _harness(
      const BrowseQuery(type: MediaType.tv, feed: BrowseFeed.popular),
      (uri) => _pageOf(uri) == 2 ? pending.future
          : _page(1, [changed ? 9 : 1], totalPages: 3),
    );
    await h.notifier.load();
    final more = h.notifier.loadMore();
    await Future<void>.delayed(Duration.zero);
    changed = true;
    await h.notifier.refresh();
    pending.complete(_page(2, [2], totalPages: 3));
    await more;
    expect(_ids(h.notifier.items), [9]);
    expect(h.notifier.isLoading, isFalse);
  });

  test('successful empty refresh clears old titles; denied refresh clears them too', () async {
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.popular),
      (_) => _page(1, [1]),
    );
    await h.notifier.load();
    h.adapter.respond = (_) => _page(1, []);
    await h.notifier.refresh();
    expect(h.notifier.items, isEmpty);
    expect(h.notifier.error, isNull);
    expect(h.notifier.hasLoaded, isTrue);
    h.adapter.respond = (_) => _page(1, [2]);
    await h.notifier.refresh();
    h.adapter.respond = (_) => 403;
    await h.notifier.refresh();
    expect(h.notifier.items, isEmpty);
    expect(h.notifier.error, isNotNull);
  });

  test('fifty-page limit also bounds empty-page walking and refresh', () async {
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.popular),
      (uri) => _page(_pageOf(uri), _pageOf(uri) == 50 ? [] : [_pageOf(uri)],
          totalPages: 500),
    );
    await h.notifier.load();
    for (var page = 2; page <= 52; page++) { await h.notifier.loadMore(); }
    expect(h.adapter.requested.map(_pageOf).last, 50);
    await h.notifier.refresh();
    expect(h.adapter.requested.map(_pageOf).last, 50);
    expect(h.notifier.items, hasLength(49));
  });

  test('query changes and disposal reject late refresh results', () async {
    final pending = Completer<Object>();
    final h = _harness(
      const BrowseQuery(type: MediaType.movie, feed: BrowseFeed.discover),
      (uri) => uri.queryParameters['with_genres'] == '28'
          ? _page(1, [28]) : pending.future,
    );
    final first = h.notifier.refresh();
    await h.notifier.setQuery(h.notifier.query.copyWith(
        filters: const BrowseFilters(genreIds: [28])));
    pending.complete(_page(1, [1]));
    await first;
    expect(_ids(h.notifier.items), [28]);
    final pendingAgain = Completer<Object>();
    h.adapter.respond = (_) => pendingAgain.future;
    final read = h.notifier.refresh();
    h.notifier.dispose();
    pendingAgain.complete(_page(1, [2]));
    await read;
  });

  test('session cache reuses complete queries and evicts the least recently used', () async {
    final adapter = _RecordingAdapter((uri) => _page(1, [1]));
    final cache = BrowseSession(DiscoverApiService(backendDio:
        Dio(BaseOptions(baseUrl: 'http://cantinarr.test'))..httpClientAdapter = adapter));
    addTearDown(cache.dispose);
    const query = BrowseQuery(type: MediaType.movie, feed: BrowseFeed.discover,
        filters: BrowseFilters(language: 'ko', providerIds: [8], watchRegion: 'GB',
          keywords: [TaggedId(id: 1)], companies: [TaggedId(id: 2)]));
    final first = cache.acquire(query);
    await first.load();
    first.scrollOffset = 120;
    cache.release(first);
    final sameQuery = cache.acquire(BrowseQuery.tryParse(Uri.parse(query.toLocation()))!);
    expect(sameQuery, same(first));
    expect(sameQuery.scrollOffset, 120);
    expect(sameQuery.items, hasLength(1));
    cache.release(sameQuery);
    for (var i = 0; i < BrowseSession.maxQueries; i++) {
      final entry = cache.acquire(query.copyWith(filters: BrowseFilters(yearFrom: 2000 + i)));
      cache.release(entry);
    }
    expect(cache.acquire(query), isNot(same(first)));
  });

  test('returning to a mounted query renews its place in the cache', () {
    final cache = BrowseSession(DiscoverApiService(backendDio: Dio()));
    addTearDown(cache.dispose);
    BrowseQuery query(int id) => BrowseQuery(type: MediaType.movie,
        feed: BrowseFeed.similar, id: id);
    final first = cache.acquire(query(1));
    cache.release(first);
    for (var id = 2; id <= 8; id++) {
      final entry = cache.acquire(query(id));
      cache.release(entry);
    }
    cache.touch(first);
    final ninth = cache.acquire(query(9));
    cache.release(ninth);
    expect(cache.acquire(query(1)), same(first));
  });

}
