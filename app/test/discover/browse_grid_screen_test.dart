import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/error_banner.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:cantinarr/features/discover/ui/browse_grid_screen.dart';
import 'package:cantinarr/features/discover/ui/filter_sheet.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// The grid behind every "See all" and the Browse page: badges from the
/// shared library snapshot, one computed poster width, paging toward the
/// end, and honest empty and error states.
void main() {
  testWidgets('badges posters from the library and keeps loading to the end',
      (tester) async {
    final adapter = _Adapter(
      topRatedPages: {
        1: [201, 202, 203, 204],
        2: [204, 205, 206],
      },
    );
    await _pumpGrid(tester, adapter, '/browse/movie/top-rated');

    final cards = tester.widgetList<MediaCard>(find.byType(MediaCard)).toList();
    final byTitle = {for (final c in cards) c.title: c};
    expect(byTitle['Movie 201']?.statusLabel, 'Available');
    expect(byTitle['Movie 201']?.statusColor, AppTheme.available);
    expect(byTitle['Movie 202']?.statusLabel, 'Requested');
    expect(byTitle['Movie 203']?.statusLabel, isNull);

    // Four posters do not fill the viewport, so page 2 was asked for at
    // once, and the title both pages carry appears once.
    expect(adapter.topRatedPagesRequested, [1, 2]);
    expect(cards.where((c) => c.title == 'Movie 204'), hasLength(1));
    expect(cards, hasLength(6));
    // The library was read once, for the badges.
    expect(adapter.libraryReads, 1);
  });

  testWidgets('every cell shares one computed width and the row extent matches',
      (tester) async {
    final adapter = _Adapter(topRatedPages: {
      1: [201, 202, 203],
    });
    await _pumpGrid(tester, adapter, '/browse/movie/top-rated');

    // 390 wide, 16px gutters: 358 usable fits two 132px-minimum columns,
    // so each card is (358 - 14) / 2 wide.
    const cardWidth = (390.0 - 32 - 14) / 2;
    for (final card in tester.widgetList<MediaCard>(find.byType(MediaCard))) {
      expect(card.width, cardWidth);
    }
    final grid = tester.widget<SliverGrid>(find.byType(SliverGrid));
    final delegate =
        grid.gridDelegate as SliverGridDelegateWithFixedCrossAxisCount;
    expect(delegate.crossAxisCount, 2);
    expect(delegate.mainAxisExtent,
        cardWidth * 1.5 + MediaCard.plainRowExtraHeight);
  });

  testWidgets('sort and filters appear only on the Browse feed',
      (tester) async {
    final adapter = _Adapter(topRatedPages: {
      1: [201],
    });
    await _pumpGrid(tester, adapter, '/browse/movie/top-rated');
    expect(find.text('Filters'), findsNothing);
    expect(find.text('Most popular'), findsNothing);
  });

  testWidgets('the Browse feed filters through the sheet and says so',
      (tester) async {
    final adapter = _Adapter(discoverIds: [301, 302]);
    await _pumpGrid(tester, adapter, '/browse/movie/discover');
    expect(find.text('Browse Movies'), findsOneWidget);
    expect(find.text('Most popular'), findsOneWidget);

    await tester.tap(find.text('Filters'));
    await tester.pumpAndSettle();
    expect(find.byType(FilterSheet), findsOneWidget);

    await tester.tap(find.widgetWithText(FilterChip, 'Action'));
    await tester.tap(find.widgetWithText(FilterChip, '7+'));
    await tester.tap(find.text('Apply'));
    await tester.pumpAndSettle();

    expect(find.text('Filters (2)'), findsOneWidget);
    final last = adapter.discoverRequests.last.queryParameters;
    expect(last['with_genres'], '28');
    expect(last['vote_average.gte'], '7.0');
  });

  testWidgets('an empty Browse names what it looked for and can clear it',
      (tester) async {
    final adapter = _Adapter(discoverIds: const []);
    await _pumpGrid(
      tester,
      adapter,
      '/browse/movie/discover?genres=28&rating=7',
    );

    expect(find.text('Nothing matched'), findsOneWidget);
    expect(find.text('No titles matched Action · rated 7+.'), findsOneWidget);

    await tester.tap(find.text('Clear filters'));
    await tester.pumpAndSettle();
    expect(find.text('Filters'), findsOneWidget);
    expect(
      adapter.discoverRequests.last.queryParameters.containsKey('with_genres'),
      isFalse,
    );
  });

  testWidgets('an unreadable feed shows an error with retry, not an empty grid',
      (tester) async {
    final adapter = _Adapter(
      topRatedPages: {
        1: [201],
      },
      failTopRated: true,
    );
    await _pumpGrid(tester, adapter, '/browse/movie/top-rated');

    expect(find.byType(FullScreenError), findsOneWidget);
    expect(find.text('Nothing matched'), findsNothing);

    adapter.failTopRated = false;
    await tester.tap(find.text('Try Again'));
    await tester.pumpAndSettle();
    expect(find.byType(FullScreenError), findsNothing);
    expect(find.text('Movie 201'), findsOneWidget);
  });
}

