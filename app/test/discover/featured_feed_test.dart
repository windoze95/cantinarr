import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/discover/data/discover_api_service.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Answers GETs from a fixed map of path → payload, recording what was asked.
class _StubAdapter implements HttpClientAdapter {
  _StubAdapter(this.responses);

  final Map<String, Object> responses;
  final List<String> requested = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path;
    requested.add(path);
    return ResponseBody.fromString(
      jsonEncode(responses[path] ?? const <String, Object>{}),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

/// A Dio wired to the stub adapter.
({Dio dio, _StubAdapter adapter}) _stub(Map<String, Object> responses) {
  final adapter = _StubAdapter(responses);
  final dio = Dio(BaseOptions(baseUrl: 'http://cantinarr.test'))
    ..httpClientAdapter = adapter;
  return (dio: dio, adapter: adapter);
}

void main() {
  group('fetchFeaturedTV', () {
    test('reads the items and the source that answered', () async {
      final stub = _stub({
        '/api/discover/tv/featured': {
          'source': 'trakt_trending',
          'page': 1,
          'total_pages': 1,
          'total_results': 2,
          'results': [
            {
              'id': 95396,
              'name': 'Severance',
              'poster_path': 'https://walter-r2.trakt.tv/poster.jpg',
              'first_air_date': '2022-02-18',
            },
            {'id': 1, 'name': 'The Pitt', 'poster_path': '/pitt.jpg'},
          ],
        },
      });

      final feed =
          await DiscoverApiService(backendDio: stub.dio).fetchFeaturedTV();

      expect(stub.adapter.requested, ['/api/discover/tv/featured']);
      expect(feed.source, 'trakt_trending');
      expect(feed.items, hasLength(2));
      expect(feed.items.first.title, 'Severance');
      expect(feed.items.first.mediaType, MediaType.tv);
      // Trakt art arrives as an absolute URL; TMDB art as a bare path. Both
      // must survive parsing untouched for the card to build the right URL.
      expect(feed.items.first.posterPath,
          'https://walter-r2.trakt.tv/poster.jpg');
      expect(feed.items.last.posterPath, '/pitt.jpg');
    });

    test('survives a payload with no source or results', () async {
      final stub = _stub({'/api/discover/tv/featured': <String, Object>{}});
      final feed =
          await DiscoverApiService(backendDio: stub.dio).fetchFeaturedTV();
      expect(feed.source, '');
      expect(feed.items, isEmpty);
    });
  });

  group('fetchFeaturedMovies', () {
    test('parses movie entries from the movie endpoint', () async {
      final stub = _stub({
        '/api/discover/movies/featured': {
          'source': 'tmdb_trending',
          'results': [
            {'id': 1233413, 'title': 'Sinners', 'release_date': '2025-04-18'},
          ],
        },
      });

      final feed =
          await DiscoverApiService(backendDio: stub.dio).fetchFeaturedMovies();

      expect(stub.adapter.requested, ['/api/discover/movies/featured']);
      expect(feed.source, 'tmdb_trending');
      expect(feed.items.single.title, 'Sinners');
      expect(feed.items.single.mediaType, MediaType.movie);
      expect(feed.items.single.releaseDate, '2025-04-18');
    });
  });

  group('featuredRowTitle', () {
    test('names the feed the row is actually showing', () {
      expect(featuredRowTitle('tmdb_trending', isTv: true), 'Trending This Week');
      expect(featuredRowTitle('trakt_trending', isTv: true), 'Trending Now');
      expect(featuredRowTitle('tmdb_popular', isTv: true), 'Popular TV Shows');
      expect(featuredRowTitle('tmdb_popular', isTv: false), 'Popular Movies');
    });

    test('falls back to the default source label for an unknown source', () {
      // A row that cannot name its source must not claim to be "Popular" —
      // that is the label this change exists to stop over-applying.
      expect(featuredRowTitle('', isTv: true), 'Trending This Week');
      expect(featuredRowTitle('from_a_newer_build', isTv: false),
          'Trending This Week');
    });
  });
}
