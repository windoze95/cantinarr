import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/requester_album_detail_screen.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// The requester album detail surface, exercised through the real router with
/// a faked backend: an owned digest row resolves ownership, a deep link
/// resolves lookup metadata, a request submits the music wire shape, and a
/// dead id degrades to a graceful not-found state that points back to the
/// Music tab.
void main() {
  testWidgets('a deep link resolves lookup metadata and offers a request',
      (tester) async {
    final adapter = _MusicAdapter();
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();

    expect(find.byType(RequesterAlbumDetailScreen), findsOneWidget);
    expect(find.text('Pinkerton'), findsOneWidget);
    expect(find.text('Weezer'), findsOneWidget);
    expect(find.text('1996 · Album · 10 tracks'), findsOneWidget);
    expect(find.text('Alternative Rock'), findsOneWidget);
    expect(find.text('A dark second record.'), findsOneWidget);
    // Unrequested and unowned: the one action is Request.
    expect(find.text('Request'), findsOneWidget);
  });

  testWidgets('tapping Request submits the music wire shape', (tester) async {
    final adapter = _MusicAdapter();
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/album/mb-1?title=Pinkerton&q=weezer%20pinkerton');
    await tester.pumpAndSettle();

    await tester.tap(find.text('Request'));
    await tester.pumpAndSettle();

    final body = adapter.submissions.single;
    expect(body['media_type'], 'music');
    expect(body['foreign_id'], 'mb-1');
    expect(body['title'], 'Pinkerton');
    expect(body['search_term'], 'weezer pinkerton',
        reason: 'the proven search travels with the request');
    expect(body.containsKey('book_format'), false);
    expect(find.text('Requested'), findsOneWidget);
  });

  testWidgets('an owned digest row reads Available without a lookup match',
      (tester) async {
    final adapter = _MusicAdapter(
      owned: const [
        {
          'title': 'Pinkerton',
          'artist': 'Weezer',
          'year': 1996,
          'foreign_album_id': 'mb-1',
          'cover': '',
          'monitored': true,
          'downloaded': true,
        },
      ],
      lookupAlbums: const [],
    );
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();

    expect(find.text('Pinkerton'), findsOneWidget);
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Request'), findsNothing);
  });

  testWidgets('a dead id degrades to a not-found state pointing at Music',
      (tester) async {
    final adapter = _MusicAdapter(lookupAlbums: const []);
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/album/mb-gone');
    await tester.pumpAndSettle();

    expect(find.textContaining('could not be found'), findsOneWidget);
    expect(find.text('Browse Music'), findsOneWidget);
  });

  testWidgets('an unreadable status renders no Request button', (tester) async {
    final adapter = _MusicAdapter(statusCode: 500);
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();

    expect(find.text('Request'), findsNothing,
        reason: 'an outage must not mint requests');
    expect(find.textContaining('could not be read'), findsOneWidget);
  });

  testWidgets('a downloads-enabled instance offers labelled per-track rows',
      (tester) async {
    final adapter = _MusicAdapter(
      owned: const [
        {
          'title': 'Pinkerton',
          'artist': 'Weezer',
          'year': 1996,
          'foreign_album_id': 'mb-1',
          'cover': '',
          'monitored': true,
          'downloaded': true,
        },
      ],
    )
      ..libraryAlbums = const [
        {'id': 9, 'title': 'Pinkerton', 'artistId': 4, 'foreignAlbumId': 'mb-1'},
      ]
      ..trackFiles = const [
        {
          'id': 72,
          'albumId': 9,
          'path': '/music/Weezer/Pinkerton/02 - Getchoo.flac',
          'size': 31000000,
          'quality': {
            'quality': {'name': 'FLAC'}
          },
        },
        {
          'id': 71,
          'albumId': 9,
          'path': '/music/Weezer/Pinkerton/01 - Tired of Sex.flac',
          'size': 30000000,
          'quality': {
            'quality': {'name': 'FLAC'}
          },
        },
      ]
      ..tracks = const [
        {
          'id': 2,
          'trackFileId': 72,
          'absoluteTrackNumber': 2,
          'title': 'Getchoo',
        },
        {
          'id': 1,
          'trackFileId': 71,
          'absoluteTrackNumber': 1,
          'title': 'Tired of Sex',
        },
      ];
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: adapter,
      state: _musicDownloadsState,
    );

    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();

    // The live record was resolved and both reads went to that album id.
    expect(adapter.fileReads, containsAll(['album', 'trackfile:9', 'track:9']));
    expect(find.text('Download tracks'), findsOneWidget);

    await tester.tap(find.text('Download tracks'));
    await tester.pumpAndSettle();

    // Rows are track-ordered and track-named, never bare file paths.
    final first = tester.getTopLeft(find.text('1. Tired of Sex'));
    final second = tester.getTopLeft(find.text('2. Getchoo'));
    expect(first.dy, lessThan(second.dy),
        reason: 'files sort by track number, not by file id');
    expect(find.textContaining('FLAC'), findsWidgets);
  });

  testWidgets('without downloads the page fetches no files and shows no button',
      (tester) async {
    final adapter = _MusicAdapter(
      owned: const [
        {
          'title': 'Pinkerton',
          'artist': 'Weezer',
          'year': 1996,
          'foreign_album_id': 'mb-1',
          'cover': '',
          'monitored': true,
          'downloaded': true,
        },
      ],
    )..libraryAlbums = const [
        {'id': 9, 'title': 'Pinkerton', 'artistId': 4, 'foreignAlbumId': 'mb-1'},
      ];
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);

    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();

    expect(adapter.fileReads, isEmpty,
        reason: 'no mappings means the arr is never asked for file records');
    expect(find.textContaining('Download track'), findsNothing);
  });

  testWidgets('an owned album offers Report a problem when reporting is on',
      (tester) async {
    final adapter = _MusicAdapter(
      owned: const [
        {
          'title': 'Pinkerton',
          'artist': 'Weezer',
          'year': 1996,
          'foreign_album_id': 'mb-1',
          'cover': '',
          'monitored': true,
          'downloaded': true,
        },
      ],
    );
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: adapter,
      state: _musicReportingState,
    );

    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();

    expect(find.text('Report a problem'), findsOneWidget);
  });

  testWidgets(
      'no report entry without the server toggle or without a library record',
      (tester) async {
    // Reporting off: an owned album still shows no entry.
    final adapter = _MusicAdapter(
      owned: const [
        {
          'title': 'Pinkerton',
          'artist': 'Weezer',
          'year': 1996,
          'foreign_album_id': 'mb-1',
          'cover': '',
          'monitored': true,
          'downloaded': true,
        },
      ],
    );
    final (:router, container: _) = await _pumpRouter(tester, adapter: adapter);
    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();
    expect(find.text('Report a problem'), findsNothing);
  });

  testWidgets('an unowned album cannot be reported even with reporting on',
      (tester) async {
    // No library record means nothing to remediate; the gate mirrors books.
    final adapter = _MusicAdapter();
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: adapter,
      state: _musicReportingState,
    );
    router.go('/detail/album/mb-1?title=Pinkerton');
    await tester.pumpAndSettle();
    expect(find.text('Report a problem'), findsNothing);
  });
}

