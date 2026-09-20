import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/config/app_config.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/dashboard/ui/tv_library_link.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:cantinarr/features/media_detail/ui/media_hero.dart';
import 'package:cantinarr/features/media_detail/ui/season_table.dart';
import 'package:cantinarr/features/media_detail/ui/tv_library_detail_screen.dart';
import 'package:cantinarr/features/request/data/request_service.dart' show RequestStatus;
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('combined series uses the regular detail layout', (tester) async {
    tester.view.physicalSize = const Size(1000, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(() { tester.view.resetPhysicalSize(); tester.view.resetDevicePixelRatio(); });
    await _pump(tester, _Adapter());
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    await expectLater(find.byKey(const ValueKey('tv-library-page')),
        matchesGoldenFile('goldens/tv_library_detail_desktop.png'));
  });
  test('TV library capability is opt-in and survives connection copies', () {
    expect(ServerConfig.fromJson({}).tvLibraryNavigation, isFalse);
    expect(ServerConfig.fromJson({'tv_library_navigation': true}).tvLibraryNavigation, isTrue);
    const connection = BackendConnection(serverUrl: 'http://fixture',
        accessToken: 'test', refreshToken: 'test', tvLibraryNavigation: true);
    expect(connection.copyWith().tvLibraryNavigation, isTrue);
    expect(connection.copyWith(tvLibraryNavigation: false).tvLibraryNavigation, isFalse);
  });

  test('library art retains its CDN URL; catalog art keeps TMDB sizes', () {
    expect(AppConfig.tmdbPoster('https://cdn.example/parent.jpg'), 'https://cdn.example/parent.jpg');
    expect(AppConfig.tmdbBackdropOriginal('https://cdn.example/backdrop.jpg'), 'https://cdn.example/backdrop.jpg');
    expect(AppConfig.tmdbPoster('/poster.jpg'), 'https://image.tmdb.org/t/p/w500/poster.jpg');
  });

  for (final parentId in [0, 113988]) {
    testWidgets('parent $parentId opens regular title page with all four seasons', (tester) async {
      final adapter = _Adapter();
      final router = await _pump(tester, adapter, tmdbId: parentId);
      await tester.tap(find.text('Monster card'));
      await tester.pumpAndSettle();
      expect(find.byType(MediaDetailScreen), findsOneWidget);
      expect(find.text('Choose a title'), findsNothing);
      expect(find.text('An anthology with four stories.'), findsOneWidget);
      final hero = tester.widget<SliverPersistentHeader>(find.byType(SliverPersistentHeader)).delegate as MediaHeroDelegate;
      expect(hero.title, 'Monster (2022)');
      expect(hero.posterPath, isNull); // Artwork transport is covered separately.
      final table = tester.widget<SeasonTable>(find.byType(SeasonTable));
      expect(table.seasons.map((s) => s.seasonNumber), [1, 2, 3, 4]);
      expect(table.notifier.state.hasStatus, isTrue);
      expect(table.notifier.instanceId, 'tv-other');
      expect(adapter.requests.where((r) => r.path == '/api/requests/tv-library')
          .every((r) => r.queryParameters['instance_id'] == 'tv-other' &&
              r.queryParameters['series_id'] == 42 && !r.queryParameters.containsKey('season_number')), isTrue);
      expect(adapter.requests.any((r) => r.path == '/api/media/tv/0' || r.path.contains('/requests/0/')), isFalse);
      router.pop();
      await tester.pumpAndSettle();
      expect(find.text('Monster card'), findsOneWidget);
      await tester.tap(find.text('Movies'));
      await tester.pumpAndSettle();
      expect(find.text('Movies screen'), findsOneWidget);
      router.go('/dashboard/tv');
      await tester.pumpAndSettle();
      expect(find.text('Monster card'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('calendar episode opens the same full series, not its source story', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter, season: 2);
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    expect(tester.widget<SeasonTable>(find.byType(SeasonTable)).seasons.length, 4);
    expect(adapter.requests.where((r) => r.path == '/api/requests/tv-library')
        .any((r) => r.queryParameters.containsKey('season_number')), isFalse);
  });

  testWidgets('one blocked mapping keeps the page and other season requests working', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() { tester.view.resetPhysicalSize(); tester.view.resetDevicePixelRatio(); });
    final adapter = _Adapter(blocked: {2});
    await _pump(tester, adapter);
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    expect(find.byType(MediaDetailScreen), findsOneWidget);
    final table = tester.widget<SeasonTable>(find.byType(SeasonTable));
    expect(table.seasons.map((s) => s.seasonNumber), [1, 2, 3, 4]);
    expect(table.notifier.state.seasons[1].isRequestable, isFalse);
    expect(table.notifier.state.seasons[0].isRequestable, isTrue);
    final boxes = tester.widgetList<Checkbox>(find.byType(Checkbox)).toList();
    expect(boxes[1].value, isFalse);
    expect(boxes[1].onChanged, isNull);
    expect(find.text('This season needs a TV match before it can be requested.'), findsOneWidget);
    await tester.ensureVisible(find.text('All'));
    await tester.tap(find.text('All'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Request 3 seasons'));
    await tester.tap(find.text('Request 3 seasons'));
    await tester.pumpAndSettle();
    expect(adapter.requests.singleWhere((r) => r.method == 'POST').data['seasons'], [1, 3, 4]);
    expect(table.notifier.state.seasons[1].isRequestable, isFalse);
    expect(tester.takeException(), isNull);
  });

  testWidgets('all mappings unavailable still shows the title and can recover on retry', (tester) async {
    final adapter = _Adapter(blocked: {1, 2, 3, 4});
    final router = await _pump(tester, adapter);
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    expect(find.text('An anthology with four stories.'), findsOneWidget);
    expect(tester.widget<SeasonTable>(find.byType(SeasonTable)).seasons.length, 4);
    expect(tester.widgetList<Checkbox>(find.byType(Checkbox))
        .every((c) => c.onChanged == null && c.value == false), isTrue);
    expect(find.text('All'), findsNothing);
    expect(tester.widget<ElevatedButton>(find.widgetWithText(ElevatedButton, 'Request')).onPressed, isNull);
    expect(find.text('Season requests are unavailable. See the notes beside each season.'), findsOneWidget);
    adapter.blocked.clear();
    await tester.ensureVisible(find.text('Retry TV match'));
    await tester.tap(find.text('Retry TV match'));
    await tester.pumpAndSettle();
    expect(tester.widgetList<Checkbox>(find.byType(Checkbox))
        .every((c) => c.onChanged != null), isTrue);
    expect(find.text('Retry TV match'), findsNothing);
    router.pop();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Movies'));
    await tester.pumpAndSettle();
    expect(find.text('Movies screen'), findsOneWidget);
    router.go('/dashboard/tv');
    await tester.pumpAndSettle();
    expect(find.text('Monster card'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('problem reports exclude seasons without a verified source identity', (tester) async {
    final adapter = _Adapter(blocked: {2})..requested.add(1);
    await _pump(tester, adapter, reporting: true);
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Report a problem'));
    await tester.tap(find.text('Report a problem'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(ListTile, 'Season 1'), findsOneWidget);
    expect(find.widgetWithText(ListTile, 'Season 2'), findsNothing);
    expect(find.widgetWithText(ListTile, 'Season 3'), findsOneWidget);
    expect(adapter.requests.any((r) => r.path.contains('/tv/0')), isFalse);
  });

  testWidgets('season request keeps native season, revision and exact library', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    final notifier = tester.widget<SeasonTable>(find.byType(SeasonTable)).notifier;
    final request = notifier.request(seasons: [2]);
    await tester.pumpAndSettle();
    expect(await request, isTrue);
    await tester.pumpAndSettle();
    final write = adapter.requests.singleWhere((r) => r.method == 'POST');
    expect(write.path, '/api/requests/tv-library');
    expect(write.data, {'instance_id': 'tv-other', 'series_id': 42,
      'revision': 'current', 'seasons': [2]});
    expect(notifier.state.seasons[1].status, RequestStatus.requested);
    expect(notifier.state.seasons[0].status, RequestStatus.unavailable);
  });

  testWidgets('partial submission refreshes accepted seasons before retry', (tester) async {
    final adapter = _Adapter(partialWrite: true);
    await _pump(tester, adapter);
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    final notifier = tester.widget<SeasonTable>(find.byType(SeasonTable)).notifier;
    final request = notifier.request(seasons: [2, 3]);
    await tester.pumpAndSettle();
    expect(await request, isFalse);
    await tester.pumpAndSettle();
    expect(notifier.state.hasStatus, isTrue);
    expect(notifier.state.seasons[1].status, RequestStatus.requested);
    expect(notifier.state.seasons[2].status, RequestStatus.unavailable);
    expect(notifier.state.error, contains('Some seasons were accepted'));
  });

  testWidgets('ordinary library series retains catalog details and recommendations', (tester) async {
    final adapter = _Adapter(catalogId: 456);
    await _pump(tester, adapter);
    await tester.tap(find.text('Monster card'));
    await tester.pumpAndSettle();
    final detail = tester.widget<MediaDetailScreen>(find.byType(MediaDetailScreen));
    expect(detail.id, 456);
    expect(detail.librarySeries, isNull);
    expect(find.text('Ordinary overview'), findsOneWidget);
    expect(adapter.requests.any((r) => r.path == '/api/media/tv/456/recommendations'), isTrue);
    expect(adapter.requests.where((r) => r.path == '/api/requests/456/status')
        .every((r) => r.queryParameters['instance_id'] == 'tv-other'), isTrue);
  });

  for (final code in [403, 404, 409, 503]) {
    testWidgets('resolution $code keeps back navigation usable without a wrong title', (tester) async {
      final adapter = _Adapter(status: code);
      final router = await _pump(tester, adapter);
      await tester.tap(find.text('Monster card'));
      await tester.pumpAndSettle();
      expect(find.byType(MediaDetailScreen), findsNothing);
      expect(find.text(code == 403 ? 'Library unavailable' : code == 404
          ? 'This title is not available.' : 'Retry'), findsOneWidget);
      if (code >= 409) {
        adapter.status = 200;
        await tester.tap(find.text('Retry'));
        await tester.pumpAndSettle();
        expect(find.byType(MediaDetailScreen), findsOneWidget);
      }
      router.pop();
      await tester.pumpAndSettle();
      expect(find.text('Monster card'), findsOneWidget);
    });
  }

  for (final parentId in [0, 456]) {
    testWidgets('old server handles parent $parentId without unsupported read', (tester) async {
      final adapter = _Adapter();
      await _pump(tester, adapter, supported: false, tmdbId: parentId);
      if (parentId == 0) {
        expect(tester.widget<TextButton>(find.widgetWithText(TextButton, 'Monster card')).onPressed, isNull);
      } else {
        await tester.tap(find.text('Monster card'));
        await tester.pumpAndSettle();
        expect(find.text('/detail/tv/456?instance_id=tv-other'), findsOneWidget);
      }
      expect(adapter.requests, isEmpty);
    });
  }

  testWidgets('late series load cannot navigate back over another screen', (tester) async {
    final gate = Completer<void>();
    final adapter = _Adapter(gate: gate);
    final router = await _pump(tester, adapter);
    await tester.tap(find.text('Monster card'));
    await tester.pump();
    router.go('/dashboard/movies');
    await tester.pumpAndSettle();
    gate.complete();
    await tester.pumpAndSettle();
    expect(find.text('Movies screen'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

Future<GoRouter> _pump(WidgetTester tester, _Adapter adapter,
    {int tmdbId = 0, int? season, bool supported = true, bool reporting = false}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://fixture'))..httpClientAdapter = adapter;
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _Auth(supported, reporting)),
    backendClientProvider.overrideWithValue(dio),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
  ]);
  final router = GoRouter(initialLocation: '/dashboard/tv', routes: [
    GoRoute(path: '/dashboard/tv', builder: (context, _) => Scaffold(body: Column(children: [
      TVLibraryLink(instanceId: 'tv-other', seriesId: 42, tmdbId: tmdbId, seasonNumber: season,
        builder: (tap) => TextButton(onPressed: tap, child: const Text('Monster card'))),
      TextButton(onPressed: () => context.go('/dashboard/movies'), child: const Text('Movies')),
    ]))),
    GoRoute(path: '/detail/tv-library/:id', builder: (_, state) => TVLibraryDetailScreen(
      seriesId: int.parse(state.pathParameters['id']!), instanceId: state.uri.queryParameters['instance_id']!)),
    GoRoute(path: '/detail/tv/:id', builder: (_, state) => Scaffold(body: Text(state.uri.toString()))),
    GoRoute(path: '/dashboard/movies', builder: (_, __) => const Scaffold(body: Text('Movies screen'))),
  ]);
  addTearDown(router.dispose);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  await tester.pumpWidget(UncontrolledProviderScope(container: container,
    child: RepaintBoundary(key: const ValueKey('tv-library-page'),
      child: MaterialApp.router(theme: AppTheme.dark, routerConfig: router))));
  await tester.pumpAndSettle();
  return router;
}

class _Auth extends AuthNotifier {
  _Auth(this.supported, this.reporting);
  final bool supported;
  final bool reporting;
  @override
  Future<AuthState> build() async => AuthState(
    connection: BackendConnection(serverUrl: 'http://fixture', accessToken: 'test',
      refreshToken: 'test', tvLibraryNavigation: supported, tvMatchCorrections: true,
      allowReporting: reporting,
      configConfirmed: true, instances: const [
        ServiceInstance(id: 'tv-main', serviceType: 'sonarr', name: 'Default TV', isDefault: true),
        ServiceInstance(id: 'tv-other', serviceType: 'sonarr', name: 'Other TV'),
      ]),
    user: const UserProfile(id: 1, username: 'tester', role: 'user'),
  );
}

class _Adapter implements HttpClientAdapter {
  _Adapter({this.status = 200, this.gate, this.catalogId = 0, this.partialWrite = false,
    Set<int> blocked = const {}}) : blocked = {...blocked};
  final Set<int> blocked;
  int status;
  final int catalogId;
  final bool partialWrite;
  final Completer<void>? gate;
  final requested = <int>{};
  final requests = <RequestOptions>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    requests.add(options);
    final path = options.path;
    Object body;
    var code = 200;
    if (path == '/api/requests/tv-library') {
      await gate?.future;
      code = status;
      if (options.method == 'POST') {
        final selected = (options.data['seasons'] as List?)?.cast<int>() ?? [1, 2, 3, 4];
        requested.addAll(partialWrite ? selected.take(1) : selected);
        body = {'success': !partialWrite, 'accepted_seasons': requested.toList(),
          if (partialWrite) 'error': 'Some seasons were accepted. Review the remaining seasons.'};
      } else {
        body = {'instance_id': 'tv-other', 'series_id': 42,
          if (catalogId > 0) 'tmdb_id': catalogId,
          'revision': 'current',
          'detail': {'id': 0, 'name': 'Monster (2022)', 'overview': 'An anthology with four stories.',
            'external_ids': {'tvdb_id': 389492},
            'seasons': [for (var n = 1; n <= 4; n++) {'id': n, 'season_number': n, 'episode_count': 2}]},
          'status': {'status': requested.isEmpty ? 'unavailable' : 'partial', 'status_known': true,
            'seasons': [for (var n = 1; n <= 4; n++) {'season_number': n,
              if (blocked.contains(n)) 'request_blocked_reason': 'tv_seasons_unmapped',
              if (blocked.contains(n)) 'request_blocked_message': 'This season needs a TV match before it can be requested.',
              'episode_count': 2, 'status': requested.contains(n) ? 'requested' : 'unavailable'}]},
          'matches': [for (var n = 1; n <= 4; n++) if (!blocked.contains(n)) {'tmdb_id': n*100, 'series_id': 42,
            'tvdb_id': 389492, 'state': 'resolved', 'revision': 'source$n', 'season_map': {'1': n}}],
        };
      }
    } else if (path == '/api/requests/options') {
      body = {'can_choose_season': true};
    } else if (path == '/api/media/tv/456') {
      body = {'id': 456, 'name': 'Ordinary show', 'overview': 'Ordinary overview'};
    } else if (path.endsWith('/status')) {
      body = {'status': 'unavailable', 'status_known': true};
    } else if (path.endsWith('/similar') || path.endsWith('/recommendations')) {
      body = {'results': []};
    } else {
      body = [];
    }
    return ResponseBody.fromString(jsonEncode(body), code,
      headers: {Headers.contentTypeHeader: [Headers.jsonContentType]});
  }
  @override
  void close({bool force = false}) {}
}
