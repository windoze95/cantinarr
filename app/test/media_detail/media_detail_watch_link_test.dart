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

const _tmdbId = 10378;

const _jellyfin = ServiceInstance(
  id: 'jf-a',
  serviceType: 'jellyfin',
  name: 'Home Jellyfin',
);
const _emby = ServiceInstance(
  id: 'em-a',
  serviceType: 'emby',
  name: 'Den Emby',
);
const _plex = ServiceInstance(
  id: 'px-a',
  serviceType: 'plex',
  name: 'Cantina Plex',
);

Map<String, dynamic> _link(
  ServiceInstance server,
  String state, {
  String url = '',
}) =>
    {
      'instance_id': server.id,
      'service_type': server.serviceType,
      'name': server.name,
      'state': state,
      if (url.isNotEmpty) 'url': url,
    };

void main() {
  Future<_JsonAdapter> pumpDetail(
    WidgetTester tester, {
    required String status,
    required List<Map<String, dynamic>> links,
    List<ServiceInstance> mediaServers = const [_jellyfin],
    MediaType mediaType = MediaType.movie,
    int watchStatus = 200,
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
    final adapter = _JsonAdapter(
      status: status,
      links: links,
      watchStatus: watchStatus,
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_state(mediaServers, mediaType)),
          ),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();
    return adapter;
  }

  testWidgets('an available movie the server holds gets a Watch button',
      (tester) async {
    final adapter = await pumpDetail(
      tester,
      status: 'available',
      links: [
        _link(_jellyfin, 'found',
            url: 'https://jf.example.com/web/#/details?id=i-1&serverId=s'),
      ],
    );

    expect(find.text('Watch on Jellyfin'), findsOneWidget);
    expect(find.textContaining('Not on'), findsNothing);
    // One lookup, narrowed by what the detail knows: the year and the title.
    final watch = adapter.watchRequests.single;
    expect(watch.queryParameters['media_type'], 'movie');
    expect(watch.queryParameters['tmdb_id'], '$_tmdbId');
    expect(watch.queryParameters['year'], '2008');
    expect(watch.queryParameters['title'], 'Big Buck Bunny');
    expect(watch.queryParameters.containsKey('tvdb_id'), isFalse);
  });

  testWidgets('a show sends its TVDB id and first-air year', (tester) async {
    final adapter = await pumpDetail(
      tester,
      status: 'partial',
      mediaType: MediaType.tv,
      links: [
        _link(_jellyfin, 'found', url: 'https://jf.example.com/web/#/x'),
      ],
    );

    expect(find.text('Watch on Jellyfin'), findsOneWidget);
    final watch = adapter.watchRequests.single;
    expect(watch.queryParameters['media_type'], 'tv');
    expect(watch.queryParameters['tvdb_id'], '81189');
    expect(watch.queryParameters['year'], '2008');
    expect(watch.queryParameters['title'], 'The Show');
  });

  testWidgets('a confirmed absence says "not yet"; no answer says nothing',
      (tester) async {
    await pumpDetail(
      tester,
      status: 'available',
      mediaServers: const [_jellyfin, _emby],
      links: [
        _link(_jellyfin, 'missing'),
        _link(_emby, 'unreachable'),
      ],
    );

    final missing = find.widgetWithText(TextButton, 'Not on Jellyfin yet');
    expect(missing, findsOneWidget);
    expect(tester.widget<TextButton>(missing).onPressed, isNull);
    expect(find.textContaining('Emby'), findsNothing);
    expect(find.textContaining('Watch on'), findsNothing);
  });

  testWidgets('two servers of one type are told apart by name',
      (tester) async {
    const second = ServiceInstance(
      id: 'jf-b',
      serviceType: 'jellyfin',
      name: 'Cabin Jellyfin',
    );
    await pumpDetail(
      tester,
      status: 'available',
      mediaServers: const [_jellyfin, second],
      links: [
        _link(_jellyfin, 'found', url: 'https://jf.example.com/web/#/a'),
        _link(second, 'found', url: 'https://cabin.example.com/web/#/b'),
      ],
    );

    expect(find.text('Watch on Home Jellyfin'), findsOneWidget);
    expect(find.text('Watch on Cabin Jellyfin'), findsOneWidget);
    expect(find.text('Watch on Jellyfin'), findsNothing);
  });

  testWidgets('nothing is asked when the title is not there to watch',
      (tester) async {
    final adapter = await pumpDetail(
      tester,
      status: 'requested',
      links: [
        _link(_jellyfin, 'found', url: 'https://jf.example.com/web/#/a'),
      ],
    );

    expect(adapter.watchRequests, isEmpty);
    expect(find.textContaining('Watch on'), findsNothing);
  });

  testWidgets('nothing is asked without a Jellyfin or Emby server',
      (tester) async {
    final adapter = await pumpDetail(
      tester,
      status: 'available',
      mediaServers: const [_plex],
      links: [
        _link(_plex, 'found', url: 'https://app.plex.tv/x'),
      ],
    );

    expect(adapter.watchRequests, isEmpty);
    expect(find.textContaining('Watch on'), findsNothing);
  });

  testWidgets('a failed lookup shows nothing rather than a stale answer',
      (tester) async {
    await pumpDetail(
      tester,
      status: 'available',
      links: [
        _link(_jellyfin, 'found', url: 'https://jf.example.com/web/#/a'),
      ],
      watchStatus: 503,
    );

    expect(find.textContaining('Watch on'), findsNothing);
    expect(find.textContaining('Not on'), findsNothing);
  });
}

AuthState _state(List<ServiceInstance> mediaServers, MediaType mediaType) =>
    AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        instances: [
          if (mediaType == MediaType.movie)
            const ServiceInstance(
              id: 'radarr-main',
              serviceType: 'radarr',
              name: 'Main Radarr',
              isDefault: true,
            )
          else
            const ServiceInstance(
              id: 'sonarr-main',
              serviceType: 'sonarr',
              name: 'Main Sonarr',
              isDefault: true,
            ),
          ...mediaServers,
        ],
      ),
      user: const UserProfile(id: 2, username: 'viewer', role: 'user'),
    );

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

/// Minimal backend stub: the TMDB detail, the request status, and the watch
/// lookup, which is recorded so a test can see what was asked.
class _JsonAdapter implements HttpClientAdapter {
  final String status;
  final List<Map<String, dynamic>> links;
  final int watchStatus;
  final List<Uri> watchRequests = [];

  _JsonAdapter({
    required this.status,
    required this.links,
    required this.watchStatus,
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path;
    Object body;
    var code = 200;
    if (path == '/api/media-servers/watch') {
      watchRequests.add(options.uri);
      body = links;
      code = watchStatus;
      if (code != 200) body = {'error': 'temporarily unavailable'};
    } else if (path.endsWith('/status')) {
      body = {'status': status, 'seasons': <dynamic>[]};
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      body = {'results': <dynamic>[]};
    } else if (path.contains('/api/media/tv/')) {
      body = {
        'id': _tmdbId,
        'name': 'The Show',
        'first_air_date': '2008-01-20',
        'external_ids': {'tvdb_id': 81189},
        'seasons': <dynamic>[],
      };
    } else if (path.contains('/api/media/movie/')) {
      body = {
        'id': _tmdbId,
        'title': 'Big Buck Bunny',
        'release_date': '2008-04-10',
      };
    } else {
      body = <dynamic>[];
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      code,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