/// The downloads-enabled twin of [_musicState]: the instance reports saved
/// path mappings, which is what turns the per-track download surface on.
const _musicDownloadsState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    instances: [
      ServiceInstance(
        id: 'music-1',
        serviceType: 'lidarr',
        name: 'Music',
        isDefault: true,
        mediaDownloads: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// The reporting-enabled twin of [_musicState].
const _musicReportingState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    allowReporting: true,
    instances: [
      ServiceInstance(
        id: 'music-1',
        serviceType: 'lidarr',
        name: 'Music',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

const _musicState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    instances: [
      ServiceInstance(
        id: 'music-1',
        serviceType: 'lidarr',
        name: 'Music',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

Future<({ProviderContainer container, GoRouter router})> _pumpRouter(
  WidgetTester tester, {
  required _MusicAdapter adapter,
  AuthState state = _musicState,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(state)),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  addTearDown(container.dispose);

  await container.read(authProvider.future);
  await container.pump();
  final router = container.read(appRouterProvider);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return (container: container, router: router);
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

/// Serves the album detail's backend calls: the owned digest, the lookup,
/// the status read, and the request submission.
class _MusicAdapter implements HttpClientAdapter {
  _MusicAdapter({
    this.owned = const [],
    List<Map<String, dynamic>>? lookupAlbums,
    this.statusCode = 200,
  }) : lookupAlbums = lookupAlbums ?? _defaultLookup;

  final List<Map<String, dynamic>> owned;
  final List<Map<String, dynamic>> lookupAlbums;
  final int statusCode;
  final submissions = <Map<String, dynamic>>[];

  /// Library records served from GET /album, for the downloads flow.
  List<Map<String, dynamic>> libraryAlbums = const [];
  List<Map<String, dynamic>> trackFiles = const [];
  List<Map<String, dynamic>> tracks = const [];
  final fileReads = <String>[];

  static final _defaultLookup = [
    {
      'id': 0,
      'title': 'Pinkerton',
      'foreignAlbumId': 'mb-1',
      'releaseDate': '1996-09-24T00:00:00Z',
      'albumType': 'Album',
      'overview': 'A dark second record.',
      'genres': ['Alternative Rock'],
      'statistics': {'trackCount': 10},
      'artist': {
        'id': 0,
        'artistName': 'Weezer',
        'foreignArtistId': 'artist-9',
      },
    },
  ];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/music-library') {
      return _json({'titles': owned});
    }
    if (options.path.endsWith('/api/v1/album/lookup')) {
      return _json(lookupAlbums);
    }
    if (options.path.endsWith('/api/v1/album')) {
      fileReads.add('album');
      return _json(libraryAlbums);
    }
    if (options.path.endsWith('/api/v1/trackfile')) {
      fileReads.add('trackfile:${options.queryParameters['albumId']}');
      return _json(trackFiles);
    }
    if (options.path.endsWith('/api/v1/track')) {
      fileReads.add('track:${options.queryParameters['albumId']}');
      return _json(tracks);
    }
    if (options.path == '/api/media-files/coverage') {
      final raw = options.data;
      final body = raw is Map<String, dynamic>
          ? raw
          : jsonDecode(raw as String) as Map<String, dynamic>;
      final paths = (body['paths'] as List).length;
      return _json({'covered': List.filled(paths, true)});
    }
    if (options.path == '/api/requests/music-status') {
      if (statusCode != 200) {
        return ResponseBody.fromString(
          jsonEncode({'error': 'boom'}),
          statusCode,
          headers: {
            Headers.contentTypeHeader: [Headers.jsonContentType],
          },
        );
      }
      return _json({'status': 'unavailable'});
    }
    if (options.path == '/api/requests' && options.method == 'POST') {
      final raw = options.data;
      submissions.add(raw is Map<String, dynamic>
          ? raw
          : jsonDecode(raw as String) as Map<String, dynamic>);
      return _json({'status': 'requested', 'title': 'Pinkerton'});
    }
    return _json(const <String, dynamic>{});
  }

  ResponseBody _json(Object body) => ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );

  @override
  void close({bool force = false}) {}
}
