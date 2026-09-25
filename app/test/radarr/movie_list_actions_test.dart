import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/radarr/data/radarr_api_service.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/ui/movie_actions.dart';
import 'package:cantinarr/features/radarr/ui/radarr_movie_list.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// The list and long-press sheet dispatch the same exact movie action.
/// Records request method, path, query, and body through a fake provider.
class _FakeAdapter implements HttpClientAdapter {
  final List<
      ({
        String method,
        String path,
        Map<String, dynamic> query,
        dynamic body,
      })> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    requests.add((
      method: options.method,
      path: path,
      query: options.uri.queryParameters,
      body: body,
    ));

    dynamic response = <String, dynamic>{};
    if (options.method == 'GET') {
      if (path.endsWith('/movie/7')) response = _rawMovie;
      if (path.endsWith('/qualityprofile')) response = _profiles;
      if (path.endsWith('/tag')) response = _tags;
      if (path.endsWith('/queue')) response = {'records': <dynamic>[]};
      if (path.endsWith('/history/movie')) response = <dynamic>[];
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

/// The movie as Radarr returns it, monitored so the sheet offers Unmonitor.
const _rawMovie = {
  'id': 7,
  'title': 'Example Movie',
  'year': 2020,
  'monitored': true,
  'qualityProfileId': 5,
  'minimumAvailability': 'announced',
  'path': '/movies/Example Movie (2020)',
  'tmdbId': 555,
  'imdbId': 'tt123',
  'tags': [2],
  'hasFile': false,
  'isAvailable': true,
};

const _profiles = [
  {'id': 5, 'name': 'HD-1080p'},
  {'id': 9, 'name': 'Best'},
];

const _tags = [
  {'id': 2, 'label': 'kids'},
  {'id': 3, 'label': '4k'},
];

final _movie = RadarrMovie.fromJson(Map<String, dynamic>.from(_rawMovie));

void main() {
  group('RadarrMovieList wired to showMovieActions', () {
    late _FakeAdapter adapter;

    Future<void> pumpWiredList(WidgetTester tester) async {
      adapter = _FakeAdapter();
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter;
      final service = RadarrApiService(backendDio: dio, instanceId: 'inst1');
      await tester.pumpWidget(
        ProviderScope(
          overrides: [backendClientProvider.overrideWithValue(dio)],
          child: MaterialApp(
            home: Scaffold(
              body: Builder(
                builder: (ctx) => RadarrMovieList(
                  movies: [_movie],
                  onAction: (movie, action) => showMovieActions(
                    ctx,
                    service: service,
                    instanceId: 'inst1',
                    movie: movie,
                    selectedAction: action,
                  ),
                ),
              ),
            ),
          ),
        ),
      );
    }

    testWidgets(
        'a long-press opens the movie action sheet and Search Movie posts '
        'MoviesSearch for that movie', (tester) async {
      await pumpWiredList(tester);

      await tester.longPress(find.text('Example Movie'));
      await tester.pumpAndSettle();
      for (final label in [
        'Automatic search',
        'Edit movie',
        'Refresh metadata',
        'Remove…',
        'Unmonitor movie',
      ]) {
        expect(find.text(label), findsOneWidget);
      }

      await tester.tap(find.text('Automatic search'));
      await tester.pumpAndSettle();

      final posts = adapter.requests.where((r) => r.method == 'POST').toList();
      expect(posts, hasLength(1));
      expect(posts.single.path, endsWith('/command'));
      expect(posts.single.body, {
        'name': 'MoviesSearch',
        'movieIds': [7]
      });
    });
  });
}
