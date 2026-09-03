import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:cantinarr/features/discover/ui/browse_grid_screen.dart';
import 'package:cantinarr/features/discover/ui/filter_sheet.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// The Browse grid's language, streaming, keyword, and studio filters: the
/// lists it loads for the sheet, the region it starts from, and how an empty
/// grid names what it applied.
void main() {
  testWidgets(
      'an empty Browse names every filter group it applied, in the link\'s region',
      (tester) async {
    final adapter = _Adapter(discoverIds: const []);
    await _pumpGrid(
      tester,
      adapter,
      '/browse/movie/discover?lang=ko&prov=8&region=GB&kw=1&co=2',
    );

    expect(find.text('Nothing matched'), findsOneWidget);
    expect(
      find.text('No titles matched in Korean · on Netflix · about keyword 1 · from studio 2.'),
      findsOneWidget,
    );
    expect(find.text('Filters (4)'), findsOneWidget);
    // The services were looked up for the link's region, not the device's.
    expect(adapter.providerRequests.first.queryParameters['region'], 'GB');
    final discover = adapter.discoverRequests.last.queryParameters;
    expect(discover['with_original_language'], 'ko');
    expect(discover['with_watch_providers'], '8');
    expect(discover['watch_region'], 'GB');
    expect(discover['with_keywords'], '1');
    expect(discover['with_companies'], '2');
  });

  testWidgets(
      'the streaming region defaults to the device country and rides with a chosen service',
      (tester) async {
    tester.platformDispatcher.localeTestValue = const Locale('en', 'GB');
    addTearDown(tester.platformDispatcher.clearLocaleTestValue);
    final adapter = _Adapter(discoverIds: const [301]);
    await _pumpGrid(tester, adapter, '/browse/movie/discover');

    expect(adapter.providerRequests.first.queryParameters['region'], 'GB');

    await tester.tap(find.text('Filters'));
    await tester.pumpAndSettle();
    expect(find.byType(FilterSheet), findsOneWidget);
    final skyGo = find.widgetWithText(FilterChip, 'Sky Go');
    await tester.ensureVisible(skyGo);
    await tester.pumpAndSettle();
    await tester.tap(skyGo);
    await tester.ensureVisible(find.text('Apply'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Apply'));
    await tester.pumpAndSettle();

    expect(find.text('Filters (1)'), findsOneWidget);
    final discover = adapter.discoverRequests.last.queryParameters;
    expect(discover['with_watch_providers'], '39');
    expect(discover['watch_region'], 'GB');
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

// No library instances: the grid has no badges to fetch, which keeps the
// test on the filter lists alone.
const _state = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
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

Map<String, dynamic> _provider(int id, String name, int priority) => {
      'provider_id': id,
      'provider_name': name,
      'logo_path': null,
      'display_priority': priority,
    };

class _Adapter implements HttpClientAdapter {
  _Adapter({required this.discoverIds});

  final List<int> discoverIds;
  final List<Uri> discoverRequests = [];
  final List<Uri> providerRequests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    Object body;
    switch (options.path) {
      case '/api/discover/movies':
        discoverRequests.add(options.uri);
        body = {
          'page': 1,
          'results': [for (final id in discoverIds) _movie(id)],
          'total_pages': discoverIds.isEmpty ? 0 : 1,
          'total_results': discoverIds.length,
        };
      case '/api/genres/movie':
        body = {
          'genres': [
            {'id': 28, 'name': 'Action'},
          ],
        };
      case '/api/languages':
        body = [
          {'iso_639_1': 'en', 'english_name': 'English', 'name': 'English'},
          {'iso_639_1': 'ko', 'english_name': 'Korean', 'name': '한국어'},
        ];
      case '/api/providers/regions':
        body = {
          'results': [
            {'iso_3166_1': 'US', 'english_name': 'United States'},
            {'iso_3166_1': 'GB', 'english_name': 'United Kingdom'},
          ],
        };
      case '/api/providers/movie':
        providerRequests.add(options.uri);
        final region = options.uri.queryParameters['region'];
        body = {
          'results': [
            _provider(8, 'Netflix', 1),
            if (region == 'GB') _provider(39, 'Sky Go', 2),
          ],
        };
      case '/api/search/keyword':
        body = {
          'results': [
            {'id': 1, 'name': 'heist'},
          ],
        };
      case '/api/search/company':
        body = {
          'results': [
            {'id': 2, 'name': 'A24'},
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
