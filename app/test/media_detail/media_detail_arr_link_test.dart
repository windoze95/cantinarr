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
    bool? instanceMediaDownloads,
    bool mappedSibling = false,
    MediaType mediaType = MediaType.movie,
    List<Map<String, dynamic>> sonarrSeries = const [],
    List<Map<String, dynamic>> sonarrEpisodes = const [],
    Map<String, dynamic>? tvMatch,
    bool? tvCorrections,
    Size viewport = const Size(390, 844),
    double textScale = 1,
  }) async {
    tester.view.physicalSize = viewport;
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
              _state(
                isAdmin,
                mediaDownloads,
                mediaType,
                instanceMediaDownloads: instanceMediaDownloads,
                mappedSibling: mappedSibling,
                tvCorrections: tvCorrections ?? tvMatch != null,
              ),
            ),
          ),
          backendClientProvider.overrideWithValue(_fakeDio(
            radarrMovies,
            sonarrSeries: sonarrSeries,
            sonarrEpisodes: sonarrEpisodes,
            tvMatch: tvMatch,
          )),
          realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(
          routerConfig: router,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(textScale)),
            child: child!,
          ),
        ),
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

  testWidgets('a mapped sibling does not enable downloads on this instance',
      (tester) async {
    await pumpDetail(
      tester,
      isAdmin: false,
      mediaDownloads: true,
      instanceMediaDownloads: false,
      mappedSibling: true,
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

    expect(find.text('Download movie'), findsNothing);
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
  testWidgets('corrected TV links and downloads use only the server target, with source season labels', (tester) async {
    await pumpDetail(tester, isAdmin: true, mediaDownloads: true,
      mediaType: MediaType.tv, radarrMovies: [],
      tvMatch: {'tmdb_id': _tmdbId, 'tvdb_id': 389492, 'series_id': 7,
        'title': 'The Show', 'target_title': 'Monster (2022)', 'provenance': 'bundled',
        'revision': 'v1', 'state': 'resolved', 'season_map': {'1': 4}},
      sonarrSeries: [
        {'id': 8, 'title': 'The Show', 'tvdbId': 81189},
        {'id': 7, 'title': 'Monster (2022)', 'tvdbId': 389492},
      ],
      sonarrEpisodes: [for (final n in [1, 4]) {
        'id': 70 + n, 'seriesId': 7, 'seasonNumber': n, 'episodeNumber': 1,
        'title': n == 1 ? 'Dahmer file' : 'Lizzie file', 'hasFile': true, 'episodeFileId': 100 + n,
        'episodeFile': {'id': 100 + n, 'seriesId': 7, 'seasonNumber': n, 'size': 100},
      }, {
        'id': 79, 'seriesId': 7, 'seasonNumber': 4, 'episodeNumber': 2,
        'title': 'Lizzie second file', 'hasFile': true, 'episodeFileId': 109,
        'episodeFile': {'id': 109, 'seriesId': 7, 'seasonNumber': 4, 'size': 100},
      }]);
    expect(find.text('Open in Sonarr'), findsOneWidget);
    expect(find.text('Correct TV match'), findsNothing);
    expect(find.byTooltip('More options'), findsNothing);
    expect(find.byTooltip('Download Season 4 episodes'), findsNothing);
    await tester.ensureVisible(find.byTooltip('Download Season 1 episodes'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Download Season 1 episodes'));
    await tester.pumpAndSettle();
    expect(find.text('S01E01 · Lizzie file'), findsOneWidget);
    expect(find.textContaining('Dahmer file'), findsNothing);
  });

  testWidgets('unrequested TV details have no correction button or header menu', (tester) async {
    await pumpDetail(tester, isAdmin: true, mediaType: MediaType.tv,
      radarrMovies: [], tvCorrections: true, viewport: const Size(320, 640), textScale: 2);
    expect(find.text('Correct TV match'), findsNothing);
    expect(find.byTooltip('More options'), findsNothing);
    expect(find.text('Report a problem'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final scenario in [
    (name: 'requesters', admin: false, corrections: true, type: MediaType.tv),
    (name: 'older servers', admin: true, corrections: false, type: MediaType.tv),
    (name: 'movie details', admin: true, corrections: true, type: MediaType.movie),
  ]) {
    testWidgets('correction menu stays hidden for ${scenario.name}', (tester) async {
      await pumpDetail(tester, isAdmin: scenario.admin, mediaType: scenario.type,
        radarrMovies: [], tvCorrections: scenario.corrections);
      expect(find.byTooltip('More options'), findsNothing);
      expect(find.text('Correct TV match'), findsNothing);
    });
  }
}

AuthState _state(
  bool isAdmin,
  bool mediaDownloads,
  MediaType mediaType,
  {
  bool? instanceMediaDownloads,
  bool mappedSibling = false,
  bool tvCorrections = false,
}) =>
    AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        tvMatchCorrections: tvCorrections,
        services: AvailableServices(mediaDownloads: mediaDownloads),
        instances: mediaType == MediaType.movie
            ? [
                ServiceInstance(
                  id: 'radarr-main',
                  serviceType: 'radarr',
                  name: 'Main Radarr',
                  isDefault: true,
                  mediaDownloads: instanceMediaDownloads,
                ),
                if (mappedSibling)
                  const ServiceInstance(
                    id: 'radarr-4k',
                    serviceType: 'radarr',
                    name: '4K Radarr',
                    mediaDownloads: true,
                  ),
              ]
            : [
                ServiceInstance(
                  id: 'sonarr-main',
                  serviceType: 'sonarr',
                  name: 'Main Sonarr',
                  isDefault: true,
                  mediaDownloads: instanceMediaDownloads,
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
  Map<String, dynamic>? tvMatch,
}) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter(
    radarrMovies,
    sonarrSeries: sonarrSeries,
    sonarrEpisodes: sonarrEpisodes,
    tvMatch: tvMatch,
  );
  return dio;
}

/// Minimal backend stub: the TMDB detail load, the request-status check, and
/// the Radarr library listing the "Open in Radarr" resolution depends on.
class _JsonAdapter implements HttpClientAdapter {
  final List<Map<String, dynamic>> radarrMovies;
  final List<Map<String, dynamic>> sonarrSeries;
  final List<Map<String, dynamic>> sonarrEpisodes;
  final Map<String, dynamic>? tvMatch;

  _JsonAdapter(
    this.radarrMovies, {
    this.sonarrSeries = const [],
    this.sonarrEpisodes = const [],
    this.tvMatch,
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
      body = {'status': 'unavailable', 'seasons': <dynamic>[],
        if (tvMatch != null) 'match': tvMatch, 'status_known': true};
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
