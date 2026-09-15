import 'dart:async';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/requester_album_detail_screen.dart';
import 'package:cantinarr/features/dashboard/ui/requester_artist_detail_screen.dart';
import 'package:cantinarr/features/discover/data/music_models.dart';
import 'package:cantinarr/features/discover/ui/music_search_results_view.dart';
import 'package:cantinarr/features/shell/logic/shell_music_search_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const albumID = '11111111-1111-4111-8111-111111111111';
const artistID = '22222222-2222-4222-8222-222222222222';
const authState = AuthState(
    connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'a',
        refreshToken: 'r',
        services: AvailableServices(lidarr: true),
        instances: [
          ServiceInstance(
              id: 'music',
              serviceType: 'lidarr',
              name: 'Music',
              isDefault: true)
        ]),
    user: UserProfile(
        id: 1,
        username: 'listener',
        role: 'user',
        permissions: ['media:discover', 'media:request']));

class TestAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => authState;
}

class MusicFixture {
  late final Dio dio;
  final calls = <RequestOptions>[];
  final heldArtists = Completer<Map<String, dynamic>>();
  final heldLibrary = Completer<Map<String, dynamic>>();
  bool stallArtists = false;
  bool stallLibrary = false;
  bool failSaved = false;
  bool failTruth = false;
  bool pending = false;
  String? deliveryState;
  Map<String, dynamic> get receipt => {
        'request_id': 1,
        'status': pending ? 'pending' : 'requested',
        'status_known': false,
        'delivery': [
          {
            'request_id': 1,
            'state': deliveryState ?? (pending ? 'approval' : 'queued'),
            'message': 'Your request is saved.',
            'can_manage': true,
            'can_cancel': true
          }
        ]
      };
  MusicFixture() {
    dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.interceptors.add(InterceptorsWrapper(onRequest: (o, h) async {
      calls.add(o);
      Map<String, dynamic> data = {};
      if (o.path.endsWith('/music/search')) {
        data = {
          'page': o.queryParameters['page'] ?? 1,
          'results': [
            for (var i = 0; i < 24; i++)
              {
                'foreign_id': i == 0 ? albumID : 'album-$i',
                'title': i < 2 ? 'Same title' : 'Album $i',
                'artist': 'Artist',
                'release_type': i == 1 ? 'Single' : 'Album'
              }
          ],
          if (o.queryParameters['page'] == 1) 'next_page': 2
        };
      } else if (o.path.endsWith('/music/artists')) {
        data = stallArtists
            ? await heldArtists.future
            : {
                'page': 1,
                'results': [
                  {
                    'foreign_id': artistID,
                    'name': 'Same artist',
                    'disambiguation': 'UK band'
                  },
                  {
                    'foreign_id': 'artist-other',
                    'name': 'Same artist',
                    'disambiguation': 'US singer'
                  }
                ]
              };
      } else if (o.path.endsWith('/music-library')) {
        data = stallLibrary ? await heldLibrary.future : {'titles': []};
      } else if (o.path.endsWith('/music-saved')) {
        data = {
          'requests': deliveryState == null ? [] : [receipt]
        };
      } else if (o.path.endsWith('/music-status')) {
        if (failTruth) {
          h.reject(DioException(requestOptions: o));
          return;
        }
        data = {'status': 'unavailable'};
      } else if (o.path.endsWith('/delivery-status')) {
        if (failSaved) {
          h.reject(DioException(requestOptions: o));
          return;
        }
        data =
            deliveryState == null ? {'request_id': 0, 'delivery': []} : receipt;
      } else if (o.path == '/api/requests' && o.method == 'POST') {
        deliveryState = pending ? 'approval' : 'queued';
        data = receipt;
      } else if (o.path.endsWith('/delivery')) {
        deliveryState =
            (o.data as Map)['action'] == 'cancel' ? 'cancelled' : 'queued';
        data = receipt;
      } else if (o.path.contains('/media/music/artists/') &&
          o.path.endsWith('/albums')) {
        data = {
          'page': o.queryParameters['page'] ?? 1,
          'results': [
            {
              'foreign_id': albumID,
              'title': 'Exact artist album',
              'artist': 'Same artist',
              'release_type': 'Single',
              'artists': [
                {'foreign_id': artistID, 'name': 'Same artist'}
              ]
            }
          ],
          if (o.queryParameters['page'] == 1) 'next_page': 2
        };
      } else if (o.path.contains('/media/music/artists/')) {
        data = {
          'foreign_id': artistID,
          'name': 'Same artist',
          'disambiguation': 'UK band'
        };
      } else if (o.path.contains('/media/music/')) {
        data = {
          'foreign_id': albumID,
          'title': 'Cold single',
          'artist': 'Same artist',
          'release_type': 'Single',
          'artists': [
            {'foreign_id': artistID, 'name': 'Same artist'}
          ]
        };
      } else if (o.path.endsWith('/music-artist')) {
        h.reject(DioException(
            requestOptions: o,
            response: Response(requestOptions: o, statusCode: 404),
            type: DioExceptionType.badResponse));
        return;
      } else if (o.path.endsWith('/artist')) {
        h.resolve(Response(requestOptions: o, statusCode: 200, data: []));
        return;
      }
      h.resolve(Response(requestOptions: o, statusCode: 200, data: data));
    }));
  }
  void dispose() {
    if (!heldArtists.isCompleted) {
      heldArtists.complete({'page': 1, 'results': []});
    }
    if (!heldLibrary.isCompleted) heldLibrary.complete({'titles': []});
    dio.close(force: true);
  }
}

