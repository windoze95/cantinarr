import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:cantinarr/features/discover/logic/search_library_status.dart';
import 'package:cantinarr/features/discover/logic/tv_card_status_provider.dart';
import 'package:cantinarr/features/discover/ui/browse_grid_screen.dart';
import 'package:cantinarr/features/discover/ui/category_row.dart';
import 'package:cantinarr/features/discover/ui/search_results_view.dart';
import 'package:cantinarr/features/request/data/request_service.dart' hide RequestOptions;
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _ids = [113988, 225634, 286801, 299939];
const _legacy = LibraryStatus(label: 'Available', color: AppTheme.available);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  // The 4K badges setting is device storage; no test may inherit another's.
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('TV card labels preserve authoritative statuses and source counts', () {
    for (final status in RequestStatus.values) {
      final badge = tvLibraryStatus(RequestStatusDetail(status: status));
      expect(badge?.label, switch (status) {
        RequestStatus.unavailable || RequestStatus.denied => null,
        RequestStatus.pending => 'Pending',
        _ => status.label,
      });
    }
    final partial = tvLibraryStatus(const RequestStatusDetail(
      status: RequestStatus.partial, seasons: [
        RequestSeasonStatus(seasonNumber: 0, episodeFileCount: 20, episodeCount: 20),
        RequestSeasonStatus(seasonNumber: 1, episodeFileCount: 2, episodeCount: 8),
      ]));
    expect(partial?.episodeSubtitle, '2/8 eps');
    expect(tvLibraryStatus(const RequestStatusDetail(
      status: RequestStatus.partial))?.episodeSubtitle, isNull);
    expect(tvLibraryStatus(const RequestStatusDetail(
      status: RequestStatus.available, isKnown: false))?.label, 'Status unknown');
    for (final state in ['paused', 'unresolved']) {
      expect(tvLibraryStatus(RequestStatusDetail.fromJson({
        'status': 'requested', 'match': {'state': state},
      }))?.label, 'Status unknown');
    }
  });

  test('cache coalesces readers and expires confirmed missing and present alike', () async {
    var now = DateTime(2026);
    var calls = 0;
    var status = RequestStatus.unavailable;
    final cache = TVCardStatusCache(now: () => now, load: (_, __) async {
      calls++;
      return RequestStatusDetail(status: status);
    });
    addTearDown(cache.dispose);
    final first = cache.read(299939);
    expect(identical(first, cache.read(299939)), isTrue);
    expect(await first, isNull);
    status = RequestStatus.requested;
    now = now.add(const Duration(seconds: 29));
    expect(await cache.read(299939), isNull);
    expect(calls, 1);
    now = now.add(const Duration(seconds: 1));
    expect((await cache.read(299939))?.label, 'Requested');
    expect(calls, 2);
  });

  test('no more than four status reads run concurrently', () async {
    final pending = <Completer<RequestStatusDetail>>[];
    final cache = TVCardStatusCache(load: (_, __) {
      final response = Completer<RequestStatusDetail>();
      pending.add(response);
      return response.future;
    });
    addTearDown(cache.dispose);
    final results = [for (var id = 1; id <= 7; id++) cache.read(id)];
    expect(pending.length, 4);
    for (var i = 0; i < 7; i++) {
      pending[i].complete(const RequestStatusDetail(status: RequestStatus.requested));
      await results[i];
    }
    expect(pending.length, 7);
  });

  test('invalidating cancels old work and fences late mapping responses', () async {
    final old = Completer<RequestStatusDetail>();
    CancelToken? oldToken;
    var calls = 0;
    final cache = TVCardStatusCache(load: (_, token) {
      if (calls++ == 0) { oldToken = token; return old.future; }
      return Future.value(const RequestStatusDetail(status: RequestStatus.requested));
    });
    addTearDown(cache.dispose);
    final before = cache.read(299939);
    cache.invalidate();
    expect(oldToken!.isCancelled, isTrue);
    expect((await before)?.label, 'Status unknown');
    expect((await cache.read(299939))?.label, 'Requested');
    old.complete(const RequestStatusDetail(status: RequestStatus.available));
    await Future<void>.delayed(Duration.zero);
    expect(cache.peek(299939)?.label, 'Requested');
  });

  test('failed reads are unknown, cached briefly, and explicitly retryable', () async {
    var fail = true;
    final cache = TVCardStatusCache(load: (_, __) async {
      if (fail) throw StateError('library down');
      return const RequestStatusDetail();
    });
    addTearDown(cache.dispose);
    expect((await cache.read(299939))?.label, 'Status unknown');
    fail = false;
    cache.invalidate();
    expect(await cache.read(299939), isNull);
  });

  test('requester reads use source IDs and only the default granted library', () async {
    final h = await _harness();
    for (final id in _ids) {
      h.adapter.statuses[id] = 'requested';
      expect((await h.container.read(tvCardStatusProvider(id).future))?.label, 'Requested');
    }
    expect(h.adapter.statusReads.map((r) => r.path),
      _ids.map((id) => '/api/requests/$id/status'));
    for (final request in h.adapter.statusReads) {
      expect(request.queryParameters, {
        'media_type': 'tv', 'instance_id': 'shows', 'include_instance_statuses': false,
      });
    }
  });

  test('request tick, permissions, account, library and server changes invalidate', () async {
    final h = await _harness();
    final sub = h.container.listen(tvCardStatusProvider(299939), (_, __) {});
    addTearDown(sub.close);
    expect(await h.container.read(tvCardStatusProvider(299939).future), isNull);
    h.adapter.statuses[299939] = 'requested';
    h.container.read(libraryRefreshTickProvider.notifier).state++;
    expect((await h.container.read(tvCardStatusProvider(299939).future))?.label, 'Requested');
    var expectedReads = 2;
    for (final next in [
      _state.copyWith(user: const UserProfile(id: 1, username: 'tester', role: 'user', permissions: ['request'])),
      _state.copyWith(user: const UserProfile(id: 2, username: 'second', role: 'user')),
      _state.copyWith(connection: _state.connection!.copyWith(instances: const [
        ServiceInstance(id: 'sibling', serviceType: 'sonarr', name: 'Sibling', isDefault: true),
      ])),
      _state.copyWith(connection: _state.connection!.copyWith(serverUrl: 'http://other')),
    ]) {
      h.auth.switchTo(next);
      await h.container.pump();
      await h.container.read(tvCardStatusProvider(299939).future);
      expect(h.adapter.statusReads.length, ++expectedReads);
    }
    expect(h.adapter.statusReads[4].queryParameters['instance_id'], 'sibling');
    h.auth.switchTo(_state.copyWith(connection: _state.connection!.copyWith(instances: [])));
    await h.container.pump();
    expect(await h.container.read(tvCardStatusProvider(299939).future), isNull);
    expect(h.adapter.statusReads.length, expectedReads);
  });

  test('4K is asked for only while badges are on; switching refetches', () async {
    final h = await _harness();
    final sub = h.container.listen(tvCardStatusProvider(299939), (_, __) {});
    addTearDown(sub.close);
    h.adapter.statuses[299939] = 'available';
    h.adapter.fourK.add(299939);
    final off = await h.container.read(tvCardStatusProvider(299939).future);
    expect(off?.label, 'Available');
    expect(off?.is4K, isFalse);
    expect(h.adapter.statusReads.single.queryParameters.containsKey('include_4k'), isFalse);

    await h.container.read(cover4KBadgesProvider.notifier).set(true);
    await h.container.pump();
    final on = await h.container.read(tvCardStatusProvider(299939).future);
    expect(on?.label, 'Available');
    expect(on?.is4K, isTrue);
    expect(h.adapter.statusReads, hasLength(2));
    expect(h.adapter.statusReads.last.queryParameters['include_4k'], isTrue);

    // A server that says nothing about 4K never gets a claim made for it.
    h.adapter.fourK.clear();
    h.container.read(libraryRefreshTickProvider.notifier).state++;
    expect((await h.container.read(tvCardStatusProvider(299939).future))?.is4K, isFalse);
  });

  testWidgets('a whole show in 4K carries the tag on its row card', (tester) async {
    SharedPreferences.setMockInitialValues({'cover_4k_badges': true});
    final h = await _harness();
    h.adapter.statuses[299939] = 'available';
    h.adapter.fourK.add(299939);
    await _pump(tester, h, _surface('row'));
    await tester.pumpAndSettle();
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('4K'), findsOneWidget);
    expect(h.adapter.statusReads.last.queryParameters['include_4k'], isTrue);
    await tester.pumpWidget(const SizedBox.shrink());
    h.container.dispose();
  });

  test('a session switch cancels and discards an in-flight result', () async {
    final h = await _harness();
    final cache = h.container.read(tvCardStatusCacheProvider);
    final held = Completer<RequestStatusDetail>();
    final delayed = TVCardStatusCache(load: (_, __) => held.future);
    final before = delayed.read(299939);
    delayed.dispose();
    h.auth.switchTo(const AuthState());
    await h.container.pump();
    expect(identical(cache, h.container.read(tvCardStatusCacheProvider)), isFalse);
    held.complete(const RequestStatusDetail(status: RequestStatus.available));
    expect((await before)?.label, 'Status unknown');
    expect(delayed.peek(299939), isNull);
  });

  for (final surface in ['search', 'row', 'grid']) {
    for (final scale in [1.0, 2.0]) {
      testWidgets('$surface shows corrected Requested at 320px and ${scale}x text', (tester) async {
        final h = await _harness();
        h.adapter.statuses[299939] = 'requested';
        await _pump(tester, h, _surface(surface), scale: scale);
        expect(find.text('Requested'), findsOneWidget);
        expect(h.adapter.statusReads.single.path, '/api/requests/299939/status');
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
        h.container.dispose();
      });
    }
  }

  testWidgets('Dahmer files never badge Lizzie; repair refreshes the same search', (tester) async {
    final h = await _harness();
    h.adapter.statuses[113988] = 'available';
    await _pump(tester, h, SearchResultsView(
      results: [_item(113988), _item(299939)], isLoading: false, query: 'Monster',
      resolveTVStatus: true,
      libraryStatus: {(MediaType.tv, 299939): _legacy},
    ));
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Requested'), findsNothing);
    h.adapter.statuses[299939] = 'requested';
    h.container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pump();
    await tester.pumpAndSettle();
    expect(h.adapter.statusReads.length, 4);
    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Available'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    h.container.dispose();
  });

  testWidgets('unresolved reads show unknown, not the legacy parent badge', (tester) async {
    final h = await _harness();
    h.adapter.unknown = true;
    await _pump(tester, h, _surface('search'), scale: 2);
    expect(find.text('Status unknown'), findsOneWidget);
    expect(find.text('Available'), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
    h.container.dispose();
  });

  testWidgets('old servers retain existing TV badges without status traffic', (tester) async {
    final h = await _harness(capable: false);
    await _pump(tester, h, _surface('search'));
    expect(find.text('Available'), findsOneWidget);
    expect(h.adapter.statusReads, isEmpty);
    await tester.pumpWidget(const SizedBox.shrink());
    h.container.dispose();
  });

  testWidgets('visible cards refresh on expiry, websocket and app resume', (tester) async {
    final h = await _harness();
    await _pump(tester, h, _surface('search'));
    h.adapter.statuses[299939] = 'requested';
    // The provider timer expires while the card remains visible.
    await tester.pump(const Duration(seconds: 31));
    await tester.pumpAndSettle();
    expect(find.text('Requested'), findsOneWidget);
    h.adapter.statuses[299939] = 'available';
    h.events.add(const WsEvent(type: 'request_updated', data: {'media_type': 'tv', 'instance_id': 'shows'}));
    await tester.pump();
    await tester.pump(const Duration(seconds: 3));
    await tester.pumpAndSettle();
    expect(find.text('Available'), findsOneWidget);
    final cache = h.container.read(tvCardStatusCacheProvider);
    cache.didChangeAppLifecycleState(AppLifecycleState.paused);
    await tester.pumpAndSettle();
    final calls = h.adapter.statusReads.length;
    await tester.pump(const Duration(seconds: 31));
    expect(h.adapter.statusReads.length, calls);
    h.adapter.statuses[299939] = 'partial';
    cache.didChangeAppLifecycleState(AppLifecycleState.resumed);
    await tester.pump();
    await tester.pumpAndSettle();
    expect(find.text('Partial'), findsOneWidget);
    expect(find.text('2/8 eps'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    h.container.dispose();
  });

  testWidgets('inactive routes do not resolve cards', (tester) async {
    final h = await _harness();
    await _pump(tester, h, TickerMode(enabled: false, child: _surface('row')));
    expect(h.adapter.statusReads, isEmpty);
    await tester.pumpWidget(const SizedBox.shrink());
    h.container.dispose();
  });
}

MediaItem _item(int id) => MediaItem(id: id, title: id == 113988 ? 'Dahmer' : 'Lizzie Borden',
  mediaType: MediaType.tv, releaseDate: '2026-01-01', voteAverage: 8);

Widget _surface(String surface) => switch (surface) {
  'search' => SearchResultsView(results: [_item(299939)], isLoading: false,
    query: 'Lizzie', resolveTVStatus: true, libraryStatus: {(MediaType.tv, 299939): _legacy}),
  'row' => ListView(children: [CategoryRow(title: 'TV', items: [_item(299939)],
    isLoading: false, isTvRow: true, resolveTVStatus: true,
    libraryStatus: {(MediaType.tv, 299939): _legacy})]),
  _ => const BrowseGridScreen(query: BrowseQuery(type: MediaType.tv, feed: BrowseFeed.topRated)),
};

typedef _Harness = ({ProviderContainer container, _Adapter adapter,
  _Auth auth, StreamController<WsEvent> events});

Future<_Harness> _harness({bool capable = true}) async {
  final adapter = _Adapter();
  final auth = _Auth(_state.copyWith(connection: _state.connection!.copyWith(tvMatchCorrections: capable)));
  final events = StreamController<WsEvent>.broadcast();
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => auth),
    backendClientProvider.overrideWithValue(Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = adapter),
    libraryChangedEventsProvider.overrideWith((_) => events.stream),
    tvCardStatusClockProvider.overrideWithValue(() => adapter.now()),
  ]);
  addTearDown(() { container.dispose(); events.close(); });
  await container.read(authProvider.future);
  return (container: container, adapter: adapter, auth: auth, events: events);
}

