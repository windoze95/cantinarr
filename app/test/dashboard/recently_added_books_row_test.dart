import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_books_tab.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Serves the Books tab's backend calls, with the recent-books body under test.
class _RecentAdapter implements HttpClientAdapter {
  _RecentAdapter({this.recentStatus = 200, this.items});

  final int recentStatus;
  final List<Map<String, dynamic>>? items;
  final recentInstanceIds = <String?>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/book-recent') {
      recentInstanceIds.add(options.queryParameters['instance_id'] as String?);
      if (recentStatus != 200) {
        return ResponseBody.fromString(
          jsonEncode({'error': 'forbidden'}),
          recentStatus,
          headers: {
            Headers.contentTypeHeader: [Headers.jsonContentType],
          },
        );
      }
      return _json({'items': items ?? const []});
    }
    if (options.path == '/api/requests/book-library') {
      return _json({'titles': const []});
    }
    if (options.path.endsWith('/api/v1/book/lookup')) {
      return _json(const []);
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

const _authState = AuthState(
  connection: BackendConnection(
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
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

Future<_RecentAdapter> _pumpBooksTab(
  WidgetTester tester, {
  int recentStatus = 200,
  List<Map<String, dynamic>>? items,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _RecentAdapter(recentStatus: recentStatus, items: items);
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
        home: Scaffold(body: DashboardBooksTab()),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

List<Map<String, dynamic>> _twoFormatsOfOneTitle() => [
      {
        'book_id': 8,
        'foreign_book_id': 'fb-1',
        'title': 'Ahsoka',
        'format': 'audiobook',
        'cover': '',
        'imported_at': '2026-07-24T12:00:00Z',
      },
      {
        'book_id': 7,
        'foreign_book_id': 'fb-1',
        'title': 'Ahsoka',
        'format': 'ebook',
        'cover': '/MediaCover/Books/7/cover.jpg',
        'imported_at': '2026-06-01T12:00:00Z',
      },
    ];

void main() {
  testWidgets('shows each format of a title as its own card', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(tester, items: _twoFormatsOfOneTitle());

    expect(find.text('Recently Added'), findsOneWidget);
    expect(find.byType(MediaCard), findsNWidgets(2));
    // The two formats arrive at different times and must not be merged.
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('eBook'), findsOneWidget);
  });

  testWidgets('hides the row while a search is active', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(tester, items: _twoFormatsOfOneTitle());
    expect(find.text('Recently Added'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'dune');
    await tester.pumpAndSettle(const Duration(milliseconds: 600));
    expect(find.text('Recently Added'), findsNothing);
    expect(find.text('No books found. Try a different search.'), findsOneWidget);

    // Clearing the query brings the row back.
    await tester.enterText(find.byType(TextField), '');
    await tester.pumpAndSettle(const Duration(milliseconds: 600));
    expect(find.text('Recently Added'), findsOneWidget);
  });

  testWidgets('stays silent when the user has no book access', (tester) async {
    await _pumpBooksTab(tester, recentStatus: 403);

    expect(find.text('Recently Added'), findsNothing);
    // A missing row must not look like a failure, and search must still work.
    expect(find.textContaining('access'), findsNothing);
    expect(find.byType(TextField), findsOneWidget);
  });

  testWidgets('hides the row when nothing has landed', (tester) async {
    await _pumpBooksTab(tester, items: const []);

    expect(find.text('Recently Added'), findsNothing);
    expect(find.byType(MediaCard), findsNothing);
  });

  testWidgets('loads covers through the authenticated instance proxy',
      (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(tester, items: [
      {
        'book_id': 7,
        'foreign_book_id': 'fb-1',
        'title': 'Ahsoka',
        'format': 'ebook',
        'cover': '/MediaCover/Books/7/cover.jpg',
        'imported_at': '2026-07-24T12:00:00Z',
      },
    ]);

    final image = tester.widget<CachedImage>(
      find.descendant(
        of: find.byType(MediaCard),
        matching: find.byType(CachedImage),
      ),
    );
    // Never an arr-origin URL, and never routed through the TMDB poster helper.
    expect(
      image.url,
      'http://localhost/api/instances/books/api/v1/MediaCover/Books/7/cover.jpg',
    );
    expect(image.headers, {'Authorization': 'Bearer access'});
  });

  testWidgets('a record with no canonical id is not tappable', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(tester, items: [
      {
        'book_id': 7,
        'foreign_book_id': '',
        'title': 'Orphaned Record',
        'format': 'ebook',
        'cover': '',
        'imported_at': '2026-07-24T12:00:00Z',
      },
    ]);

    final card = tester.widget<MediaCard>(find.byType(MediaCard));
    expect(card.onTap, isNull);
  });

  testWidgets('scopes the request to the active book library', (tester) async {
    final adapter = await _pumpBooksTab(tester, items: const []);

    expect(adapter.recentInstanceIds, isNotEmpty);
    expect(adapter.recentInstanceIds.every((id) => id == 'books'), isTrue);
  });
}
