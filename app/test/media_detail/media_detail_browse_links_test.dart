import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/widgets/see_all_button.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const _tmdbId = 603;

/// The doors a title page opens into the browse grid: a genre chip browses
/// that genre, and the Recommended and Similar rows continue past their
/// first page.
void main() {
  testWidgets('a genre chip opens the Browse grid for that genre',
      (tester) async {
    final (:opened, :router) = await _pumpDetail(tester);

    await _tap(tester, find.widgetWithText(ActionChip, 'Action'));
    expect(opened.last.path, '/browse/movie/discover');
    expect(opened.last.queryParameters['genres'], '28');
    expect(opened.last.queryParameters['title'], 'Action');
    expect(opened.last.queryParameters.containsKey('id'), isFalse);
    router.pop();
  });

  testWidgets('Recommended and Similar continue as grids anchored on the title',
      (tester) async {
    final (:opened, :router) = await _pumpDetail(tester);

    await _tap(tester, _seeAllFor('Recommended'));
    expect(opened.last.path, '/browse/movie/recommendations');
    expect(opened.last.queryParameters['id'], '$_tmdbId');
    expect(opened.last.queryParameters['title'], 'Recommended');

    router.pop();
    await tester.pumpAndSettle();
    await _tap(tester, _seeAllFor('Similar'));
    expect(opened.last.path, '/browse/movie/similar');
    expect(opened.last.queryParameters['id'], '$_tmdbId');
  });
}

Finder _seeAllFor(String rowTitle) => find.byWidgetPredicate(
      (w) => w is SeeAllButton && w.rowTitle == rowTitle,
    );

/// Scrolls [finder] into view, lets the scroll finish, then taps it.
Future<void> _tap(WidgetTester tester, Finder finder) async {
  await tester.ensureVisible(finder);
  await tester.pumpAndSettle();
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

Future<({List<Uri> opened, GoRouter router})> _pumpDetail(
  WidgetTester tester,
) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final opened = <Uri>[];
  final router = GoRouter(
    initialLocation: '/detail/movie/$_tmdbId',
    routes: [
      GoRoute(
        path: '/detail/:type/:id',
        builder: (_, state) => MediaDetailScreen(
          id: int.parse(state.pathParameters['id']!),
          mediaType: MediaType.movie,
        ),
      ),
      GoRoute(
        path: '/browse/:type/:feed',
        builder: (_, state) {
          opened.add(state.uri);
          return const Scaffold(body: Text('grid'));
        },
      ),
    ],
  );

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _DetailAdapter();

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_state)),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return (opened: opened, router: router);
}

const _state = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
    instances: [
      ServiceInstance(
        id: 'radarr-main',
        serviceType: 'radarr',
        name: 'Movies',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'viewer', role: 'user'),
);

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

/// A movie with one genre and one title in each of its rows.
class _DetailAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object body;
    if (path.endsWith('/status')) {
      body = {'status': 'available', 'seasons': <dynamic>[]};
    } else if (path.endsWith('/recommendations')) {
      body = {
        'results': [
          {'id': 604, 'title': 'The Matrix Reloaded', 'poster_path': null},
        ],
      };
    } else if (path.endsWith('/similar')) {
      body = {
        'results': [
          {'id': 605, 'title': 'The Matrix Revolutions', 'poster_path': null},
        ],
      };
    } else if (path.contains('/api/media/movie/')) {
      body = {
        'id': _tmdbId,
        'title': 'The Matrix',
        'genres': [
          {'id': 28, 'name': 'Action'},
        ],
      };
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
