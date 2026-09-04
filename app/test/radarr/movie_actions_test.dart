import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/radarr/data/radarr_api_service.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/ui/movie_actions.dart';
import 'package:cantinarr/features/radarr/ui/radarr_movie_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// The movie action sheet and the movie page's app bar: the same set the
/// series page carries (links, edit, actions), pinned on the wire. Mirrors
/// the series actions test.
///
/// Fake Dio adapter: routes GETs to canned bodies and records every request
/// (method, path, query, decoded body) for assertions.
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

/// The movie as Radarr returns it, including a field the app doesn't model —
/// updates must send it back unchanged.
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
  'someUnknownField': {
    'nested': [1, 2]
  },
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

({_FakeAdapter adapter, Dio dio}) _fakeDio() {
  final adapter = _FakeAdapter();
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  return (adapter: adapter, dio: dio);
}

const _authState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => _authState;
}

void main() {
  group('RadarrMovie', () {
    test('parses the IMDb id and tags, and reads a blank IMDb id as unknown',
        () {
      expect(_movie.imdbId, 'tt123');
      expect(_movie.tags, [2]);
      final blank = RadarrMovie.fromJson({
        'id': 8,
        'title': 'No ids',
        'year': 2021,
        'imdbId': '  ',
      });
      expect(blank.imdbId, isNull);
      expect(blank.tags, isEmpty);
    });
  });

  group('showMovieActions', () {
    late _FakeAdapter adapter;
    var changed = 0;
    var removed = 0;

    Future<void> pumpHarness(WidgetTester tester) async {
      final fake = _fakeDio();
      adapter = fake.adapter;
      changed = 0;
      removed = 0;
      final service =
          RadarrApiService(backendDio: fake.dio, instanceId: 'inst1');
      await tester.pumpWidget(
        ProviderScope(
          overrides: [backendClientProvider.overrideWithValue(fake.dio)],
          child: MaterialApp(
            home: Scaffold(
              body: Builder(
                builder: (ctx) => TextButton(
                  onPressed: () => showMovieActions(
                    ctx,
                    service: service,
                    instanceId: 'inst1',
                    movie: _movie,
                    onChanged: () => changed++,
                    onRemoved: () => removed++,
                  ),
                  child: const Text('open'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
    }

    List<
        ({
          String method,
          String path,
          Map<String, dynamic> query,
          dynamic body
        })> ofMethod(
            String method) =>
        adapter.requests.where((r) => r.method == method).toList();

    testWidgets('shows every action for a monitored movie', (tester) async {
      await pumpHarness(tester);
      for (final label in [
        'Search Movie',
        'Edit Movie',
        'Refresh Movie',
        'Remove Movie',
        'Unmonitor Movie',
      ]) {
        expect(find.text(label), findsOneWidget);
      }
    });

    testWidgets('Search Movie posts a MoviesSearch command', (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Search Movie'));
      await tester.pumpAndSettle();

      final posts = ofMethod('POST');
      expect(posts, hasLength(1));
      expect(posts.single.path, endsWith('/command'));
      expect(posts.single.body, {
        'name': 'MoviesSearch',
        'movieIds': [7]
      });
    });

    testWidgets('Refresh Movie posts RefreshMovie and reloads', (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Refresh Movie'));
      await tester.pumpAndSettle();

      final posts = ofMethod('POST');
      expect(posts.single.body, {
        'name': 'RefreshMovie',
        'movieIds': [7]
      });
      expect(changed, 1);
    });

    testWidgets(
        'Unmonitor round-trips the whole movie with only monitored flipped',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Unmonitor Movie'));
      await tester.pumpAndSettle();

      final puts = ofMethod('PUT');
      expect(puts, hasLength(1));
      expect(puts.single.path, endsWith('/movie/7'));
      final body = puts.single.body as Map<String, dynamic>;
      expect(body['monitored'], false);
      expect(
          body['someUnknownField'],
          {
            'nested': [1, 2]
          },
          reason: 'unmodelled fields must survive the round-trip');
      expect(body['qualityProfileId'], 5);
      expect(changed, 1);
    });

    testWidgets('Remove asks for confirmation and defaults to keeping files',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Remove Movie'));
      await tester.pumpAndSettle();

      final checkbox =
          tester.widget<CheckboxListTile>(find.byType(CheckboxListTile));
      expect(checkbox.value, isFalse);

      // Cancel first: nothing deleted.
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
      expect(ofMethod('DELETE'), isEmpty);
      expect(removed, 0);

      // Again, accept the safe default: keep files on disk.
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Remove Movie'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Remove'));
      await tester.pumpAndSettle();

      final deletes = ofMethod('DELETE');
      expect(deletes, hasLength(1));
      expect(deletes.single.path, endsWith('/movie/7'));
      expect(deletes.single.query['deleteFiles'], 'false');
      expect(removed, 1);
    });

    testWidgets('Remove can opt in to deleting files from disk',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Remove Movie'));
      await tester.pumpAndSettle();

      await tester.tap(find.text('Also delete files from disk'));
      await tester.pump();
      await tester.tap(find.text('Remove'));
      await tester.pumpAndSettle();

      final deletes = ofMethod('DELETE');
      expect(deletes, hasLength(1));
      expect(deletes.single.path, endsWith('/movie/7'));
      expect(deletes.single.query['deleteFiles'], 'true');
      expect(removed, 1);
    });

    testWidgets(
        'Edit Movie opens the editor; saving PUTs the patch and reloads',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Edit Movie'));
      await tester.pumpAndSettle();

      // The editor loaded the fresh movie + profiles + tags.
      expect(find.text('Edit Movie'), findsOneWidget);
      expect(find.text('HD-1080p'), findsOneWidget);
      expect(find.text('Announced'), findsOneWidget);
      expect(find.text('kids'), findsOneWidget);

      // Flip Monitored off, pick another profile, and wait for release.
      await tester.tap(find.text('Monitored'));
      await tester.pump();
      await tester.tap(find.text('Quality Profile'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Best'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Minimum Availability'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Released'));
      await tester.pumpAndSettle();

      await tester.tap(find.text('Update'));
      await tester.pumpAndSettle();

      final puts = ofMethod('PUT');
      expect(puts, hasLength(1));
      final body = puts.single.body as Map<String, dynamic>;
      expect(body['monitored'], false);
      expect(body['qualityProfileId'], 9);
      expect(body['minimumAvailability'], 'released');
      expect(body['path'], '/movies/Example Movie (2020)');
      expect(body['tags'], [2]);
      expect(
          body['someUnknownField'],
          {
            'nested': [1, 2]
          },
          reason: 'unmodelled fields must survive the edit round-trip');

      // The editor popped back to the harness and signalled a change.
      expect(find.text('open'), findsOneWidget);
      expect(changed, 1);
    });
  });

  group('RadarrMovieDetailScreen', () {
    Future<_FakeAdapter> pumpDetail(WidgetTester tester) async {
      final fake = _fakeDio();
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            authProvider.overrideWith(_FakeAuthNotifier.new),
            backendClientProvider.overrideWithValue(fake.dio),
          ],
          child: MaterialApp(
            home: RadarrMovieDetailScreen(
              instanceId: 'inst1',
              movie: _movie,
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      return fake.adapter;
    }

    testWidgets('app bar carries links, edit and the movie action menu',
        (tester) async {
      final adapter = await pumpDetail(tester);

      expect(find.byTooltip('External links'), findsOneWidget);
      expect(find.byTooltip('Edit movie'), findsOneWidget);

      // Links sheet lists the sites derivable from the movie's ids, the same
      // set (and URL forms) as the requester title page.
      await tester.tap(find.byTooltip('External links'));
      await tester.pumpAndSettle();
      for (final site in ['IMDb', 'TMDB', 'Trakt']) {
        expect(find.text(site), findsOneWidget);
      }
      expect(find.text('TheTVDB'), findsNothing);
      await tester.tapAt(const Offset(10, 10)); // dismiss without launching
      await tester.pumpAndSettle();

      // The overflow opens the movie action sheet.
      await tester.tap(find.byTooltip('Movie actions'));
      await tester.pumpAndSettle();
      expect(find.text('Search Movie'), findsOneWidget);
      await tester.tap(find.text('Search Movie'));
      await tester.pumpAndSettle();
      final posts = adapter.requests.where((r) => r.method == 'POST').toList();
      expect(posts.single.body, {
        'name': 'MoviesSearch',
        'movieIds': [7]
      });
    });

    testWidgets('Edit movie from the app bar opens the editor', (tester) async {
      await pumpDetail(tester);
      await tester.tap(find.byTooltip('Edit movie'));
      await tester.pumpAndSettle();
      expect(find.text('Edit Movie'), findsOneWidget);
      expect(find.text('Minimum Availability'), findsOneWidget);
    });
  });
}
