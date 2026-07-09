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
  }) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/detail/movie/$_tmdbId',
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
          authProvider.overrideWith(() => _FakeAuthNotifier(_state(isAdmin))),
          backendClientProvider.overrideWithValue(_fakeDio(radarrMovies)),
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
}

AuthState _state(bool isAdmin) => AuthState(
      connection: const BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        instances: [
          ServiceInstance(
            id: 'radarr-main',
            serviceType: 'radarr',
            name: 'Main Radarr',
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

Dio _fakeDio(List<Map<String, dynamic>> radarrMovies) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter(radarrMovies);
  return dio;
}

/// Minimal backend stub: the TMDB detail load, the request-status check, and
/// the Radarr library listing the "Open in Radarr" resolution depends on.
class _JsonAdapter implements HttpClientAdapter {
  final List<Map<String, dynamic>> radarrMovies;

  _JsonAdapter(this.radarrMovies);

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object body;
    if (path.contains('/api/v3/movie')) {
      body = radarrMovies; // Radarr library listing.
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      body = {'results': <dynamic>[]};
    } else if (path.endsWith('/status')) {
      body = {'status': 'unavailable', 'seasons': <dynamic>[]};
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
