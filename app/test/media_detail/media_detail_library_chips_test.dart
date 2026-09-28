import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const _tmdbId = 603;

/// The detail dock's per-library status chips: a user granted two libraries
/// sees one chip per library labeled with that library's own status, and
/// tapping a sibling retargets the status read at that library.
void main() {
  late _StatusAdapter adapter;

  Future<void> pumpDetail(WidgetTester tester, {AuthState? auth, MediaType mediaType = MediaType.movie}) async {
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
            mediaType: mediaType,
          ),
        ),
      ],
    );

    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(auth ?? _twoLibraryState())),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('two granted libraries render one status chip each',
      (tester) async {
    adapter = _StatusAdapter();
    await pumpDetail(tester);

    expect(find.text('Movies · Available'), findsOneWidget);
    expect(find.text('4K Movies · Not Available'), findsOneWidget);
  });

  testWidgets('tapping a sibling chip retargets the status read at it',
      (tester) async {
    adapter = _StatusAdapter();
    await pumpDetail(tester);

    await tester.tap(find.text('4K Movies · Not Available'));
    await tester.pumpAndSettle();

    expect(adapter.statusInstanceIds.last, 'radarr-4k');
    // The headline follows the selected library: absent from 4K, the title
    // offers Request again.
    expect(find.widgetWithText(ElevatedButton, 'Request'), findsOneWidget);
  });

  testWidgets('a single-status payload renders no chips', (tester) async {
    adapter = _StatusAdapter(instanceStatuses: const {});
    await pumpDetail(tester);

    expect(find.textContaining('Movies ·'), findsNothing);
  });

  for (final type in [MediaType.movie, MediaType.tv]) {
    for (final admin in [false, true]) {
      testWidgets('${type.name} confirms personal default instead of browsed instance, admin=$admin', (tester) async {
        adapter = _StatusAdapter(unavailable: true);
        final service = type == MediaType.movie ? 'radarr' : 'sonarr';
        await pumpDetail(tester, mediaType: type, auth: AuthState(
          user: UserProfile(id: 1, username: 'viewer', role: admin ? 'admin' : 'user'),
          connection: BackendConnection(serverUrl: 'http://localhost', accessToken: 'access', refreshToken: 'refresh',
            instances: [
              ServiceInstance(id: 'radarr-main', serviceType: service, name: 'Browsing', isDefault: true, assigned: true, requestDefault: false),
              ServiceInstance(id: 'radarr-4k', serviceType: service, name: 'Personal default', assigned: true, requestDefault: true),
              if (admin) ServiceInstance(id: 'admin-only', serviceType: service, name: 'Admin navigation', assigned: false, requestDefault: false),
            ]),
        ));
        await tester.tap(find.widgetWithText(ElevatedButton, 'Request'));
        await tester.pumpAndSettle();
        expect(adapter.posts, isEmpty);
        expect(tester.widget<ChoiceChip>(find.widgetWithText(ChoiceChip, 'Personal default')).selected, isTrue);
        expect(find.widgetWithText(ChoiceChip, 'Admin navigation'), findsNothing);
        await tester.tap(find.text('Cancel'));
        await tester.pumpAndSettle();
        expect(adapter.posts, isEmpty);
        await tester.tap(find.widgetWithText(ElevatedButton, 'Request'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('Request').last);
        await tester.pumpAndSettle();
        expect(adapter.posts.single['instance_id'], 'radarr-4k');
        expect(adapter.posts.single['media_type'], type.name);
        expect(adapter.posts.single['tmdb_id'], _tmdbId);
        await tester.pumpWidget(const SizedBox.shrink());
      });
    }
  }
}

AuthState _twoLibraryState() => const AuthState(
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
          ServiceInstance(
            id: 'radarr-4k',
            serviceType: 'radarr',
            name: '4K Movies',
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

/// Serves the TMDB detail plus a status whose headline depends on the
/// requested library: available on the default, unavailable on the 4K
/// sibling. Records each status call's instance_id (null for none).
class _StatusAdapter implements HttpClientAdapter {
  final Map<String, Map<String, String>> instanceStatuses;
  final List<String?> statusInstanceIds = [];
  final posts = <Map<String, dynamic>>[];
  final bool unavailable;

  _StatusAdapter({
    this.unavailable = false,
    this.instanceStatuses = const {
      'radarr-main': {'status': 'available'},
      'radarr-4k': {'status': 'unavailable'},
    },
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object body;
    if (options.method == 'POST' && path == '/api/requests') {
      posts.add(Map<String, dynamic>.from(options.data as Map));
      body = {'status': 'requested'};
    } else if (path == '/api/requests/options') {
      body = {'can_choose_season': false, 'can_choose_quality': false, 'default_season_scope': 'all', 'quality_profiles': <dynamic>[]};
    } else if (path.endsWith('/status')) {
      final instanceId = options.queryParameters['instance_id'] as String?;
      statusInstanceIds.add(instanceId);
      body = {
        'status': unavailable || instanceId == 'radarr-4k' ? 'unavailable' : 'available',
        'seasons': <dynamic>[],
        if (instanceStatuses.isNotEmpty) 'instance_statuses': instanceStatuses,
      };
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      body = {'results': <dynamic>[]};
    } else if (path.contains('/api/media/movie/')) {
      body = {'id': _tmdbId, 'title': 'The Matrix'};
    } else if (path.contains('/api/media/tv/')) {
      body = {'id': _tmdbId, 'name': 'Test show', 'seasons': <dynamic>[]};
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
