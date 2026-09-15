import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/core/widgets/see_all_button.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/data/music_library_service.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_music_tab.dart';
import 'package:cantinarr/features/dashboard/ui/requester_album_detail_screen.dart';
import 'package:cantinarr/features/discover/data/music_discovery_service.dart';
import 'package:cantinarr/features/discover/data/music_models.dart';
import 'package:cantinarr/features/discover/logic/music_browse_query.dart';
import 'package:cantinarr/features/discover/logic/music_feed_provider.dart';
import 'package:cantinarr/features/discover/ui/music_browse_screen.dart';
import 'package:cantinarr/features/discover/ui/music_discovery_row.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const albumA =
    MusicAlbum(foreignId: 'mb-a', title: 'Same title', artist: 'Artist A');
const albumB = MusicAlbum(
    foreignId: 'mb-b',
    title: 'Same title',
    artist: 'Artist B',
    releaseType: 'EP');
const albumC =
    MusicAlbum(foreignId: 'mb-c', title: 'Third album', artist: 'Artist C');
const query = MusicBrowseQuery(feed: 'popular', instanceId: 'music-1');

class FakeMusicService extends MusicDiscoveryService {
  FakeMusicService() : super(Dio());
  final calls = <(MusicBrowseQuery, int)>[];
  Future<MusicPage> Function(MusicBrowseQuery, int)? fetch;
  bool unsupported = false;
  @override
  Future<MusicPage> feed(MusicBrowseQuery query, int page) async {
    calls.add((query, page));
    if (unsupported) throw const MusicDiscoveryUnsupported();
    return fetch == null
        ? MusicPage(
            results: query.feed == 'popular' ? [albumA, albumB] : [albumC],
            page: page)
        : fetch!(query, page);
  }

  @override
  Future<List<MusicGenre>> genres(String? id) async {
    if (unsupported) throw const MusicDiscoveryUnsupported();
    return [
      for (final entry in musicGenreNames.entries)
        MusicGenre(id: entry.key, name: entry.value, tag: entry.key),
    ];
  }

  @override
  Future<MusicAlbum> album(String id, String? instanceId,
          {CancelToken? cancelToken}) async =>
      MusicAlbum(
        foreignId: id,
        title: 'Cold album',
        artist: 'Cold artist',
        releaseType: 'EP',
        releaseDate: '2026-08-30',
        disambiguation: 'A live recording',
      );
}

Future<void> loaded(MusicFeedNotifier notifier) async {
  for (var i = 0; i < 20; i++) {
    await Future<void>.delayed(Duration.zero);
    if (!notifier.state.loading) return;
  }
  fail('music load did not settle');
}

const musicAuth = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    instances: [
      ServiceInstance(
          id: 'music-1', serviceType: 'lidarr', name: 'Music', isDefault: true),
      ServiceInstance(
          id: 'music-2', serviceType: 'lidarr', name: 'Second music'),
    ],
  ),
  user: UserProfile(
      id: 1,
      username: 'listener',
      role: 'user',
      permissions: ['media:discover', 'media:request']),
);

class FakeAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => musicAuth;
}

class Backend implements HttpClientAdapter {
  final ownedAlbums = <Map<String, dynamic>>[];
  final statuses = <String, String>{};
  final statusReads = <(String, String)>[];
  final submissions = <Map<String, dynamic>>[];
  String submissionStatus = 'requested';
  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    Object body = <String, dynamic>{};
    if (options.path == '/api/requests/music-status') {
      final id = options.queryParameters['foreign_id'] as String;
      final instance = options.queryParameters['instance_id'] as String;
      statusReads.add((id, instance));
      body = {
        'status': statuses['$instance:$id'] ?? 'unavailable',
        'status_known': true
      };
    } else if (options.path == '/api/requests/music-recent') {
      body = {
        'items': [
          {
            'album_id': 10,
            'foreign_album_id': 'recent',
            'title': 'Library album',
            'artist': 'Library artist',
            'cover': ''
          }
        ]
      };
    } else if (options.path == '/api/requests/music-artists') {
      body = {
        'artists': [
          {
            'foreign_artist_id': 'artist-id',
            'name': 'Library artist',
            'image': '',
            'album_count': 1,
            'available_count': 0
          }
        ],
        'total': 1
      };
    } else if (options.path == '/api/requests/music-library') {
      body = {'titles': ownedAlbums};
    } else if (options.path.endsWith('/album/lookup')) {
      body = [];
    } else if (options.path == '/api/requests' && options.method == 'POST') {
      final raw = options.data;
      submissions.add(raw is Map<String, dynamic>
          ? raw
          : jsonDecode(raw as String) as Map<String, dynamic>);
      body = {'status': submissionStatus};
    }
    return ResponseBody.fromString(jsonEncode(body), 200, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}

Future<({ProviderContainer container, GoRouter router, Backend backend})>
    pumpMusic(WidgetTester tester, FakeMusicService service,
        {String location = '/dashboard/music'}) async {
  tester.view.physicalSize = const Size(1000, 1600);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final backend = Backend();
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = backend;
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(FakeAuth.new),
    backendClientProvider.overrideWithValue(dio),
    musicDiscoveryServiceProvider.overrideWithValue(service),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  final router = container.read(appRouterProvider);
  await tester.pumpWidget(UncontrolledProviderScope(
      container: container, child: MaterialApp.router(routerConfig: router)));
  router.go(location);
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 300));
  return (container: container, router: router, backend: backend);
}

