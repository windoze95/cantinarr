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

const _tmdbId = 603;

/// The movie detail page's "Release dates" section: TMDB-backed cinema,
/// digital and disc dates for any movie, whether or not it is in a Radarr
/// library. Dates are far in the past or future so this suite never rots as
/// the wall clock moves.
void main() {
  Future<void> pumpDetail(
    WidgetTester tester, {
    required _DetailAdapter adapter,
    Locale locale = const Locale('en', 'US'),
  }) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    // The region comes from the device locale, not the widget tree: the
    // app resolves no locales, so a widget-level locale is always en_US.
    tester.platformDispatcher.localeTestValue = locale;
    addTearDown(tester.platformDispatcher.clearLocaleTestValue);

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
      ],
    );

    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_singleLibraryState())),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets(
      'US release dates render as three labeled, formatted milestone rows',
      (tester) async {
    await pumpDetail(
      tester,
      adapter: _DetailAdapter(releaseDates: {
        'results': [
          {
            'iso_3166_1': 'US',
            'release_dates': [
              {'type': 3, 'release_date': '1994-01-01T00:00:00.000Z'},
              {'type': 4, 'release_date': '1994-03-15T00:00:00.000Z'},
              {'type': 5, 'release_date': '1994-06-01T00:00:00.000Z'},
            ],
          },
        ],
      }),
    );

    expect(find.text('Release dates'), findsOneWidget);
    expect(find.text('US'), findsOneWidget);

    expect(find.text('In cinemas'), findsOneWidget);
    expect(find.text('Jan 1, 1994'), findsOneWidget);

    expect(find.text('Digital'), findsOneWidget);
    expect(find.text('Mar 15, 1994'), findsOneWidget);

    expect(find.text('Blu-ray / DVD'), findsOneWidget);
    expect(find.text('Jun 1, 1994'), findsOneWidget);
  });

  testWidgets('the device country is tried first when it has milestones',
      (tester) async {
    await pumpDetail(
      tester,
      locale: const Locale('en', 'GB'),
      adapter: _DetailAdapter(releaseDates: {
        'results': [
          {
            'iso_3166_1': 'US',
            'release_dates': [
              {'type': 3, 'release_date': '2094-02-01T00:00:00.000Z'},
            ],
          },
          {
            'iso_3166_1': 'GB',
            'release_dates': [
              {'type': 4, 'release_date': '2094-03-01T00:00:00.000Z'},
            ],
          },
        ],
      }),
    );

    expect(find.text('Release dates'), findsOneWidget);
    expect(find.text('GB'), findsOneWidget);
    expect(find.text('US'), findsNothing);
    expect(find.text('Digital'), findsOneWidget);
    expect(find.text('Mar 1, 2094'), findsOneWidget);
    expect(find.text('In cinemas'), findsNothing);
  });

  testWidgets(
      'a Premiere-only preferred region is skipped in favor of a region with '
      'real milestones', (tester) async {
    await pumpDetail(
      tester,
      locale: const Locale('en', 'GB'),
      adapter: _DetailAdapter(releaseDates: {
        'results': [
          {
            'iso_3166_1': 'GB',
            'release_dates': [
              {'type': 1, 'release_date': '2094-01-01T00:00:00.000Z'},
            ],
          },
          {
            'iso_3166_1': 'US',
            'release_dates': [
              {'type': 3, 'release_date': '2094-02-01T00:00:00.000Z'},
            ],
          },
        ],
      }),
    );

    // GB contributes nothing (only a Premiere), so the US region — the next
    // candidate in resolution order — is the one shown.
    expect(find.text('Release dates'), findsOneWidget);
    expect(find.text('US'), findsOneWidget);
    expect(find.text('GB'), findsNothing);
    expect(find.text('In cinemas'), findsOneWidget);
    expect(find.text('Feb 1, 2094'), findsOneWidget);
  });

  testWidgets('no release_dates key means the section is absent entirely',
      (tester) async {
    await pumpDetail(tester, adapter: _DetailAdapter(releaseDates: null));

    expect(find.text('Release dates'), findsNothing);
  });
}

AuthState _singleLibraryState() => const AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
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
  final AuthState authState;
  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

/// Serves a TMDB movie detail body, optionally carrying a `release_dates`
/// append. Passing null omits the key entirely, matching a server that has
/// not yet added the append (or is serving a cached pre-change body).
class _DetailAdapter implements HttpClientAdapter {
  final Map<String, dynamic>? releaseDates;

  _DetailAdapter({required this.releaseDates});

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
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      body = {'results': <dynamic>[]};
    } else if (path.contains('/api/media/movie/')) {
      body = {
        'id': _tmdbId,
        'title': 'The Matrix',
        if (releaseDates != null) 'release_dates': releaseDates,
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