Future<void> _pumpGrid(
  WidgetTester tester,
  _Adapter adapter,
  String location,
) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final router = GoRouter(
    initialLocation: location,
    routes: [
      GoRoute(
        path: '/browse/:type/:feed',
        builder: (_, state) =>
            BrowseGridScreen(query: BrowseQuery.tryParse(state.uri)!),
      ),
    ],
  );
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_state)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp.router(theme: AppTheme.dark, routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
}

const _state = AuthState(
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

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

Map<String, dynamic> _movie(int id) => {
      'id': id,
      'title': 'Movie $id',
      'poster_path': null,
      'release_date': null,
      'vote_average': 0,
    };

class _Adapter implements HttpClientAdapter {
  _Adapter({
    this.topRatedPages = const {},
    this.discoverIds = const [],
    this.failTopRated = false,
  });

  final Map<int, List<int>> topRatedPages;
  final List<int> discoverIds;
  bool failTopRated;

  final List<int> topRatedPagesRequested = [];
  final List<Uri> discoverRequests = [];
  int libraryReads = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final page = int.parse(options.uri.queryParameters['page'] ?? '1');
    Object body;
    switch (options.path) {
      case '/api/instances/movies/api/v3/movie':
        libraryReads++;
        body = [
          {
            'id': 1,
            'title': 'Movie 201',
            'tmdbId': 201,
            'monitored': true,
            'hasFile': true,
            'images': <Object>[],
          },
          {
            'id': 2,
            'title': 'Movie 202',
            'tmdbId': 202,
            'monitored': true,
            'hasFile': false,
            'images': <Object>[],
          },
        ];
      case '/api/discover/movies/top-rated':
        topRatedPagesRequested.add(page);
        if (failTopRated) {
          return ResponseBody.fromString('{"error":"down"}', 503, headers: {
            'content-type': ['application/json'],
          });
        }
        body = {
          'page': page,
          'results': [for (final id in topRatedPages[page] ?? <int>[]) _movie(id)],
          'total_pages': topRatedPages.length,
          'total_results': 0,
        };
      case '/api/discover/movies':
        discoverRequests.add(options.uri);
        body = {
          'page': page,
          'results': [for (final id in discoverIds) _movie(id)],
          'total_pages': discoverIds.isEmpty ? 0 : 1,
          'total_results': discoverIds.length,
        };
      case '/api/genres/movie':
        body = {
          'genres': [
            {'id': 28, 'name': 'Action'},
            {'id': 35, 'name': 'Comedy'},
          ],
        };
      default:
        body = {
          'page': 1,
          'results': <Object>[],
          'total_pages': 0,
          'total_results': 0,
        };
    }
    return ResponseBody.fromString(jsonEncode(body), 200, headers: {
      'content-type': ['application/json'],
    });
  }

  @override
  void close({bool force = false}) {}
}
