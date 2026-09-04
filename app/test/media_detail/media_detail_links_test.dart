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

/// The Links line of a title page's Details: IMDb, TMDB, and Trakt chips
/// that open the title's own page elsewhere, fed by the movie record's
/// `imdb_id` or a show's `external_ids`. Only known ids earn a chip.
void main() {
  testWidgets('a movie with an IMDb id offers IMDb, TMDB, and Trakt chips',
      (tester) async {
    await _pumpDetail(tester, type: MediaType.movie, body: {
      'id': 603,
      'title': 'The Matrix',
      'imdb_id': 'tt0133093',
    });

    expect(find.text('Details'), findsOneWidget);
    expect(find.text('Links'), findsOneWidget);
    for (final site in ['IMDb', 'TMDB', 'Trakt']) {
      expect(find.widgetWithText(ActionChip, site), findsOneWidget,
          reason: site);
    }
    expect(
      tester.getTopLeft(find.text('IMDb')).dx,
      lessThan(tester.getTopLeft(find.text('TMDB')).dx),
    );
  });

  testWidgets('a movie TMDB has no IMDb id for links only TMDB',
      (tester) async {
    await _pumpDetail(tester, type: MediaType.movie, body: {
      'id': 603,
      'title': 'The Matrix',
    });

    expect(find.text('Links'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'TMDB'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'IMDb'), findsNothing);
    expect(find.widgetWithText(ActionChip, 'Trakt'), findsNothing);
  });

  testWidgets('a show takes its IMDb id from external_ids', (tester) async {
    await _pumpDetail(tester, type: MediaType.tv, body: {
      'id': 1396,
      'name': 'Breaking Bad',
      'seasons': <dynamic>[],
      'external_ids': {'imdb_id': 'tt0903747', 'tvdb_id': 81189},
    });

    expect(find.text('Links'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'IMDb'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'TMDB'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Trakt'), findsOneWidget);
  });
}

Future<void> _pumpDetail(
  WidgetTester tester, {
  required MediaType type,
  required Map<String, dynamic> body,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final router = GoRouter(
    initialLocation: '/detail/${type.name}/${body['id']}',
    routes: [
      GoRoute(
        path: '/detail/:type/:id',
        builder: (_, state) => MediaDetailScreen(
          id: int.parse(state.pathParameters['id']!),
          mediaType: type,
        ),
      ),
    ],
  );

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _DetailAdapter(body);

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

/// Serves one title's detail body and empty rows for everything else the
/// page asks for.
class _DetailAdapter implements HttpClientAdapter {
  final Map<String, dynamic> body;

  _DetailAdapter(this.body);

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object response;
    if (path.endsWith('/status')) {
      response = {'status': 'available', 'seasons': <dynamic>[]};
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      response = {'results': <dynamic>[]};
    } else if (path.contains('/api/media/movie/') ||
        path.contains('/api/media/tv/')) {
      response = body;
    } else {
      response = <dynamic>[];
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
