import 'dart:async';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/music_discovery_service.dart';
import 'package:cantinarr/features/discover/data/music_models.dart';
import 'package:cantinarr/features/discover/logic/music_browse_query.dart';
import 'package:cantinarr/features/discover/logic/music_feed_provider.dart';
import 'package:cantinarr/features/discover/ui/catalog_prefetch.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class Music extends MusicDiscoveryService {
  Music() : super(Dio());
  final calls = <(String, int)>[];
  Future<MusicPage> Function(MusicBrowseQuery, int)? fetch;
  @override
  Future<MusicPage> feed(MusicBrowseQuery query, int page) async {
    calls.add((query.feed, page));
    return fetch == null ? musicPage(page) : fetch!(query, page);
  }

  @override
  Future<List<MusicGenre>> genres(String? id) async => const [];
}

MusicPage musicPage(int page) =>
    MusicPage(page: page, nextPage: page < 5 ? page + 1 : null, results: [
      MusicAlbum(
          foreignId: 'album-$page',
          title: 'Album $page',
          artist: 'An Artist',
          artwork: '/artwork/album-$page')
    ]);

AuthState auth(
        {String role = 'admin',
        bool child = false,
        bool capability = true,
        bool grant = false}) =>
    AuthState(
      connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'test-token',
          refreshToken: 'test-refresh',
          adminCatalogBrowsing: capability,
          instances: grant
              ? const [
                  ServiceInstance(
                      id: 'books',
                      name: 'Books',
                      serviceType: 'chaptarr',
                      isDefault: true),
                  ServiceInstance(
                      id: 'music',
                      name: 'Music',
                      serviceType: 'lidarr',
                      isDefault: true),
                ]
              : const []),
      user: UserProfile(
          id: 1,
          username: 'test',
          role: role,
          child: child,
          permissions: const ['media:discover']),
    );

class Auth extends AuthNotifier {
  final AuthState initial;
  Auth(this.initial);
  @override
  Future<AuthState> build() async => initial;
  void change(AuthState next) => state = AsyncData(next);
}

Future<void> flush() async {
  for (var i = 0; i < 8; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  test('music consumes a ready page without fetching it again', () async {
    final service = Music();
    final feed =
        MusicFeedNotifier(service, const MusicBrowseQuery(feed: 'popular'));
    addTearDown(feed.dispose);
    await flush();
    expect(service.calls, [('popular', 1)]);
    feed.enablePrefetch();
    await flush();
    expect(feed.state.items.single.foreignId, 'album-1');
    expect(feed.state.upcoming.single.foreignId, 'album-2');
    await feed.loadMore();
    await flush();
    expect(feed.state.items.map((a) => a.foreignId), ['album-1', 'album-2']);
    expect(service.calls, [('popular', 1), ('popular', 2), ('popular', 3)]);
  });

  test(
      'access errors during lookahead clear both visible and buffered metadata',
      () async {
    final music = Music()
      ..fetch = (_, page) async => page == 2
          ? throw DioException(
              requestOptions: RequestOptions(),
              response:
                  Response(requestOptions: RequestOptions(), statusCode: 403))
          : musicPage(page);
    final m = MusicFeedNotifier(music, const MusicBrowseQuery(feed: 'popular'));
    addTearDown(m.dispose);
    await flush();
    m.enablePrefetch();
    await flush();
    expect(m.state.items, isEmpty);
    expect(m.state.upcoming, isEmpty);
    expect(m.state.error, isNotNull);
  });

  testWidgets(
      'music rows warm before tabs with authenticated artwork and no target lookups',
      (t) async {
    final music = Music();
    final images = <ImageSource>[];
    final pending = <Completer<void>>[];
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => Auth(auth(grant: true))),
      musicDiscoveryServiceProvider.overrideWithValue(music),
      catalogArtworkLoaderProvider.overrideWithValue((source, _) {
        images.add(source);
        final c = Completer<void>();
        pending.add(c);
        return c.future;
      }),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    await t.pumpWidget(UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: CatalogWarmup(child: Text('Movies')))));
    await t.pumpAndSettle();
    expect(music.calls, [('popular', 1), ('new-releases', 1)]);
    expect(images.length, 1);
    expect(images.every((s) => s.headers?['Authorization'] == 'Bearer test-token'), isTrue);
    pending.first.complete();
    await t.pump();
    expect(images.length, 1);
    // Revocation must drop queued artwork and all warmed feeds immediately.
    (container.read(authProvider.notifier) as Auth)
        .change(auth(role: 'user', child: true));
    await t.pumpAndSettle();
    expect(container.read(catalogOpeningArtworkProvider), isEmpty);
    for (final c in pending.where((c) => !c.isCompleted)) {
      c.complete();
    }
    await t.pumpAndSettle();
    expect(images.length, 1);
  });

  for (final account in [
    auth(role: 'user'),
    auth(role: 'user', child: true),
    auth(capability: false)
  ]) {
    test(
        'warmup respects grants and older-server capability: ${account.user?.role}/${account.user?.child}/${account.connection?.adminCatalogBrowsing}',
        () async {
      final music = Music();
      final container = ProviderContainer(overrides: [
        authProvider.overrideWith(() => Auth(account)),
        musicDiscoveryServiceProvider.overrideWithValue(music),
      ]);
      addTearDown(container.dispose);
      await container.read(authProvider.future);
      final sub = container.listen(catalogOpeningArtworkProvider, (_, __) {});
      addTearDown(sub.close);
      await flush();
      expect(music.calls, isEmpty);
    });
  }
}
