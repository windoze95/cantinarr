import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/shell/logic/shell_music_search_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('no active Lidarr instance short-circuits before any request',
      () async {
    final adapter = _LookupAdapter();
    final container =
        await _makeContainer(authState: _noInstanceState, adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellMusicSearchProvider.notifier);
    notifier.updateSearch('pinkerton');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellMusicSearchProvider).error,
      MusicSearchError.noInstance,
    );
    expect(adapter.albumLookupRequests, 0);
  });

  test('a 403 response classifies as forbidden', () async {
    final adapter = _LookupAdapter(statusCode: 403);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellMusicSearchProvider.notifier);
    notifier.updateSearch('pinkerton');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellMusicSearchProvider).error,
      MusicSearchError.forbidden,
    );
  });

  test('a 500 response classifies as requestFailed', () async {
    final adapter = _LookupAdapter(statusCode: 500);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellMusicSearchProvider.notifier);
    notifier.updateSearch('pinkerton');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellMusicSearchProvider).error,
      MusicSearchError.requestFailed,
    );
  });

  test('an empty result list is a successful, matched-nothing search',
      () async {
    final adapter = _LookupAdapter(albums: const []);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellMusicSearchProvider.notifier);
    notifier.updateSearch('pinkerton');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    final state = container.read(shellMusicSearchProvider);
    expect(state.error, isNull);
    expect(state.searched, true);
    expect(state.results, isEmpty);
  });

  test('albums and artists arrive together on a populated search', () async {
    final adapter = _LookupAdapter(albums: _twoAlbums, artists: _oneArtist);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellMusicSearchProvider.notifier);
    notifier.updateSearch('weezer');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    final state = container.read(shellMusicSearchProvider);
    expect(state.error, isNull);
    expect(state.results.length, 2);
    expect(state.artists.length, 1);
    expect(state.artistsUnavailable, false);
  });

  test(
      'a failed artist lookup keeps the album results and says artists were '
      'not searched — absence vs blindness', () async {
    final adapter = _LookupAdapter(albums: _twoAlbums, artistStatusCode: 500);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellMusicSearchProvider.notifier);
    notifier.updateSearch('weezer');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    final state = container.read(shellMusicSearchProvider);
    expect(state.error, isNull, reason: 'album results are still usable');
    expect(state.results.length, 2);
    expect(state.artists, isEmpty);
    expect(state.artistsUnavailable, true,
        reason: 'an empty artist section must say "could not look", never '
            '"nobody matched"');
  });

  test(
      'supersede: an abandoned keystroke\'s failure never lands over a '
      'later successful result', () async {
    final adapter = _LookupAdapter(
      termStatusCode: const {'a': 403},
      termAlbums: {'ab': _twoAlbums},
    );
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);
    final notifier = container.read(shellMusicSearchProvider.notifier);

    notifier.updateSearch('a');
    notifier.updateSearch('ab');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    final state = container.read(shellMusicSearchProvider);
    expect(state.error, isNull);
    expect(state.results.length, 2);
  });

  test('an empty query resets to a fresh state and fires no request', () async {
    final adapter = _LookupAdapter(albums: _twoAlbums);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);
    final notifier = container.read(shellMusicSearchProvider.notifier);

    notifier.updateSearch('');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellMusicSearchProvider),
      const ShellMusicSearchState(),
    );
    expect(adapter.albumLookupRequests, 0);
  });

  test(
      'a malformed lookup payload is distinguishable from a transport '
      'failure in a debug build, without leaking hosts or terms', () async {
    final malformedAlbums = [
      {
        'id': 'not-an-int',
        'title': 'Pinkerton',
        'foreignAlbumId': 'mb-1',
      },
    ];
    final adapter = _LookupAdapter(albums: malformedAlbums);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final captured = <String>[];
    final originalDebugPrint = debugPrint;
    debugPrint = (String? message, {int? wrapWidth}) {
      if (message != null) captured.add(message);
    };
    addTearDown(() => debugPrint = originalDebugPrint);

    final notifier = container.read(shellMusicSearchProvider.notifier);
    notifier.updateSearch('pinkerton');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellMusicSearchProvider).error,
      MusicSearchError.requestFailed,
      reason: 'the user-facing state is unchanged by the diagnostic',
    );

    final output = captured.join('\n');
    expect(output, contains('TypeError'));
    expect(output, isNot(contains('http')),
        reason: 'the diagnostic must never emit a host or URL');
    expect(output, isNot(contains('pinkerton')),
        reason: 'the diagnostic must never emit the search term');
  });
}

const _music = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    instances: [
      ServiceInstance(
        id: 'music',
        serviceType: 'lidarr',
        name: 'Music',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// The grant is present (the route stays reachable) but no Lidarr instance is
/// configured.
const _noInstanceState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    instances: [],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

final _twoAlbums = [
  {
    'title': 'Pinkerton',
    'foreignAlbumId': 'mb-1',
    'artist': {'id': 0, 'artistName': 'Weezer', 'foreignArtistId': 'a-1'},
  },
  {
    'title': 'Blue Album',
    'foreignAlbumId': 'mb-2',
    'artist': {'id': 0, 'artistName': 'Weezer', 'foreignArtistId': 'a-1'},
  },
];

final _oneArtist = [
  {'id': 0, 'artistName': 'Weezer', 'foreignArtistId': 'a-1'},
];

Future<ProviderContainer> _makeContainer({
  AuthState? authState,
  required _LookupAdapter adapter,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(authState ?? _music)),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  await container.read(authProvider.future);
  return container;
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

/// Fakes `GET .../album/lookup` and `.../artist/lookup`, either uniformly or
/// per search term — the latter is what lets the supersede case answer two
/// different queries differently on the same notifier instance.
class _LookupAdapter implements HttpClientAdapter {
  _LookupAdapter({
    this.statusCode = 200,
    this.albums = const [],
    this.artists = const [],
    this.artistStatusCode,
    this.termStatusCode,
    this.termAlbums,
  });

  final int statusCode;
  final List<Map<String, dynamic>> albums;
  final List<Map<String, dynamic>> artists;
  final int? artistStatusCode;
  final Map<String, int>? termStatusCode;
  final Map<String, List<Map<String, dynamic>>>? termAlbums;

  int albumLookupRequests = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path.endsWith('/artist/lookup')) {
      return ResponseBody.fromString(
        jsonEncode(artistStatusCode == null ? artists : const []),
        artistStatusCode ?? 200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    if (!options.path.endsWith('/album/lookup')) {
      return ResponseBody.fromString(
        '{}',
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    albumLookupRequests++;
    final term = options.queryParameters['term']?.toString() ?? '';
    final code = termStatusCode?[term] ?? statusCode;
    final body = termAlbums?[term] ?? albums;
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
