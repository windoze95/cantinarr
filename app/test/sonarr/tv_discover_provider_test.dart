import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/discover/data/discover_api_service.dart';
import 'package:cantinarr/features/sonarr/logic/tv_discover_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Answers each request from [respond] (a value or a future of one) and
/// records every URI asked for.
class _RecordingAdapter implements HttpClientAdapter {
  _RecordingAdapter(this.respond);

  final FutureOr<Object> Function(Uri uri) respond;
  final List<Uri> requested = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requested.add(options.uri);
    return ResponseBody.fromString(
      jsonEncode(await respond(options.uri)),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

Map<String, Object?> _show(int id) => {
      'id': id,
      'name': 'Show $id',
      'poster_path': null,
      'first_air_date': null,
      'vote_average': 0,
    };

Map<String, Object?> _tmdbPage(int page, List<int> ids,
        {required int totalPages}) =>
    {
      'page': page,
      'results': [for (final id in ids) _show(id)],
      'total_pages': totalPages,
      'total_results': 0,
    };

Map<String, Object?> _traktShow(int trakt, {int? tmdb}) => {
      'watchers': 1,
      'list_count': 1,
      'show': {
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

({TvDiscoverNotifier notifier, _RecordingAdapter adapter}) _harness(
  FutureOr<Object> Function(Uri uri) respond,
) {
  final adapter = _RecordingAdapter(respond);
  final dio = Dio(BaseOptions(baseUrl: 'http://cantinarr.test'))
    ..httpClientAdapter = adapter;
  return (
    notifier: TvDiscoverNotifier(DiscoverApiService(backendDio: dio)),
    adapter: adapter,
  );
}

int _page(Uri uri) => int.parse(uri.queryParameters['page'] ?? '1');

List<int> _pagesOf(_RecordingAdapter adapter, String path) =>
    [for (final uri in adapter.requested.where((u) => u.path == path)) _page(uri)];

List<int> _ids(Iterable<dynamic> items) => [for (final i in items) i.id as int];

void main() {
  const topRated = '/api/discover/tv/top-rated';
  const upcoming = '/api/discover/tv/upcoming';
  const onTheAir = '/api/discover/tv/on-the-air';
  const anticipated = '/api/trakt/anticipated';
  const genres = '/api/genres/tv';

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

    await h.notifier.loadMoreTopRated();
    expect(_pagesOf(h.adapter, topRated), [1, 2]);
  });

  test('bootstrap after paging restarts the row from page one', () async {
    final h = _harness((uri) => switch (uri.path) {
          upcoming => _tmdbPage(_page(uri), [_page(uri)], totalPages: 5),
          _ => _empty,
        });

    await h.notifier.bootstrap();
    await h.notifier.loadMoreUpcoming();
    expect(_ids(h.notifier.state.upcoming), [1, 2]);

    await h.notifier.bootstrap();
    expect(_ids(h.notifier.state.upcoming), [1]);
    expect(_pagesOf(h.adapter, upcoming), [1, 2, 1]);
  });

  test('Airing This Week loads from the on-the-air feed', () async {
    final h = _harness((uri) => switch (uri.path) {
          onTheAir => _tmdbPage(1, [7], totalPages: 1),
          _ => _empty,
        });

    await h.notifier.bootstrap();
    expect(_ids(h.notifier.state.onTheAir), [7]);
    expect(h.notifier.state.isLoadingOnTheAir, isFalse);
  });

  test('Most Anticipated pages Trakt shows until a page comes back empty',
      () async {
    final h = _harness((uri) => switch (uri.path) {
          anticipated => _page(uri) == 1
              ? [_traktShow(1, tmdb: 501), _traktShow(2)]
              : const <Object>[],
          _ => _empty,
        });

    await h.notifier.bootstrap();
    expect(_ids(h.notifier.state.anticipated), [501]);
    expect(
      h.adapter.requested
          .firstWhere((u) => u.path == anticipated)
          .queryParameters['type'],
      'shows',
    );

    await h.notifier.loadMoreAnticipated();
    expect(_ids(h.notifier.state.anticipated), [501]);
    expect(_pagesOf(h.adapter, anticipated), [1, 2]);
  });

  test('the genre strip reads TMDB\'s TV genres', () async {
    final h = _harness((uri) => switch (uri.path) {
          genres => {
              'genres': [
                {'id': 18, 'name': 'Drama'},
              ],
            },
          _ => _empty,
        });

    await h.notifier.bootstrap();
    expect(h.notifier.state.genres.single.name, 'Drama');
  });

  test('genres arriving late do not overwrite rows that resolved meanwhile',
      () async {
    final stalled = Completer<Object>();
    final h = _harness((uri) => switch (uri.path) {
          genres => stalled.future,
          topRated => _tmdbPage(1, [1], totalPages: 1),
          _ => _empty,
        });

    final booting = h.notifier.bootstrap();
    // The rows resolve while the genre read is still open.
    await pumpEventQueue(times: 50);
    expect(_ids(h.notifier.state.topRated), [1]);

    stalled.complete({
      'genres': [
        {'id': 18, 'name': 'Drama'},
      ],
    });
    await booting;
    expect(_ids(h.notifier.state.topRated), [1],
        reason: 'a stale snapshot must not wipe a row that resolved first');
    expect(h.notifier.state.genres.single.name, 'Drama');
    expect(h.notifier.state.isLoadingFeatured, isFalse);
  });
}
