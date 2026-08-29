import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/data/book_authors_service.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_books_tab.dart';
import 'package:cantinarr/features/dashboard/ui/library_authors_row.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// Serves the Books tab's backend calls, with the authors body under test.
class _AuthorsAdapter implements HttpClientAdapter {
  _AuthorsAdapter({
    this.authorsStatus = 200,
    this.authors,
    this.bySort,
    this.total,
  });

  final int authorsStatus;
  final List<Map<String, dynamic>>? authors;

  /// The library's untruncated author count, as the server reports it.
  final int? total;

  /// Per-sort bodies, so a test can prove the row shows what the server
  /// returned for the requested order rather than reordering it locally.
  final Map<String, List<Map<String, dynamic>>>? bySort;
  final authorsInstanceIds = <String?>[];
  final authorsSorts = <String?>[];

  /// Gates the next authors response until the test releases it, so the
  /// in-flight state is observable.
  Completer<void>? gate;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/book-authors') {
      authorsInstanceIds.add(options.queryParameters['instance_id'] as String?);
      final sort = options.queryParameters['sort'] as String?;
      authorsSorts.add(sort);
      if (gate != null) await gate!.future;
      if (bySort != null) {
        final rows = bySort![sort] ?? const <Map<String, dynamic>>[];
        return _json({'authors': rows, 'total': total ?? rows.length});
      }
      if (authorsStatus != 200) {
        return ResponseBody.fromString(
          jsonEncode({'error': 'unreachable'}),
          authorsStatus,
          headers: {
            Headers.contentTypeHeader: [Headers.jsonContentType],
          },
        );
      }
      final rows = authors ?? const <Map<String, dynamic>>[];
      return _json({'authors': rows, 'total': total ?? rows.length});
    }
    if (options.path == '/api/requests/book-recent') {
      return _json({'items': const []});
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

Map<String, dynamic> _author({
  String foreignAuthorId = 'fa-1',
  String name = 'Martha Wells',
  String image = '',
  int titleCount = 4,
  int availableCount = 2,
}) =>
    {
      'foreign_author_id': foreignAuthorId,
      'name': name,
      'image': image,
      'title_count': titleCount,
      'available_count': availableCount,
    };

/// The location the row last pushed, so navigation can be asserted without a
/// full app router.
String? lastPushedLocation;

Future<_AuthorsAdapter> _pumpBooksTab(
  WidgetTester tester, {
  int authorsStatus = 200,
  List<Map<String, dynamic>>? authors,
  Map<String, List<Map<String, dynamic>>>? bySort,
  int? total,
}) async {
  lastPushedLocation = null;
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _AuthorsAdapter(
    authorsStatus: authorsStatus,
    authors: authors,
    bySort: bySort,
    total: total,
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

  final router = GoRouter(
    initialLocation: '/dashboard/books',
    routes: [
      GoRoute(
        path: '/dashboard/books',
        builder: (_, __) => const Scaffold(body: DashboardBooksTab()),
      ),
      GoRoute(
        path: '/detail/:type/:id',
        builder: (_, state) {
          lastPushedLocation = state.uri.toString();
          return const Scaffold(body: Text('author page'));
        },
      ),
    ],
  );
  addTearDown(router.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

void _sizeViewport(WidgetTester tester) {
  tester.view.physicalSize = const Size(900, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  testWidgets('shows the library authors with what is held by each',
      (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpBooksTab(tester, authors: [
      _author(),
      _author(
        foreignAuthorId: 'fa-2',
        name: 'Becky Chambers',
        titleCount: 3,
        availableCount: 3,
      ),
      _author(
        foreignAuthorId: 'fa-3',
        name: 'Ann Leckie',
        titleCount: 1,
        availableCount: 0,
      ),
    ]);

    expect(find.text('Authors'), findsOneWidget);
    expect(find.byType(AuthorAvatarCard), findsNWidgets(3));
    expect(find.text('Martha Wells'), findsOneWidget);
    // The count always says what it counted, so a partly-collected author can
    // never be misread as a library holding only what is on disk.
    expect(find.text('2 of 4 books available'), findsOneWidget);
    expect(find.text('3 books · all available'), findsOneWidget);
    expect(find.text('1 book'), findsOneWidget);
    // The row follows the drawer's active Chaptarr instance.
    expect(adapter.authorsInstanceIds, contains('books'));
  });

  testWidgets('opens the author page for the tapped author', (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, authors: [_author()]);
    await tester.tap(find.text('Martha Wells'));
    await tester.pumpAndSettle();

    expect(lastPushedLocation, isNotNull);
    expect(lastPushedLocation, contains('/detail/author/fa-1'));
    // The pinned instance travels with the link so the page can never read
    // another library's answer for this author.
    expect(lastPushedLocation, contains('instance_id=books'));
    expect(find.text('author page'), findsOneWidget);
  });

  testWidgets('an author with no id is shown but not openable', (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, authors: [_author(foreignAuthorId: '')]);
    expect(find.text('Martha Wells'), findsOneWidget);

    await tester.tap(find.text('Martha Wells'));
    await tester.pumpAndSettle();

    expect(lastPushedLocation, isNull);
    expect(find.text('author page'), findsNothing);
  });

  testWidgets('an unreadable library hides the row rather than claiming none',
      (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, authorsStatus: 500, authors: [_author()]);

    expect(find.text('Authors'), findsNothing);
    expect(find.byType(AuthorAvatarCard), findsNothing);
  });

  testWidgets('a library with no authors shows no row', (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, authors: const []);

    expect(find.text('Authors'), findsNothing);
  });

  _mainSortTests();
  _mainTruncationTests();

  // "hides the row while a search is active" was removed in Phase 3 (TAB-01):
  // DashboardBooksTab no longer has a search field of its own — search moved
  // to the shell toolbar, an entirely separate widget this file's harness
  // never pumps — so there is no in-widget "search active" state left for
  // this row to hide behind. The old idle gate that hid the row is gone with
  // it. Proof that the row is now covered by the shell's overlay instead of
  // being removed lives in app/test/dashboard/dashboard_books_tab_test.dart's
  // 'the browse rows stay in the tree underneath an active book-search overlay'
  // case — the only harness in this phase that pumps the real AppShell and
  // can see the overlay at all.
}

void _mainSortTests() {
  testWidgets('names the current order and asks the server for it',
      (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpBooksTab(tester, authors: [_author()]);

    // The order is not self-evident from a shelf of faces, so the control says
    // which one is active rather than showing a bare icon.
    expect(find.text('Most books'), findsOneWidget);
    expect(adapter.authorsSorts, ['books']);
  });

  testWidgets('picking an order refetches it from the server', (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpBooksTab(tester, bySort: {
      'books': [
        _author(foreignAuthorId: 'fa-1', name: 'Zed Owns Most', titleCount: 9, availableCount: 9),
        _author(foreignAuthorId: 'fa-2', name: 'Aaron Owns One', titleCount: 1, availableCount: 1),
      ],
      'name': [
        _author(foreignAuthorId: 'fa-2', name: 'Aaron Owns One', titleCount: 1, availableCount: 1),
        _author(foreignAuthorId: 'fa-1', name: 'Zed Owns Most', titleCount: 9, availableCount: 9),
      ],
    });

    LibraryAuthor firstCard() => tester
        .widgetList<AuthorAvatarCard>(find.byType(AuthorAvatarCard))
        .first
        .author;
    expect(firstCard().name, 'Zed Owns Most');

    await tester.tap(find.text('Most books'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Name').last);
    await tester.pumpAndSettle();

    // The row must show what the server returned for that order. Sorting the
    // capped list locally would mean "the most-collected authors, alphabetised".
    expect(adapter.authorsSorts, ['books', 'name']);
    expect(firstCard().name, 'Aaron Owns One');
    expect(find.text('Name'), findsOneWidget);
  });

  testWidgets('the row and its menu survive the refetch', (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpBooksTab(tester, bySort: {
      'books': [_author(name: 'Imogen Vale')],
      'name': [_author(name: 'Imogen Vale')],
    });
    expect(find.byType(AuthorAvatarCard), findsOneWidget);

    // Hold the next response open so the in-flight state is observable.
    adapter.gate = Completer<void>();
    await tester.tap(find.text('Most books'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Name').last);
    await tester.pump();

    // Collapsing to nothing here would take the control the user just used
    // with it, and bounce everything below the row up and back down.
    expect(find.text('Authors'), findsOneWidget);
    expect(find.byType(AuthorAvatarCard), findsOneWidget);

    adapter.gate!.complete();
    await tester.pumpAndSettle();
    expect(find.byType(AuthorAvatarCard), findsOneWidget);
  });
}

void _mainTruncationTests() {
  testWidgets('a library bigger than the row says how much it is holding back',
      (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(
      tester,
      authors: [_author(), _author(foreignAuthorId: 'fa-2', name: 'Ann Leckie')],
      total: 337,
    );

    // A shelf that simply stops at its cap reads as the whole library.
    expect(find.text('2 of 337'), findsOneWidget);
  });

  testWidgets('a library that fits says nothing about counts', (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, authors: [_author()]);

    // Anchored: an author card's own "2 of 4 books available" is not a
    // truncation notice.
    expect(find.textContaining(RegExp(r'^\d+ of \d+$')), findsNothing);
    expect(find.text('Most books'), findsOneWidget);
  });

  testWidgets('a server too old to report a total claims no truncation',
      (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, authors: [_author()], total: 0);

    // total:0 is what an older server sends. Reading that as "0 authors, so
    // this row is hiding some" would put a nonsense count in the header.
    expect(find.textContaining(RegExp(r'^\d+ of \d+$')), findsNothing);
  });
}
