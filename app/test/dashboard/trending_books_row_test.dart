import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/trending_books_row.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// Serves the trending feed and the owned-books digest; records the trending
/// request's instance id and every navigation the row triggers.
class _Adapter implements HttpClientAdapter {
  _Adapter({this.trendingStatus = 200, this.trending, this.titles});

  final int trendingStatus;

  /// Body of GET /api/discover/books/trending; null mimics an older server
  /// that answers the route with an unrelated empty object.
  final Map<String, dynamic>? trending;
  final List<Map<String, dynamic>>? titles;
  final trendingInstanceIds = <String?>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/discover/books/trending') {
      trendingInstanceIds
          .add(options.queryParameters['instance_id'] as String?);
      if (trendingStatus != 200) {
        return ResponseBody.fromString(
          jsonEncode({'error': 'could not reach Hardcover'}),
          trendingStatus,
          headers: {
            Headers.contentTypeHeader: [Headers.jsonContentType],
          },
        );
      }
      return _json(trending ?? const <String, dynamic>{});
    }
    if (options.path == '/api/requests/book-library') {
      return _json({'titles': titles ?? const []});
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

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);
  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

AuthState _auth({required String role}) => AuthState(
      connection: const BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(chaptarr: true),
        instances: [
          ServiceInstance(
            id: 'books',
            serviceType: 'chaptarr',
            name: 'Books',
            isDefault: true,
          ),
        ],
      ),
      user: UserProfile(id: 1, username: 'tester', role: role),
    );

Map<String, dynamic> _book(int id, String title,
        {List<String> isbns = const [], double? rating}) =>
    {
      'hardcover_id': id,
      'foreign_id': 'hc:$id',
      'title': title,
      'authors': ['Matt Dinniman'],
      'year': 2020,
      if (rating != null) 'rating': rating,
      'image_url': 'https://assets.hardcover.app/$id.jpg',
      'isbn13s': isbns,
    };

Map<String, dynamic> _owned(
  String title, {
  required List<String> identityKeys,
  String foreignBookId = '',
  bool ebookDownloaded = false,
  bool audiobookDownloaded = false,
  bool ebookMonitored = false,
  bool audiobookMonitored = false,
}) =>
    {
      'title': title,
      'author': 'Matt Dinniman',
      'foreign_book_id': foreignBookId,
      'identity_keys': identityKeys,
      'ebook': {'monitored': ebookMonitored, 'downloaded': ebookDownloaded},
      'audiobook': {
        'monitored': audiobookMonitored,
        'downloaded': audiobookDownloaded,
      },
      'status_known': true,
    };

Future<(_Adapter, List<String>)> _pump(
  WidgetTester tester, {
  required _Adapter adapter,
  String role = 'user',
}) async {
  tester.view.physicalSize = const Size(1000, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(_auth(role: role))),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  addTearDown(container.dispose);
  await container.read(authProvider.future);

  final pushed = <String>[];
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (_, __) => const Scaffold(
          body: SingleChildScrollView(child: TrendingBooksRow()),
        ),
      ),
      GoRoute(
        path: '/detail/book/:id',
        builder: (_, state) {
          pushed.add(state.uri.toString());
          return const Scaffold(body: Text('book detail'));
        },
      ),
      GoRoute(
        path: '/settings/instance/:id',
        builder: (_, state) {
          pushed.add('${state.uri} ${state.extra}');
          return const Scaffold(body: Text('instance editor'));
        },
      ),
    ],
  );
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return (adapter, pushed);
}

void main() {
  testWidgets('renders the feed in order with ownership pills by identity key',
      (tester) async {
    final (adapter, pushed) = await _pump(
      tester,
      adapter: _Adapter(
        trending: {
          'instance_id': 'books',
          'connected': true,
          'books': [
            _book(1, 'Owned Both', isbns: ['9780000000001'], rating: 4.4),
            _book(2, 'Owned By ISBN', isbns: ['9780000000002']),
            _book(3, 'Requested One'),
            _book(4, 'Not In Library'),
          ],
        },
        titles: [
          // Matched by the hc-book key.
          _owned('Owned Both',
              identityKeys: ['hc-book:1'],
              ebookDownloaded: true,
              audiobookDownloaded: true),
          // Matched only through a shared ISBN.
          _owned('Owned By ISBN',
              identityKeys: ['gr-work:99', 'isbn:9780000000002'],
              ebookDownloaded: true),
          // Matched by the native foreign id, one format still downloading.
          _owned('Requested One',
              foreignBookId: 'hc:3',
              identityKeys: const [],
              ebookMonitored: true),
        ],
      ),
    );

    expect(adapter.trendingInstanceIds, ['books']);
    expect(find.text('Trending Books'), findsOneWidget);
    final cards = tester
        .widgetList<MediaCard>(find.byType(MediaCard))
        .toList(growable: false);
    expect(cards.map((c) => c.title),
        ['Owned Both', 'Owned By ISBN', 'Requested One', 'Not In Library']);
    expect(cards[0].statusLabel, 'Available');
    expect(cards[0].subtitle, 'eBook + Audiobook');
    expect(cards[0].rating, 4.4);
    expect(cards[1].statusLabel, 'Partial');
    expect(cards[2].statusLabel, 'Requested');
    // No pill for a book the library does not hold; the author fills in.
    expect(cards[3].statusLabel, isNull);
    expect(cards[3].subtitle, 'Matt Dinniman');
    expect(cards[3].posterPath, 'https://assets.hardcover.app/4.jpg');
    expect(find.text('Connect Hardcover in Chaptarr settings'), findsNothing);

    await tester.tap(find.text('Not In Library'));
    await tester.pumpAndSettle();
    expect(pushed, hasLength(1));
    expect(pushed.single, startsWith('/detail/book/hc%3A4?'));
    expect(pushed.single, contains('source=chaptarr'));
    expect(pushed.single, contains('instance_id=books'));
  });

  testWidgets('an admin on an unconnected instance gets the way to Settings',
      (tester) async {
    final (_, pushed) = await _pump(
      tester,
      role: 'admin',
      adapter: _Adapter(
        trending: {'instance_id': 'books', 'connected': false, 'books': []},
      ),
    );
    expect(find.text('Trending Books'), findsOneWidget);
    expect(find.byType(MediaCard), findsNothing);
    await tester.tap(find.text('Connect Hardcover in Chaptarr settings'));
    await tester.pumpAndSettle();
    expect(pushed.single, startsWith('/settings/instance/books '));
    expect(pushed.single, contains('service_type: chaptarr'));
  });

  testWidgets('a requester on an unconnected instance sees no row',
      (tester) async {
    await _pump(
      tester,
      adapter: _Adapter(
        trending: {'instance_id': 'books', 'connected': false, 'books': []},
      ),
    );
    expect(find.text('Trending Books'), findsNothing);
    expect(find.text('Connect Hardcover in Chaptarr settings'), findsNothing);
  });

  testWidgets('an older server that does not know the route hides the row',
      (tester) async {
    await _pump(tester, role: 'admin', adapter: _Adapter());
    expect(find.text('Trending Books'), findsNothing);
    expect(find.text('Connect Hardcover in Chaptarr settings'), findsNothing);
  });

  testWidgets('a feed the server could not read hides the row', (tester) async {
    // Blindness is not absence: no row, and no "connect" prompt either.
    await _pump(tester, role: 'admin', adapter: _Adapter(trendingStatus: 502));
    expect(find.text('Trending Books'), findsNothing);
    expect(find.text('Connect Hardcover in Chaptarr settings'), findsNothing);
  });
}
