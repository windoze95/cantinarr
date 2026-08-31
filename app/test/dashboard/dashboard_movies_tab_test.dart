import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/featured_media_hero.dart';
import 'package:cantinarr/core/widgets/horizontal_item_row.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_movies_tab.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Covers the wiring dashboard_tv_tab_test.dart established for TV: that the
/// Movies tab's existing Radarr fetch also feeds Available/Requested badges
/// onto Discover browse-row posters, with no second Radarr call and no badge
/// on the featured hero.
void main() {
  testWidgets(
      'Movies Discover browse rows badge Available/Requested from the Radarr library',
      (tester) async {
    final adapter = _RadarrAdapter(
      movies: [
        _movie(id: 1, title: 'Hasfile Movie', tmdbId: 101, hasFile: true),
        _movie(id: 2, title: 'Requested Movie', tmdbId: 102, hasFile: false),
        _movie(
          id: 3,
          title: 'Unmonitored Movie',
          tmdbId: 103,
          monitored: false,
          hasFile: false,
        ),
      ],
      featured: [
        _featured(id: 100, title: 'Hero Movie'),
        _featured(id: 101, title: 'Hasfile Movie'),
        _featured(id: 102, title: 'Requested Movie'),
        _featured(id: 103, title: 'Unmonitored Movie'),
        _featured(id: 104, title: 'No Match Movie'),
      ],
    );

    await _pumpMoviesTab(tester, adapter);

    // Scope to the Discover browse row specifically: the dashboard's own
    // Radarr library rows ("Downloading Soon" / "Recently Downloaded") reuse
    // the same movie titles with their own, already-correct badges, and would
    // otherwise collide with the browse-row assertions below.
    final browseRowCards = _browseRowCards(tester);
    final byTitle = {for (final c in browseRowCards) c.title: c};

    expect(byTitle['Hasfile Movie']?.statusLabel, 'Available');
    expect(byTitle['Hasfile Movie']?.statusColor, AppTheme.available);

    expect(byTitle['Requested Movie']?.statusLabel, 'Requested');
    expect(byTitle['Requested Movie']?.statusColor, AppTheme.requested);

    expect(byTitle['Unmonitored Movie']?.statusLabel, isNull);
    expect(byTitle['No Match Movie']?.statusLabel, isNull);

    // D-01: the featured hero never carries a badge — it isn't even rendered
    // through a MediaCard (it's a different widget entirely), so its title
    // can never appear among the browse row's badged cards.
    expect(find.byType(FeaturedMediaHero), findsOneWidget);
    expect(byTitle.containsKey('Hero Movie'), isFalse);
  });

  testWidgets(
      'browse-row posters still render, with no badge, when the Radarr fetch fails',
      (tester) async {
    final adapter = _RadarrAdapter(
      movies: const [],
      featured: [
        _featured(id: 100, title: 'Hero Movie'),
        _featured(id: 101, title: 'Unreachable Movie'),
      ],
      failMovies: true,
    );

    await _pumpMoviesTab(tester, adapter);

    // D-02: posters paint regardless of arr fetch state.
    final unreachable =
        _browseRowCards(tester).where((c) => c.title == 'Unreachable Movie');
    expect(unreachable, isNotEmpty);
    expect(unreachable.first.statusLabel, isNull);
  });
}

/// The MediaCards rendered by Discover browse rows (CategoryRow), as opposed
/// to the dashboard's own Radarr library rows — both use MediaCard, but only
/// the browse rows are this plan's concern, and both can carry the same movie
/// title with a different (correct, out-of-scope) badge.
List<MediaCard> _browseRowCards(WidgetTester tester) {
  final rows = find.byType(HorizontalItemRow<MediaItem>);
  final cards = <MediaCard>[];
  for (final element in tester.elementList(rows)) {
    cards.addAll(tester
        .widgetList<MediaCard>(find.descendant(
          of: find.byWidget(element.widget),
          matching: find.byType(MediaCard),
        ))
        .toList());
  }
  return cards;
}

Future<void> _pumpMoviesTab(WidgetTester tester, _RadarrAdapter adapter) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(_moviesState)),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  addTearDown(container.dispose);

  await container.read(authProvider.future);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: Scaffold(body: DashboardMoviesTab())),
    ),
  );
  await tester.pumpAndSettle();
}

const _moviesState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(radarr: true),
    instances: [
      ServiceInstance(
        id: 'movies',
        serviceType: 'radarr',
        name: 'Movies',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

Map<String, dynamic> _movie({
  required int id,
  required String title,
  required int tmdbId,
  bool monitored = true,
  bool hasFile = false,
}) =>
    {
      'id': id,
      'title': title,
      'tmdbId': tmdbId,
      'monitored': monitored,
      'hasFile': hasFile,
      // No images: a null poster keeps the cards off the network.
      'images': <Object>[],
    };

Map<String, dynamic> _featured({required int id, required String title}) => {
      'id': id,
      'title': title,
      'poster_path': null,
      'release_date': null,
      'vote_average': 0,
    };

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

class _RadarrAdapter implements HttpClientAdapter {
  _RadarrAdapter({
    required this.movies,
    required this.featured,
    this.failMovies = false,
  });

  final List<Map<String, dynamic>> movies;
  final List<Map<String, dynamic>> featured;
  final bool failMovies;

  static const _base = '/api/instances/movies/api/v3';

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    Object body;
    if (options.path == '$_base/movie') {
      if (failMovies) {
        return ResponseBody.fromString('{"error":"unavailable"}', 503,
            headers: {
              'content-type': ['application/json'],
            });
      }
      body = movies;
    } else if (options.path == '$_base/queue') {
      body = {'records': <Object>[], 'totalRecords': 0};
    } else if (options.path == '/api/discover/movies/featured') {
      body = {
        'source': 'tmdb_trending',
        'page': 1,
        'results': featured,
        'total_pages': 1,
        'total_results': featured.length,
      };
    } else {
      // Top Rated, Coming Soon, Most Anticipated: empty is enough, and every
      // fetch there is guarded.
      body = {
        'page': 1,
        'results': <Object>[],
        'total_pages': 0,
        'total_results': 0,
      };
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
