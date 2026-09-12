import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_access/logic/media_app_launcher.dart';
import 'package:cantinarr/features/media_access/logic/video_apps_provider.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
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
  String fallbackUrl = '',
}) =>
    {
      'instance_id': server.id,
      'service_type': server.serviceType,
      'name': server.name,
      'state': state,
      if (url.isNotEmpty) 'url': url,
      if (fallbackUrl.isNotEmpty) 'fallback_url': fallbackUrl,
    };

void main() {
  Future<_JsonAdapter> pumpDetail(
    WidgetTester tester, {
    required String status,
    required List<Map<String, dynamic>> links,
    List<ServiceInstance> mediaServers = const [_jellyfin],
    MediaType mediaType = MediaType.movie,
    int watchStatus = 200,
    MediaAppLauncher? launcher,
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
          if (launcher != null)
            mediaAppLauncherProvider.overrideWithValue(launcher),
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

  for (final server in [_plex, _jellyfin, _emby]) {
    for (final type in [MediaType.movie, MediaType.tv]) {
      testWidgets('${server.serviceType} $type opens Infuse using the verified title', (tester) async {
        final urls = <Uri>[];
        await pumpDetail(tester, status: 'available', mediaType: type,
          mediaServers: [server], links: [{
            ..._link(server, 'found', url: 'https://watch.example/title'),
            'video_apps': {'ios': 'infuse'},
          }], launcher: MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false,
            launchExternal: (uri) async { urls.add(uri); return true; }));
        final action = find.text('Open in Infuse');
        await tester.ensureVisible(action);
        await tester.tap(action);
        await tester.pumpAndSettle();
        expect(urls.single.toString(), 'infuse://${type == MediaType.movie ? 'movie' : 'series'}/$_tmdbId');
      });
    }
  }

  testWidgets('multiple Infuse actions retain service and instance context', (tester) async {
    const second = ServiceInstance(id: 'px-b', serviceType: 'plex', name: 'Other Plex');
    final servers = [_plex, second, _jellyfin, _emby];
    await pumpDetail(tester, status: 'available', mediaServers: servers,
      links: [for (final server in servers) {
        ..._link(server, 'found', url: 'https://watch.example/title'),
        'video_apps': {'ios': 'infuse'},
      }], launcher: MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false));
    for (final server in servers) {
      expect(find.textContaining('Open in Infuse ·').evaluate().any((e) =>
        (e.widget as Text).data!.contains(server.name)), isTrue);
    }
  });

  testWidgets('generic Infuse fallback does not receive the page title id', (tester) async {
    final urls = <Uri>[];
    await pumpDetail(tester, status: 'available', mediaServers: [_plex], links: [{
      ..._link(_plex, 'unverified', fallbackUrl: 'https://app.plex.tv'),
      'video_apps': {'ios': 'infuse'},
    }], launcher: MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false,
      launchExternal: (uri) async { urls.add(uri); return true; }));
    await tester.ensureVisible(find.text('Open in Infuse'));
    await tester.tap(find.text('Open in Infuse'));
    await tester.pumpAndSettle();
    expect(urls.single.toString(), 'infuse://');
  });

  for (final refresh in ['save', 'config', 'resume']) {
    testWidgets('$refresh refreshes the video choice on an open title page', (tester) async {
      final links = [{..._link(_plex, 'found', url: 'https://app.plex.tv/desktop/#!/server/m/details?key=%2Flibrary%2Fmetadata%2F1'),
        'video_apps': {'ios': 'service'}}];
      final adapter = await pumpDetail(tester, status: 'available', mediaServers: [_plex], links: links,
        launcher: MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false));
      expect(find.text('Watch on Plex'), findsOneWidget);
      links.single['video_apps'] = {'ios': 'infuse'};
      if (refresh == 'save') {
        ProviderScope.containerOf(tester.element(find.byType(MediaDetailScreen)))
          .read(videoAppRevisionProvider.notifier).state++;
      } else if (refresh == 'config') {
        (ProviderScope.containerOf(tester.element(find.byType(MediaDetailScreen)))
          .read(authProvider.notifier) as _FakeAuthNotifier).refreshConfiguration();
      } else {
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      }
      await tester.pumpAndSettle();
      expect(find.text('Open in Infuse'), findsOneWidget);
      expect(adapter.watchRequests.length, greaterThan(1));
    });
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

  testWidgets('a Plex-only household keeps the exact web link on desktop',
      (tester) async {
    const url = 'https://app.plex.tv/desktop/#!/server/m1/details?key=%2Flibrary%2Fmetadata%2F123';
    final launched = <String>[];
    const channel = MethodChannel('plugins.flutter.io/url_launcher');
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      channel,
      (call) async {
        if (call.method == 'launch') {
          launched.add((call.arguments as Map)['url'] as String);
        }
        return true;
      },
    );
    addTearDown(() => tester.binding.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null));
    final adapter = await pumpDetail(
      tester,
      status: 'available',
      mediaServers: const [_plex],
      launcher: MediaAppLauncher(platform: TargetPlatform.linux),
      links: [
        _link(_plex, 'found', url: url),
      ],
    );

    expect(adapter.watchRequests, hasLength(1));
    final button = find.widgetWithText(TextButton, 'Watch on Plex');
    expect(button, findsOneWidget);
    expect(find.text('Open Plex'), findsNothing);
    await tester.ensureVisible(button);
    await tester.tap(button);
    await tester.pumpAndSettle();
    expect(launched, [url]);
  });

  for (final state in ['unverified', 'unreachable', 'missing']) {
    testWidgets('Plex $state offers only the generic shortcut', (tester) async {
      const url = 'https://watch.example.com';
      final launched = <String>[];
      const channel = MethodChannel('plugins.flutter.io/url_launcher');
      tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        channel,
        (call) async {
          if (call.method == 'launch') {
            launched.add((call.arguments as Map)['url'] as String);
          }
          return true;
        },
      );
      addTearDown(() => tester.binding.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, null));
      await pumpDetail(tester,
          status: 'partial',
          mediaType: MediaType.tv,
          mediaServers: const [_plex],
          launcher: MediaAppLauncher(platform: TargetPlatform.linux),
          links: [_link(_plex, state, fallbackUrl: url)]);
      final button = find.widgetWithText(TextButton, 'Open Plex');
      expect(button, findsOneWidget);
      expect(find.textContaining('Watch on'), findsNothing);
      expect(find.textContaining('Not on'), findsNothing);
      await tester.ensureVisible(button);
      await tester.tap(button);
      await tester.pumpAndSettle();
      expect(launched, [url]);
    });
  }

  for (final platform in [TargetPlatform.iOS, TargetPlatform.android]) {
    testWidgets('$platform watch buttons hand each title to its media app',
        (tester) async {
      final external = <Uri>[];
      final android = <({String serviceType, Uri? uri})>[];
      final launcher = MediaAppLauncher(
        platform: platform,
        launchExternal: (uri) async {
          external.add(uri);
          return true;
        },
        launchAndroid: (serviceType, uri) async {
          android.add((serviceType: serviceType, uri: uri));
          return true;
        },
      );
      await pumpDetail(tester,
          status: 'partial',
          mediaType: MediaType.tv,
          mediaServers: const [_plex, _jellyfin, _emby],
          launcher: launcher,
          links: [
            _link(_plex, 'found', url: 'https://app.plex.tv/desktop/'
                '#!/server/m1/details?key=%2Flibrary%2Fmetadata%2F123'),
            _link(_jellyfin, 'found', url: 'https://jf.example.com/'
                'web/#/details?id=456&serverId=jf1'),
            _link(_emby, 'found', url: 'https://emby.example.com/'
                'web/index.html#!/item?id=789&serverId=em1'),
          ]);
      for (final name in ['Plex', 'Jellyfin', 'Emby']) {
        final button = find.widgetWithText(TextButton, 'Watch on $name');
        await tester.ensureVisible(button);
        await tester.tap(button);
        await tester.pumpAndSettle();
      }
      final plex = platform == TargetPlatform.iOS
          ? external.first
          : android.first.uri!;
      expect(plex.queryParameters['metadataType'], '2');
      expect(plex.queryParameters['server'], 'm1');
      if (platform == TargetPlatform.iOS) {
        expect(external.map((uri) => uri.scheme),
            ['plex', 'org.jellyfin.expo', 'emby']);
        expect(external.last.queryParameters,
            {'serverId': 'em1', 'itemId': '789'});
      } else {
        expect(android.map((call) => call.serviceType),
            ['plex', 'jellyfin', 'emby']);
        expect(android[1].uri, isNull);
        expect(android.last.uri.toString(), 'emby://items/em1/789');
        expect(external, isEmpty);
      }
    });
  }

  testWidgets('unverified Plex opens the app without a title claim',
      (tester) async {
    final launched = <Uri>[];
    await pumpDetail(tester,
        status: 'available',
        mediaServers: const [_plex],
        launcher: MediaAppLauncher(
          platform: TargetPlatform.iOS,
          launchExternal: (uri) async {
            launched.add(uri);
            return true;
          },
        ),
        links: [_link(_plex, 'unverified', fallbackUrl: 'https://app.plex.tv')]);
    final button = find.widgetWithText(TextButton, 'Open Plex');
    await tester.ensureVisible(button);
    await tester.tap(button);
    await tester.pumpAndSettle();
    expect(launched.single.toString(), 'plex://');
    expect(find.textContaining('Watch on'), findsNothing);
  });

  testWidgets('watch reports failure only after the app and browser fail',
      (tester) async {
    final launched = <Uri>[];
    await pumpDetail(tester,
        status: 'available',
        launcher: MediaAppLauncher(
          platform: TargetPlatform.iOS,
          launchExternal: (uri) async {
            launched.add(uri);
            return false;
          },
        ),
        links: [_link(_jellyfin, 'found', url: 'https://jf.example.com/web/'
            '#/details?id=123&serverId=jf1')]);
    final button = find.widgetWithText(TextButton, 'Watch on Jellyfin');
    await tester.ensureVisible(button);
    await tester.tap(button);
    await tester.pumpAndSettle();
    expect(launched.map((uri) => uri.scheme), ['org.jellyfin.expo', 'https']);
    expect(find.text("Couldn't open Jellyfin."), findsOneWidget);
  });

  testWidgets('Plex names distinguish an exact link from another server shortcut',
      (tester) async {
    const second = ServiceInstance(id: 'px-b', serviceType: 'plex', name: 'Cabin Plex');
    await pumpDetail(tester,
        status: 'available',
        mediaServers: const [_plex, second],
        links: [
          _link(_plex, 'found', url: 'https://app.plex.tv/desktop/#!/server/m1/details?key=x'),
          _link(second, 'unverified', fallbackUrl: 'https://app.plex.tv'),
        ]);
    expect(find.text('Watch on Cantina Plex'), findsOneWidget);
    expect(find.text('Open Cabin Plex'), findsOneWidget);
  });

  testWidgets('no media server means no lookup', (tester) async {
    final adapter = await pumpDetail(tester,
        status: 'available', mediaServers: const [], links: const []);
    expect(adapter.watchRequests, isEmpty);
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

  void refreshConfiguration() => state = AsyncData(authState.copyWith(
    connection: authState.connection!.copyWith()));
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
