import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/logic/library_snapshot_provider.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _moviesPath = '/api/instances/movies/api/v3/movie';
const _seriesPath = '/api/instances/shows/api/v3/series';

void main() {
  test('a seeded snapshot is trusted: refresh costs nothing', () async {
    final h = await _harness();
    h.notifier.seed(movies: [_movie(1)]);
    expect(h.container.read(librarySnapshotProvider).movies, hasLength(1));

    await h.notifier.refresh();
    expect(h.adapter.requested, isEmpty);
  });

  test('an empty snapshot fetches both libraries once, then rests', () async {
    final h = await _harness();
    await h.notifier.refresh();
    expect(h.adapter.requested, [_moviesPath, _seriesPath]);
    final snapshot = h.container.read(librarySnapshotProvider);
    expect(snapshot.movies.single.tmdbId, 603);
    expect(snapshot.series.single.title, 'Severance');

    await h.notifier.refresh();
    expect(h.adapter.requested, hasLength(2));
  });

  test('a forced refresh fetches even while fresh', () async {
    final h = await _harness();
    await h.notifier.refresh();
    await h.notifier.refresh(force: true);
    expect(h.adapter.requested, hasLength(4));
  });

  test('concurrent readers share one fetch', () async {
    final h = await _harness();
    await Future.wait([h.notifier.refresh(), h.notifier.refresh()]);
    expect(h.adapter.requested, hasLength(2));
  });

  test('an unreadable library keeps the last good list', () async {
    final h = await _harness();
    h.notifier.seed(movies: [_movie(1)]);
    h.adapter.failMovies = true;

    await h.notifier.refresh(force: true);
    final snapshot = h.container.read(librarySnapshotProvider);
    expect(snapshot.movies.single.tmdbId, 1);
    expect(snapshot.series, hasLength(1));
  });

  test('a server switch discards the old server\'s libraries', () async {
    final h = await _harness();
    h.notifier.seed(movies: [_movie(1)]);
    expect(h.container.read(librarySnapshotProvider).serverUrl,
        'http://localhost');

    h.auth.switchTo(_state.copyWith(
      connection: _state.connection!.copyWith(serverUrl: 'http://elsewhere'),
    ));
    await h.container.pump();
    // Fresh by age, but taken against another server: refetched, not kept.
    await h.notifier.refresh();
    final snapshot = h.container.read(librarySnapshotProvider);
    expect(snapshot.serverUrl, 'http://elsewhere');
    expect(snapshot.movies.single.tmdbId, 603);
    expect(h.adapter.requested, [_moviesPath, _seriesPath]);
  });
}

RadarrMovie _movie(int tmdbId) => RadarrMovie.fromJson({
      'id': tmdbId,
      'title': 'Movie $tmdbId',
      'tmdbId': tmdbId,
      'monitored': true,
      'hasFile': true,
      'images': <Object>[],
    });

Future<
    ({
      ProviderContainer container,
      LibrarySnapshotNotifier notifier,
      _Adapter adapter,
      _SwitchableAuth auth,
    })> _harness() async {
  final adapter = _Adapter();
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  final auth = _SwitchableAuth(_state);
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => auth),
    backendClientProvider.overrideWithValue(dio),
  ]);
  addTearDown(container.dispose);
  // Resolve sign-in before the snapshot reads it, as the app has by the time
  // a tab or grid mounts.
  await container.read(authProvider.future);
  return (
    container: container,
    notifier: container.read(librarySnapshotProvider.notifier),
    adapter: adapter,
    auth: auth,
  );
}

const _state = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(radarr: true, sonarr: true),
    instances: [
      ServiceInstance(
        id: 'movies',
        serviceType: 'radarr',
        name: 'Movies',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'shows',
        serviceType: 'sonarr',
        name: 'Shows',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

class _SwitchableAuth extends AuthNotifier {
  _SwitchableAuth(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;

  void switchTo(AuthState next) => state = AsyncData(next);
}

class _Adapter implements HttpClientAdapter {
  final List<String> requested = [];
  bool failMovies = false;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requested.add(options.path);
    if (options.path == _moviesPath && failMovies) {
      return ResponseBody.fromString('{"error":"down"}', 503, headers: {
        'content-type': ['application/json'],
      });
    }
    final Object body = switch (options.path) {
      _moviesPath => [
          {
            'id': 9,
            'title': 'The Matrix',
            'tmdbId': 603,
            'monitored': true,
            'hasFile': true,
            'images': <Object>[],
          },
        ],
      _seriesPath => [
          {
            'id': 3,
            'title': 'Severance',
            'tmdbId': 95396,
            'monitored': true,
            'seasons': <Object>[],
            'images': <Object>[],
          },
        ],
      _ => <Object>[],
    };
    return ResponseBody.fromString(jsonEncode(body), 200, headers: {
      'content-type': ['application/json'],
    });
  }

  @override
  void close({bool force = false}) {}
}
