import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

// A movie whose TMDB id we render the detail for.
const _tmdbId = 603;

void main() {
  Future<void> pumpDetail(
    WidgetTester tester, {
    required bool isAdmin,
    required List<Map<String, dynamic>> radarrMovies,
    bool mediaDownloads = false,
    MediaType mediaType = MediaType.movie,
    List<Map<String, dynamic>> sonarrSeries = const [],
    List<Map<String, dynamic>> sonarrEpisodes = const [],
  }) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/detail/${mediaType.name}/$_tmdbId',
      routes: [
        GoRoute(
          path: '/detail/:type/:id',
          builder: (_, state) => MediaDetailScreen(
            id: int.parse(state.pathParameters['id']!),
            mediaType: state.pathParameters['type'] == 'tv'
                ? MediaType.tv
                : MediaType.movie,
          ),
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(
              _state(isAdmin, mediaDownloads, mediaType),
            ),
          ),
          backendClientProvider.overrideWithValue(_fakeDio(
            radarrMovies,
            sonarrSeries: sonarrSeries,
            sonarrEpisodes: sonarrEpisodes,
          )),
          realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('admin sees "Open in Radarr" when the movie is in the library',
      (tester) async {
    await pumpDetail(
      tester,
      isAdmin: true,
      radarrMovies: [
        {'id': 5, 'title': 'The Matrix', 'year': 1999, 'tmdbId': _tmdbId},
      ],
    );

    expect(find.text('Open in Radarr'), findsOneWidget);
  });

  testWidgets('admin sees no link when the movie is not in the library',
      (tester) async {
    await pumpDetail(
      tester,
      isAdmin: true,
      // A different movie — no tmdbId match.
      radarrMovies: [
        {'id': 5, 'title': 'Some Other Film', 'year': 2010, 'tmdbId': 111},
      ],
    );

    expect(find.text('Open in Radarr'), findsNothing);
  });

  testWidgets('non-admin never sees the link even when the movie is present',
      (tester) async {
    await pumpDetail(
      tester,
      isAdmin: false,
      radarrMovies: [
        {'id': 5, 'title': 'The Matrix', 'year': 1999, 'tmdbId': _tmdbId},
      ],
    );

    expect(find.text('Open in Radarr'), findsNothing);
  });

  testWidgets('non-admin can download an exact live movie file when enabled',
      (tester) async {
    await pumpDetail(
      tester,
      isAdmin: false,
      mediaDownloads: true,
      radarrMovies: [
        {
          'id': 5,
          'title': 'The Matrix',
          'year': 1999,
          'tmdbId': _tmdbId,
          'hasFile': true,
          'movieFile': {
            'id': 42,
            'relativePath': 'The Matrix.mkv',
            'size': 100,
          },
        },
      ],
    );

    expect(find.text('Download movie'), findsOneWidget);
    expect(find.text('Open in Radarr'), findsNothing);
  });

  testWidgets('TV download opens individual exact episode choices',
      (tester) async {
    await pumpDetail(
      tester,
      isAdmin: false,
      mediaDownloads: true,
      mediaType: MediaType.tv,
      radarrMovies: const [],
      sonarrSeries: const [
        {'id': 7, 'title': 'The Show', 'tvdbId': 81189},
      ],
      sonarrEpisodes: const [
        {
          'id': 71,
          'seriesId': 7,
          'seasonNumber': 1,
          'episodeNumber': 1,
          'title': 'Pilot',
          'hasFile': true,
          'episodeFileId': 101,
          'episodeFile': {
            'id': 101,
            'seriesId': 7,
            'seasonNumber': 1,
            'size': 100,
          },
        },
        {
          'id': 72,
          'seriesId': 7,
          'seasonNumber': 1,
          'episodeNumber': 2,
          'title': 'Second',
          'hasFile': true,
          'episodeFileId': 102,
          'episodeFile': {
            'id': 102,
            'seriesId': 7,
            'seasonNumber': 1,
            'size': 200,
          },
        },
      ],
    );

    expect(find.byTooltip('Download Season 1 episodes'), findsOneWidget);
    await tester.tap(find.byTooltip('Download Season 1 episodes'));
    await tester.pumpAndSettle();
    expect(find.text('S01E01 · Pilot'), findsOneWidget);
    expect(find.text('S01E02 · Second'), findsOneWidget);
  });
}

AuthState _state(
  bool isAdmin,
  bool mediaDownloads,
  MediaType mediaType,
) =>
    AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(mediaDownloads: mediaDownloads),
        instances: mediaType == MediaType.movie
            ? const [
                ServiceInstance(
                  id: 'radarr-main',
                  serviceType: 'radarr',
                  name: 'Main Radarr',
                  isDefault: true,
                ),
              ]
            : const [
                ServiceInstance(
                  id: 'sonarr-main',
                  serviceType: 'sonarr',
                  name: 'Main Sonarr',
                  isDefault: true,
                ),
              ],
      ),
      user: UserProfile(
        id: 1,
        username: isAdmin ? 'admin' : 'viewer',
        role: isAdmin ? 'admin' : 'user',
      ),
    );

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

Dio _fakeDio(
  List<Map<String, dynamic>> radarrMovies, {
  List<Map<String, dynamic>> sonarrSeries = const [],
  List<Map<String, dynamic>> sonarrEpisodes = const [],
}) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter(
    radarrMovies,
    sonarrSeries: sonarrSeries,
    sonarrEpisodes: sonarrEpisodes,
  );
  return dio;
}

/// Minimal backend stub: the TMDB detail load, the request-status check, and
/// the Radarr library listing the "Open in Radarr" resolution depends on.
class _JsonAdapter implements HttpClientAdapter {
  final List<Map<String, dynamic>> radarrMovies;
  final List<Map<String, dynamic>> sonarrSeries;
  final List<Map<String, dynamic>> sonarrEpisodes;

  _JsonAdapter(
    this.radarrMovies, {
    this.sonarrSeries = const [],
    this.sonarrEpisodes = const [],
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object body;
    if (path.endsWith('/api/v3/series')) {
      body = sonarrSeries;
    } else if (path.endsWith('/api/v3/episode')) {
      body = sonarrEpisodes;
    } else if (path.contains('/api/v3/movie/')) {
      body = radarrMovies.firstWhere(
        (movie) => path.endsWith('/${movie['id']}'),
      );
    } else if (path.contains('/api/v3/movie')) {
      body = radarrMovies; // Radarr library listing.
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      body = {'results': <dynamic>[]};
    } else if (path.endsWith('/status')) {
      body = {'status': 'unavailable', 'seasons': <dynamic>[]};
    } else if (path.contains('/api/media/tv/')) {
      body = {
        'id': _tmdbId,
        'name': 'The Show',
        'external_ids': {'tvdb_id': 81189},
        'seasons': [
          {
            'id': 7001,
            'season_number': 1,
            'name': 'Season 1',
            'episode_count': 2,
          },
        ],
      };
    } else if (path.contains('/api/media/movie/')) {
      body = {'id': _tmdbId, 'title': 'The Matrix'};
    } else {
      body = <dynamic>[];
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