Future<void> _pump(WidgetTester tester, _Harness h, Widget child, {double scale = 1}) async {
  h.adapter.now = tester.binding.clock.now;
  tester.view.physicalSize = const Size(320, 640);
  tester.view.devicePixelRatio = 1;
  addTearDown(() { tester.view.resetPhysicalSize(); tester.view.resetDevicePixelRatio(); });
  await tester.pumpWidget(UncontrolledProviderScope(container: h.container,
    child: MaterialApp(theme: AppTheme.dark, builder: (context, child) =>
      MediaQuery(data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(scale)), child: child!),
      home: Scaffold(body: child))));
  await tester.pumpAndSettle();
}

const _state = AuthState(
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
  connection: BackendConnection(serverUrl: 'http://localhost', accessToken: 'access',
    refreshToken: 'refresh', services: AvailableServices(sonarr: true), tvMatchCorrections: true,
    instances: [ServiceInstance(id: 'shows', serviceType: 'sonarr', name: 'TV', isDefault: true)]),
);

class _Auth extends AuthNotifier {
  final AuthState initial;
  _Auth(this.initial);
  @override
  Future<AuthState> build() async => initial;
  void switchTo(AuthState value) => state = AsyncData(value);
}

class _Adapter implements HttpClientAdapter {
  DateTime Function() now = DateTime.now;
  final statusReads = <RequestOptions>[];
  final statuses = <int, String>{};
  final fourK = <int>{};
  bool unknown = false;
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    Object body = [];
    if (options.path.startsWith('/api/requests/')) {
      statusReads.add(options);
      final id = int.parse(options.path.split('/')[3]);
      body = {'status': statuses[id] ?? 'unavailable', 'status_known': !unknown,
        'match': {'tmdb_id': id, 'tvdb_id': 389492, 'state': 'resolved',
          'season_map': {'1': _ids.indexOf(id) + 1}},
        'seasons': [{'season_number': 1, 'status': statuses[id] ?? 'unavailable',
          'episode_file_count': 2, 'episode_count': 8}],
        if (options.queryParameters['include_4k'] == true && fourK.contains(id))
          'is_4k': true,
      };
    } else if (options.path == '/api/discover/tv/top-rated') {
      body = {'page': 1, 'total_pages': 1, 'total_results': 1,
        'results': [{'id': 299939, 'name': 'Lizzie Borden', 'first_air_date': '2026-01-01'}]};
    }
    return ResponseBody.fromString(jsonEncode(body), 200,
      headers: {'content-type': ['application/json']});
  }
  @override
  void close({bool force = false}) {}
}
