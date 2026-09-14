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

void main() {
  for (final admin in [true, false]) {
    testWidgets('pinned TV library initializes every read, admin=$admin', (tester) async {
      final adapter = _LibraryAdapter();
      await _pump(tester, adapter, admin: admin);
      final status = adapter.requests.where((r) => r.path.endsWith('/status'));
      expect(status, isNotEmpty);
      expect(status.every((r) => r.queryParameters['instance_id'] == 'tv-import'), isTrue);
      final options = adapter.requests.where((r) => r.path == '/api/requests/options');
      expect(options, isNotEmpty);
      expect(options.every((r) => r.queryParameters['instance_id'] == 'tv-import'), isTrue);
      final library = adapter.requests.where((r) => r.path.startsWith('/api/instances/'));
      expect(library.any((r) => r.path.endsWith('/episode')), isTrue);
      expect(library.every((r) => r.path.startsWith('/api/instances/tv-import/')), isTrue);
      expect(find.text('Open in Sonarr'), admin ? findsOneWidget : findsNothing);
      expect(find.text('Dahmer file'), findsNothing);
      expect(tester.takeException(), isNull);
    });
  }

  for (final code in [400, 403]) {
    testWidgets('live library refusal $code never falls back to the default', (tester) async {
      final adapter = _LibraryAdapter(statusCode: code);
      await _pump(tester, adapter);
      expect(find.text('Library unavailable'), findsOneWidget);
      expect(adapter.requests.where((r) => r.path.endsWith('/status'))
          .every((r) => r.queryParameters['instance_id'] == 'tv-import'), isTrue);
      expect(adapter.requests.where((r) => r.path.startsWith('/api/instances/')), isEmpty);
    });
  }

  testWidgets('deleted or inaccessible library shows unavailable before reads', (tester) async {
    final adapter = _LibraryAdapter();
    await _pump(tester, adapter, instanceId: 'tv-deleted');
    expect(find.text('Library unavailable'), findsOneWidget);
    expect(adapter.requests.where((r) => r.path.endsWith('/status') ||
        r.path.startsWith('/api/instances/') || r.path == '/api/requests/options'), isEmpty);
  });

  testWidgets('legacy destination keeps default-library behavior', (tester) async {
    final adapter = _LibraryAdapter();
    await _pump(tester, adapter, instanceId: null);
    expect(adapter.requests.where((r) => r.path.endsWith('/status'))
        .every((r) => r.queryParameters['instance_id'] == null), isTrue);
    expect(find.text('Library unavailable'), findsNothing);
  });
}

Future<void> _pump(WidgetTester tester, _LibraryAdapter adapter,
    {bool admin = true, String? instanceId = 'tv-import'}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = adapter;
  final state = AuthState(
    user: UserProfile(id: 1, username: 'viewer', role: admin ? 'admin' : 'user'),
    connection: const BackendConnection(serverUrl: 'http://localhost',
      accessToken: 'fixture', refreshToken: 'fixture', configConfirmed: true,
      tvMatchCorrections: true, services: AvailableServices(mediaDownloads: true),
      instances: [
        ServiceInstance(id: 'tv-default', serviceType: 'sonarr', name: 'Default TV', isDefault: true, mediaDownloads: true),
        ServiceInstance(id: 'tv-import', serviceType: 'sonarr', name: 'Importing TV', mediaDownloads: true),
      ],
    ),
  );
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _Auth(state)),
    backendClientProvider.overrideWithValue(dio),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  final router = GoRouter(routes: [GoRoute(path: '/', builder: (_, __) =>
      MediaDetailScreen(id: 225634, mediaType: MediaType.tv, instanceId: instanceId))]);
  addTearDown(router.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(container: container,
      child: MaterialApp.router(routerConfig: router)));
  await tester.pumpAndSettle();
}

class _Auth extends AuthNotifier {
  _Auth(this.initial);
  final AuthState initial;
  @override
  Future<AuthState> build() async => initial;
}

class _LibraryAdapter implements HttpClientAdapter {
  _LibraryAdapter({this.statusCode = 200});
  final int statusCode;
  final requests = <RequestOptions>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    requests.add(options);
    final path = options.path;
    final Object body;
    var code = 200;
    if (path.endsWith('/status')) {
      code = statusCode;
      body = {'status': 'partially_available', 'status_known': true,
        'match': {'tmdb_id': 225634, 'tvdb_id': 389492, 'series_id': 42,
          'season_map': {'1': 2}, 'state': 'resolved', 'revision': 'current'},
        'seasons': [{'season_number': 1, 'status': 'partially_available', 'episode_count': 2}]};
    } else if (path == '/api/requests/options') {
      body = {'can_choose_season': true};
    } else if (path.endsWith('/api/v3/series')) {
      body = [{'id': 42, 'tvdbId': 389492, 'tmdbId': 113988, 'title': 'Monster parent'}];
    } else if (path.endsWith('/api/v3/episode')) {
      body = [for (final season in [1, 2]) {
        'id': season*10, 'seriesId': 42, 'seasonNumber': season, 'episodeNumber': 1,
        'hasFile': true, 'episodeFileId': 100+season,
        'title': season == 1 ? 'Dahmer file' : 'Menendez file',
        'episodeFile': {'id': 100+season, 'size': 100, 'relativePath': 'episode.mkv'},
      }];
    } else if (path == '/api/media/tv/225634') {
      body = {'id': 225634, 'name': 'Menendez story',
        'seasons': [{'id': 1, 'season_number': 1, 'name': 'Season 1', 'episode_count': 2}]};
    } else if (path.endsWith('/similar') || path.endsWith('/recommendations')) {
      body = {'results': []};
    } else {
      body = [];
    }
    return ResponseBody.fromString(jsonEncode(body), code,
        headers: {'content-type': ['application/json']});
  }
  @override
  void close({bool force = false}) {}
}