class SearchHarness extends ConsumerWidget {
  const SearchHarness({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final s = ref.watch(shellMusicSearchProvider);
    return Scaffold(
        body: MusicSearchResultsView(
            results: s.results,
            artists: s.artists,
            query: s.searchQuery,
            isLoading: s.isLoadingSearch,
            artistsLoading: s.artistsLoading,
            searched: s.searched,
            error: s.error,
            artistsUnavailable: s.artistsUnavailable));
  }
}

Future<ProviderContainer> pumpFixture(WidgetTester tester, MusicFixture fixture,
    {Widget? child, String initial = '/'}) async {
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(TestAuth.new),
    backendClientProvider.overrideWithValue(fixture.dio)
  ]);
  await container.read(authProvider.future);
  final router = GoRouter(initialLocation: initial, routes: [
    GoRoute(path: '/', builder: (_, __) => child ?? const SearchHarness()),
    GoRoute(
        path: '/detail/album/:id',
        builder: (_, s) => RequesterAlbumDetailScreen(
            foreignId: s.pathParameters['id']!,
            instanceId: s.uri.queryParameters['instance_id'],
            discoveryAlbum:
                s.extra is MusicAlbum ? s.extra as MusicAlbum : null)),
    GoRoute(
        path: '/detail/artist/:id',
        builder: (_, s) => RequesterArtistDetailScreen(
            foreignArtistId: s.pathParameters['id']!,
            instanceId: s.uri.queryParameters['instance_id']))
  ]);
  addTearDown(() => router.dispose());
  addTearDown(container.dispose);
  addTearDown(fixture.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
      container: container, child: MaterialApp.router(routerConfig: router)));
  await tester.pump();
  return container;
}

