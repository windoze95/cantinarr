import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/discover/data/discover_api_service.dart';
import 'package:cantinarr/features/radarr/logic/movie_discover_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Answers each request from [respond] and records every URI asked for, so a
/// test can assert which page of which feed a row requested.
class _RecordingAdapter implements HttpClientAdapter {
  _RecordingAdapter(this.respond);

  final Object Function(Uri uri) respond;
  final List<Uri> requested = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requested.add(options.uri);
    return ResponseBody.fromString(
      jsonEncode(respond(options.uri)),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
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

Map<String, Object?> _tmdbPage(int page, List<int> ids,
        {required int totalPages}) =>
    {
      'page': page,
      'results': [for (final id in ids) _movie(id)],
      'total_pages': totalPages,
      'total_results': 0,
    };

Map<String, Object?> _traktMovie(int trakt, {int? tmdb}) => {
      'watchers': 1,
      'list_count': 1,
      'movie': {
        'title': 'Anticipated $trakt',
        'year': 2027,
        'ids': {'trakt': trakt, 'slug': 'anticipated-$trakt', 'tmdb': tmdb},
      },
    };

const _empty = {
  'page': 1,
  'results': <Object>[],
  'total_pages': 0,
  'total_results': 0,
};

({MovieDiscoverNotifier notifier, _RecordingAdapter adapter}) _harness(
  Object Function(Uri uri) respond,
) {
  final adapter = _RecordingAdapter(respond);
  final dio = Dio(BaseOptions(baseUrl: 'http://cantinarr.test'))
    ..httpClientAdapter = adapter;
  return (
    notifier: MovieDiscoverNotifier(DiscoverApiService(backendDio: dio)),
    adapter: adapter,
  );
}

int _page(Uri uri) => int.parse(uri.queryParameters['page'] ?? '1');

List<int> _pagesOf(_RecordingAdapter adapter, String path) =>
    [for (final uri in adapter.requested.where((u) => u.path == path)) _page(uri)];

List<int> _ids(Iterable<dynamic> items) => [for (final i in items) i.id as int];

void main() {
  const topRated = '/api/discover/movies/top-rated';
  const upcoming = '/api/discover/movies/upcoming';
  const nowPlaying = '/api/discover/movies/now-playing';
  const anticipated = '/api/trakt/anticipated';

  test('Top Rated appends its next page without repeating a title', () async {
    final h = _harness((uri) => switch (uri.path) {
          topRated => _page(uri) == 1
              ? _tmdbPage(1, [1, 2, 3], totalPages: 2)
              : _tmdbPage(2, [3, 4], totalPages: 2),
          _ => _empty,
        });

    await h.notifier.bootstrap();
    expect(_ids(h.notifier.state.topRated), [1, 2, 3]);

    await h.notifier.loadMoreTopRated();
    expect(_ids(h.notifier.state.topRated), [1, 2, 3, 4]);
    expect(h.notifier.state.isLoadingTopRated, isFalse);
    expect(_pagesOf(h.adapter, topRated), [1, 2]);

    // The feed said two pages: a further ask costs no request.
    await h.notifier.loadMoreTopRated();
    expect(_pagesOf(h.adapter, topRated), [1, 2]);
  });

  test('bootstrap after paging restarts the row from page one', () async {
    final h = _harness((uri) => switch (uri.path) {
          topRated => _tmdbPage(_page(uri), [_page(uri)], totalPages: 5),
          _ => _empty,
        });

    await h.notifier.bootstrap();
    await h.notifier.loadMoreTopRated();
    expect(_ids(h.notifier.state.topRated), [1, 2]);

    await h.notifier.bootstrap();
    expect(_ids(h.notifier.state.topRated), [1]);
    expect(_pagesOf(h.adapter, topRated), [1, 2, 1]);
  });

  test('In Theaters loads from the now-playing feed', () async {
    final h = _harness((uri) => switch (uri.path) {
          nowPlaying => _tmdbPage(1, [7], totalPages: 1),
          _ => _empty,
        });

    await h.notifier.bootstrap();
    expect(_ids(h.notifier.state.nowPlaying), [7]);
    expect(h.notifier.state.isLoadingNowPlaying, isFalse);
  });

  test('a page the filter emptied is skipped rather than ending the row',
      () async {
    final h = _harness((uri) => switch (uri.path) {
          upcoming => switch (_page(uri)) {
              1 => _tmdbPage(1, [1], totalPages: 4),
              3 => _tmdbPage(3, [2], totalPages: 4),
              _ => _tmdbPage(_page(uri), const [], totalPages: 4),
            },
          _ => _empty,
        });

    await h.notifier.bootstrap();
    await h.notifier.loadMoreUpcoming();
    expect(_ids(h.notifier.state.upcoming), [1, 2]);
    expect(_pagesOf(h.adapter, upcoming), [1, 2, 3]);
  });

  test('Most Anticipated pages Trakt until a page comes back empty',
      () async {
    final h = _harness((uri) => switch (uri.path) {
          anticipated => _page(uri) == 1
              ? [_traktMovie(1, tmdb: 501), _traktMovie(2)]
              : const <Object>[],
          _ => _empty,
        });

    await h.notifier.bootstrap();
    // A Trakt title TMDB does not know has nothing to open, so it is dropped.
    expect(_ids(h.notifier.state.anticipated), [501]);
    expect(h.adapter.requested.firstWhere((u) => u.path == anticipated)
        .queryParameters['type'], 'movies');

    await h.notifier.loadMoreAnticipated();
    expect(_ids(h.notifier.state.anticipated), [501]);
    expect(h.notifier.state.isLoadingAnticipated, isFalse);
    expect(_pagesOf(h.adapter, anticipated), [1, 2]);

    // An empty page ended the feed.
    await h.notifier.loadMoreAnticipated();
    expect(_pagesOf(h.adapter, anticipated), [1, 2]);
  });
}