void main() {
  test('music URLs round trip period, genre and instance without TMDB IDs', () {
    for (final q in [
      query.withPeriod('this_year'),
      const MusicBrowseQuery(
          feed: 'genre', instanceId: 'music-2', genre: 'r-and-b'),
      const MusicBrowseQuery(feed: 'new-releases', instanceId: 'music-1'),
    ]) {
      expect(MusicBrowseQuery.tryParse(Uri.parse(q.location)), q);
    }
    expect(
        MusicBrowseQuery.tryParse(
            Uri.parse('/browse/music/genre?genre=unknown')),
        isNull);
    expect(
        MusicBrowseQuery.tryParse(
            Uri.parse('/browse/music/popular?period=all_time')),
        isNull);
    expect(
        MusicAlbum.fromJson(
                {'foreign_id': 'mbid', 'title': 'Album', 'artist': 'Artist'})
            .foreignId,
        'mbid');
    expect(() => MusicPage.fromJson({'results': [], 'page': 2, 'next_page': 2}),
        throwsFormatException);
  });

  test('filtered pages continue and only identical MBIDs are suppressed',
      () async {
    final service = FakeMusicService()
      ..fetch = (_, page) async => switch (page) {
            1 => const MusicPage(results: [], page: 1, nextPage: 2),
            2 =>
              const MusicPage(results: [albumA, albumB], page: 2, nextPage: 3),
            _ => const MusicPage(results: [albumA, albumC], page: 3),
          };
    final notifier = MusicFeedNotifier(service, query);
    addTearDown(notifier.dispose);
    await loaded(notifier);
    expect(notifier.state.items.map((a) => a.foreignId), ['mb-a', 'mb-b']);
    await notifier.loadMore();
    expect(
        notifier.state.items.map((a) => a.foreignId), ['mb-a', 'mb-b', 'mb-c']);
    expect(service.calls.map((c) => c.$2), [1, 2, 3]);
    expect(notifier.state.nextPage, isNull);
  });

  test('old-server responses differ from retryable provider failures',
      () async {
    for (final status in [200, 404, 502]) {
      final dio = Dio();
      dio.interceptors.add(InterceptorsWrapper(onRequest: (options, handler) {
        if (status == 200) {
          handler.resolve(
              Response(requestOptions: options, data: '<html>old app</html>'));
        } else {
          handler.reject(DioException(
            requestOptions: options,
            response: Response(requestOptions: options, statusCode: status),
            type: DioExceptionType.badResponse,
          ));
        }
      }));
      await expectLater(
          MusicDiscoveryService(dio).feed(query, 1),
          status == 502
              ? throwsA(isA<DioException>())
              : throwsA(isA<MusicDiscoveryUnsupported>()));
      dio.close();
    }
  });

  test('failed refresh retains albums and retries from the first page',
      () async {
    final service = FakeMusicService();
    final notifier = MusicFeedNotifier(service, query);
    addTearDown(notifier.dispose);
    await loaded(notifier);
    service.fetch = (_, __) async => throw StateError('provider down');
    await notifier.refresh();
    expect(notifier.state.items, [albumA, albumB]);
    expect(notifier.state.error, contains('Refresh failed'));
    service.fetch = (_, page) async => MusicPage(results: [albumC], page: page);
    await notifier.retry();
    expect(notifier.state.items, [albumC]);
    expect(service.calls.last.$2, 1);
  });

  test('empty-page traversal is bounded without declaring exhaustion',
      () async {
    final service = FakeMusicService()
      ..fetch = (_, page) async => MusicPage(
          results: [],
          page: page,
          nextPage: page + 1,
          emptyMessage: 'No albums this week on this page.');
    final notifier = MusicFeedNotifier(service, query);
    addTearDown(notifier.dispose);
    await loaded(notifier);
    expect(service.calls.length, 3);
    expect(notifier.state.nextPage, 4);
    await notifier.loadMore();
    expect(service.calls.length, 6);
    expect(notifier.state.nextPage, 7);
  });

  testWidgets('tab orders discovery before library and renders square EP cards',
      (tester) async {
    final service = FakeMusicService();
    await pumpMusic(tester, service);
    await tester.pumpAndSettle();
    final labels = [
      'Popular Albums',
      'New Releases',
      'Browse by genre',
      'Recently Added',
      'Artists'
    ];
    final y = labels
        .map((label) => tester.getTopLeft(find.text(label).last).dy)
        .toList();
    expect(y, [...y]..sort());
    expect(find.text('Artist B · EP'), findsOneWidget);
    final cards = tester.widgetList<MediaCard>(find.byType(MediaCard));
    expect(cards.every((card) => card.artworkAspectRatio == 1), isTrue);
    expect(cards.where((card) => card.title == 'Same title').length, 2);
    expect(cards.first.placeholderIcon, Icons.album);
    await tester.tap(find.text('This month'));
    await tester.pumpAndSettle();
    expect(service.calls.any((c) => c.$1.period == 'this_month'), isTrue);
    await tester.tap(find.byWidgetPredicate(
        (w) => w is SeeAllButton && w.rowTitle == 'Popular Albums'));
    await tester.pumpAndSettle();
    expect(find.byType(MusicBrowseScreen), findsOneWidget);
  });

  testWidgets('one loading or failed feed leaves the other rows usable',
      (tester) async {
    final pending = Completer<MusicPage>();
    final service = FakeMusicService()
      ..fetch = (q, page) async => q.feed == 'popular'
          ? pending.future
          : MusicPage(results: [albumC], page: page);
    await pumpMusic(tester, service);
    expect(find.text('Third album'), findsOneWidget);
    expect(find.text('Library album'), findsOneWidget);
    pending.completeError(StateError('provider down'));
    await tester.pumpAndSettle();
    expect(find.text('Could not load music. Please retry.'), findsOneWidget);
    expect(find.text('Third album'), findsOneWidget);
    service.fetch = (_, page) async => MusicPage(results: [albumA], page: page);
    await tester.tap(find.text('Retry'));
    await tester.pumpAndSettle();
    expect(find.text('Artist A'), findsOneWidget);
  });

  testWidgets('older servers keep music library and search navigation',
      (tester) async {
    await pumpMusic(tester, FakeMusicService()..unsupported = true);
    await tester.pumpAndSettle();
    expect(find.text(musicUpdateMessage), findsWidgets);
    expect(find.text('Library album'), findsOneWidget);
    expect(find.byType(DashboardMusicTab), findsOneWidget);
  });

  testWidgets('genres and See all retain the selected instance in URLs',
      (tester) async {
    final (:router, container: _, backend: _) =
        await pumpMusic(tester, FakeMusicService());
    await tester.pumpAndSettle();
    await tester.tap(find.text('Hip-Hop'));
    await tester.pumpAndSettle();
    final uri = router.routeInformationProvider.value.uri;
    expect(uri.path, '/browse/music/genre');
    expect(uri.queryParameters, {'genre': 'hip-hop', 'instance_id': 'music-1'});
    expect(find.text('Albums and EPs · MusicBrainz matching order'),
        findsOneWidget);
  });

  testWidgets(
      'visible card status follows refresh events and instance identity',
      (tester) async {
    final (:container, router: _, :backend) =
        await pumpMusic(tester, FakeMusicService());
    await tester.pumpAndSettle();
    backend.statuses['music-1:mb-a'] = 'partial';
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    await tester.pumpAndSettle();
    expect(backend.statusReads, contains(('mb-a', 'music-1')));
    expect(
        container
            .read(musicCardStatusProvider((id: 'mb-a', instanceId: 'music-1')))
            .valueOrNull
            ?.status
            .name,
        'partial');
    expect(find.text('Partial'), findsOneWidget);
    expect(backend.statusReads, contains(('mb-a', 'music-1')));
    backend.statuses['music-2:mb-a'] = 'available';
    final secondProvider =
        musicCardStatusProvider((id: 'mb-a', instanceId: 'music-2'));
    final subscription = container.listen(secondProvider, (_, __) {});
    addTearDown(subscription.close);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(subscription.read().valueOrNull?.status.name, 'available');
    expect(
        container
            .read(musicCardStatusProvider((id: 'mb-a', instanceId: 'music-1')))
            .valueOrNull
            ?.status
            .name,
        'partial');
  });

  testWidgets('cold album links resolve by ID without a title hint',
      (tester) async {
    await pumpMusic(tester, FakeMusicService(),
        location: '/detail/album/mb-cold?instance_id=music-2');
    await tester.pumpAndSettle();
    expect(find.byType(RequesterAlbumDetailScreen), findsOneWidget);
    expect(find.text('Cold album'), findsOneWidget);
    expect(find.text('Cold artist'), findsOneWidget);
    expect(find.text('2026 · EP'), findsOneWidget);
    expect(find.text('A live recording'), findsOneWidget);
  });

  testWidgets('discovery cover survives the album becoming owned',
      (tester) async {
    final (:router, :backend, :container) =
        await pumpMusic(tester, FakeMusicService());
    await tester.pumpAndSettle();
    const album = MusicAlbum(
        foreignId: 'with-cover',
        title: 'Discovery title',
        artist: 'Artist',
        artwork: '/api/discover/music/artwork/with-cover');
    router.push(album.detailLocation('music-1'), extra: album);
    await tester.pumpAndSettle();
    expect(find.text('Discovery title'), findsOneWidget);
    backend.ownedAlbums.add({
      'foreign_album_id': 'with-cover',
      'title': 'Library title',
      'artist': 'Library artist',
      'monitored': true,
      'cover': '/mediacover/album/1/cover.jpg',
    });
    backend.statuses['music-1:with-cover'] = 'requested';
    container.invalidate(ownedAlbumsForInstanceProvider('music-1'));
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    await tester.pumpAndSettle();
    final cover = tester.widget<CachedImage>(find
        .descendant(
            of: find.byType(RequesterAlbumDetailScreen),
            matching: find.byType(CachedImage))
        .first);
    expect(cover.url,
        'http://localhost/api/discover/music/artwork/with-cover?instance_id=music-1');
    expect(cover.headers, {'Authorization': 'Bearer access'});
    expect(find.text('Library title'), findsOneWidget);
    expect(find.text('Request'), findsNothing);
  });

  for (final status in ['requested', 'pending']) {
    testWidgets('discovered album enters the existing $status request path',
        (tester) async {
      final (:router, container: _, :backend) =
          await pumpMusic(tester, FakeMusicService());
      await tester.pumpAndSettle();
      backend.submissionStatus = status;
      await tester.tap(find.byType(MusicDiscoveryCard).first);
      await tester.pumpAndSettle();
      expect(
          router.routeInformationProvider.value.uri.path, '/detail/album/mb-a');
      expect(find.text('Artist A'), findsOneWidget);
      await tester.tap(find.text('Request'));
      await tester.pumpAndSettle();
      expect(backend.submissions.single, containsPair('foreign_id', 'mb-a'));
      expect(backend.submissions.single, containsPair('media_type', 'music'));
      expect(
          backend.submissions.single, containsPair('instance_id', 'music-1'));
      expect(backend.submissions.single, containsPair('title', 'Same title'));
    });
  }

  testWidgets('grid pagination and back navigation restore albums and scroll',
      (tester) async {
    final service = FakeMusicService()
      ..fetch = (_, page) async => MusicPage(
            page: page,
            nextPage: page == 1 ? 2 : null,
            results: List.generate(
                30,
                (i) => MusicAlbum(
                    foreignId: 'mb-${page * 30 + i}',
                    title: 'Album ${page * 30 + i}',
                    artist: 'Artist')),
          );
    final (:router, container: _, :backend) = await pumpMusic(tester, service,
        location: '/browse/music/popular?period=this_year&instance_id=music-2');
    await tester.pumpAndSettle();
    final scrollFinder = find.descendant(
        of: find.byType(MusicBrowseScreen),
        matching: find.byType(CustomScrollView));
    await tester.drag(scrollFinder, const Offset(0, -1100));
    await tester.pumpAndSettle();
    final position = tester
        .state<ScrollableState>(find
            .descendant(of: scrollFinder, matching: find.byType(Scrollable))
            .first)
        .position;
    final before = position.pixels;
    expect(before, greaterThan(0));
    final card = find.byType(MusicDiscoveryCard).hitTestable().first;
    await tester.tap(card);
    await tester.pumpAndSettle();
    expect(find.byType(RequesterAlbumDetailScreen), findsOneWidget);
    router.pop();
    await tester.pumpAndSettle();
    expect(position.pixels, before);
    expect(router.routeInformationProvider.value.uri.queryParameters['period'],
        'this_year');
    expect(backend.statusReads.every((read) => read.$2 == 'music-2'), isTrue);
    await tester.drag(scrollFinder, const Offset(0, -1800));
    await tester.pumpAndSettle();
    expect(service.calls.any((call) => call.$2 == 2), isTrue);
  });
}