void main() {
  for (final width in [390.0, 1200.0]) {
    testWidgets(
        'albums render within 300 ms with artists and status stalled at width $width',
        (tester) async {
      tester.view.physicalSize = Size(width, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final fixture = MusicFixture()
        ..stallArtists = true
        ..stallLibrary = true;
      final container = await pumpFixture(tester, fixture);
      container
          .read(shellMusicSearchProvider.notifier)
          .updateSearch('Same title');
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pump(const Duration(milliseconds: 1));
      expect(find.text('Same title'), findsNWidgets(2));
      expect(container.read(shellMusicSearchProvider).isLoadingSearch, false);
      expect(container.read(shellMusicSearchProvider).artistsLoading, true);
      expect(find.byType(TabBar), findsNothing);
      final before = tester.getTopLeft(find.text('Same title').first);
      fixture.heldLibrary.complete({
        'titles': [
          {
            'record_id': 7,
            'foreign_album_id': albumID,
            'title': 'Same title',
            'artist': 'Artist',
            'status': 'available',
            'downloaded': true,
            'monitored': true
          },
          {
            'record_id': 8,
            'foreign_album_id': 'library-only',
            'title': 'Same title library only',
            'artist': 'Artist'
          }
        ]
      });
      await tester.pump();
      await tester.pump();
      expect(tester.getTopLeft(find.text('Same title').first), before);
      expect(find.text('Available'), findsOneWidget);
      expect(
          fixture.calls
              .firstWhere((o) => o.path.endsWith('/music/search'))
              .queryParameters['include_singles'],
          true);
      fixture.heldArtists.complete({'page': 1, 'results': []});
      await tester.pump();
      await tester.pumpWidget(const SizedBox());
    });
  }
  testWidgets('artist deadline preserves albums and labels the failed read',
      (tester) async {
    final fixture = MusicFixture()..stallArtists = true;
    final container = await pumpFixture(tester, fixture);
    container
        .read(shellMusicSearchProvider.notifier)
        .updateSearch('Same title');
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pump();
    expect(find.text('Same title'), findsNWidgets(2));
    await tester.pump(const Duration(seconds: 10));
    expect(container.read(shellMusicSearchProvider).artistsLoading, false);
    expect(container.read(shellMusicSearchProvider).artistsUnavailable, true);
    expect(find.text('Same title'), findsNWidgets(2));
    fixture.heldArtists.complete({'page': 1, 'results': []});
    await tester.pump();
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('search back navigation restores query and scroll',
      (tester) async {
    final fixture = MusicFixture();
    final container = await pumpFixture(tester, fixture);
    container.read(shellMusicSearchProvider.notifier).updateSearch('Album');
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pump();
    await tester.drag(find.byType(ListView).first, const Offset(0, -550));
    await tester.pumpAndSettle();
    final scroll =
        container.read(shellMusicSearchProvider.notifier).scrollOffset;
    expect(scroll, greaterThan(100));
    await tester.tap(find.text('Album 7'));
    await tester.pumpAndSettle();
    expect(find.text('Album details'), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(container.read(shellMusicSearchProvider).searchQuery, 'Album');
    expect(container.read(shellMusicSearchProvider.notifier).scrollOffset,
        closeTo(scroll, 1));
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets(
      'cold artist links use exact ID and paginate without Lidarr artist',
      (tester) async {
    final fixture = MusicFixture();
    await pumpFixture(tester, fixture,
        initial: '/detail/artist/$artistID?instance_id=music');
    await tester.pumpAndSettle();
    expect(find.text('Exact artist album'), findsOneWidget);
    expect(
        fixture.calls.where((o) => o.path.contains('/artist/lookup')), isEmpty);
    await tester.tap(find.text('More releases'));
    await tester.pumpAndSettle();
    final discography =
        fixture.calls.where((o) => o.path.endsWith('/albums')).toList();
    expect(discography.length, 2);
    expect(discography.last.queryParameters['page'], 2);
    expect(discography.every((o) => o.path.contains(artistID)), true);
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets(
      'request receipt and one control survive polling failures and approval',
      (tester) async {
    final fixture = MusicFixture()..pending = true;
    final container = await pumpFixture(tester, fixture,
        initial: '/detail/album/$albumID?instance_id=music');
    await tester.pumpAndSettle();
    expect(find.text('Cold single'), findsOneWidget);
    expect(find.text('Request'), findsOneWidget);
    await tester.tap(find.text('Request'));
    await tester.pumpAndSettle();
    expect(find.text('Waiting for approval'), findsOneWidget);
    final submission = fixture.calls
        .singleWhere((o) => o.path == '/api/requests' && o.method == 'POST');
    expect((submission.data as Map)['foreign_id'], albumID);
    expect((submission.data as Map).containsKey('catalog_ref'), false);
    fixture.failSaved = true;
    fixture.failTruth = true;
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();
    expect(find.text('Waiting for approval'), findsOneWidget);
    expect(find.text('Request'), findsNothing);
    expect(find.text('Cancel request'), findsOneWidget);
    fixture.failSaved = false;
    fixture.pending = false;
    fixture.deliveryState = 'queued';
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();
    expect(find.text('Requested'), findsOneWidget);
    expect(
        fixture.calls
            .where((o) => o.path.endsWith('/delivery-status'))
            .every((o) => o.queryParameters['include_live'] == false),
        true);
    await tester.pumpWidget(const SizedBox());
  });
}
