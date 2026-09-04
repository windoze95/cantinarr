import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_music_tab.dart';
import 'package:cantinarr/features/dashboard/ui/library_artists_row.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Serves the Music tab's backend calls: recent albums, the owned digest, and
/// the artists row.
class _MusicTabAdapter implements HttpClientAdapter {
  _MusicTabAdapter({
    this.recentStatus = 200,
    this.items,
    this.libraryStatus = 200,
    this.titles,
    this.artistsStatus = 200,
    this.artists,
    this.artistsTotal,
  });

  final int recentStatus;
  final List<Map<String, dynamic>>? items;
  final int libraryStatus;
  final List<Map<String, dynamic>>? titles;
  final int artistsStatus;
  final List<Map<String, dynamic>>? artists;
  final int? artistsTotal;
  final recentInstanceIds = <String?>[];
  final artistSorts = <String?>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/music-recent') {
      recentInstanceIds.add(options.queryParameters['instance_id'] as String?);
      if (recentStatus != 200) return _error(recentStatus);
      return _json({'items': items ?? const []});
    }
    if (options.path == '/api/requests/music-library') {
      if (libraryStatus != 200) return _error(libraryStatus);
      return _json({'titles': titles ?? const []});
    }
    if (options.path == '/api/requests/music-artists') {
      artistSorts.add(options.queryParameters['sort'] as String?);
      if (artistsStatus != 200) return _error(artistsStatus);
      final list = artists ?? const [];
      return _json({'artists': list, 'total': artistsTotal ?? list.length});
    }
    return _json(const <String, dynamic>{});
  }

  ResponseBody _error(int status) => ResponseBody.fromString(
        jsonEncode({'error': 'nope'}),
        status,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );

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

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);
  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

const _authState = AuthState(
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

Future<_MusicTabAdapter> _pumpMusicTab(
  WidgetTester tester, {
  int recentStatus = 200,
  List<Map<String, dynamic>>? items,
  int libraryStatus = 200,
  List<Map<String, dynamic>>? titles,
  int artistsStatus = 200,
  List<Map<String, dynamic>>? artists,
  int? artistsTotal,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _MusicTabAdapter(
    recentStatus: recentStatus,
    items: items,
    libraryStatus: libraryStatus,
    titles: titles,
    artistsStatus: artistsStatus,
    artists: artists,
    artistsTotal: artistsTotal,
  );
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(_authState)),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  addTearDown(container.dispose);
  await container.read(authProvider.future);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(
        home: Scaffold(body: DashboardMusicTab()),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

List<Map<String, dynamic>> _twoRecentAlbums() => [
      {
        'album_id': 9,
        'foreign_album_id': 'mb-1',
        'title': 'Pinkerton',
        'artist': 'Weezer',
        'cover': '/mediacover/album/9/cover.jpg',
        'imported_at': '2026-08-30T12:00:00Z',
      },
      {
        'album_id': 7,
        'foreign_album_id': 'mb-2',
        'title': 'Blue Album',
        'artist': 'Weezer',
        'cover': '',
        'imported_at': '2026-07-01T12:00:00Z',
      },
    ];

Map<String, dynamic> _ownedEntry({
  required String foreignAlbumId,
  bool monitored = false,
  bool downloaded = false,
}) =>
    {
      'title': 'Pinkerton',
      'artist': 'Weezer',
      'year': 1996,
      'foreign_album_id': foreignAlbumId,
      'cover': '',
      'monitored': monitored,
      'downloaded': downloaded,
    };

void main() {
  setUp(() {
    // Both rows render side-scrolling shelves; a phone-sized viewport clips
    // the second card's text into overflow errors that are not this test's
    // subject.
    TestWidgetsFlutterBinding.ensureInitialized();
  });

  testWidgets('renders the recently added cards with ownership pills',
      (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpMusicTab(
      tester,
      items: _twoRecentAlbums(),
      titles: [
        _ownedEntry(foreignAlbumId: 'mb-1', downloaded: true),
        _ownedEntry(foreignAlbumId: 'mb-2', monitored: true),
      ],
    );

    expect(find.text('Recently Added'), findsOneWidget);
    expect(find.byType(MediaCard), findsNWidgets(2));
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Requested'), findsOneWidget);
    // The artist rides as the card subtitle.
    expect(find.text('Weezer'), findsNWidgets(2));
  });

  testWidgets('a card the digest cannot vouch for renders no pill',
      (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpMusicTab(tester, items: _twoRecentAlbums(), titles: const []);

    expect(find.byType(MediaCard), findsNWidgets(2));
    expect(find.text('Available'), findsNothing,
        reason: 'an unmatched digest row must never become a guessed pill');
    expect(find.text('Requested'), findsNothing);
  });

  testWidgets('stays silent when the user has no music access',
      (tester) async {
    await _pumpMusicTab(tester, recentStatus: 403, artistsStatus: 403);

    expect(find.text('Recently Added'), findsNothing);
    expect(find.text('Artists'), findsNothing);
    // A missing row must not look like a failure.
    expect(find.textContaining('access'), findsNothing);
  });

  testWidgets('hides the recent row when nothing has landed', (tester) async {
    await _pumpMusicTab(tester, items: const [], artists: const []);

    expect(find.text('Recently Added'), findsNothing);
    expect(find.byType(MediaCard), findsNothing);
  });

  testWidgets(
      'an unreadable ownership digest hides the recent row instead of '
      'degrading it', (tester) async {
    await _pumpMusicTab(
      tester,
      items: _twoRecentAlbums(),
      libraryStatus: 500,
      artists: const [],
    );

    expect(find.text('Recently Added'), findsNothing);
  });

  testWidgets('renders the artists row with counts and a truncation note',
      (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final adapter = await _pumpMusicTab(
      tester,
      items: const [],
      artists: [
        {
          'foreign_artist_id': 'a-1',
          'name': 'Weezer',
          'image': '',
          'album_count': 4,
          'available_count': 2,
        },
      ],
      artistsTotal: 250,
    );

    expect(find.text('Artists'), findsOneWidget);
    expect(find.byType(ArtistAvatarCard), findsOneWidget);
    expect(find.text('2 of 4 albums available'), findsOneWidget);
    // The row must be able to say it is truncated — a shelf that just stops
    // reads as the whole library.
    expect(find.textContaining('of 250'), findsOneWidget);
    expect(adapter.artistSorts.single, 'albums');
  });

  testWidgets('the artists row hides on an unreadable library',
      (tester) async {
    await _pumpMusicTab(tester, items: const [], artistsStatus: 500);

    expect(find.text('Artists'), findsNothing);
  });
}
