import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('a fully requested search row still opens rich book detail',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, :container, :adapter) = await _pumpRouter(tester);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    expect(searchField, findsOneWidget);
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Meditations'), findsOneWidget);
    // Both formats are covered, so no redundant aggregate status/action sits
    // beside the row. Per-format truth is on the detail surface.
    expect(find.text('Requested'), findsNothing);
    expect(find.byIcon(Icons.chevron_right), findsWidgets);

    final statusRequestsBeforeRefresh = adapter.statusRequests;
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();
    expect(adapter.statusRequests, greaterThan(statusRequestsBeforeRefresh));

    await tester.tap(
      find.byKey(const ValueKey('book-result:book-1:book-1:lookup:0')),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Marcus Aurelius'), findsOneWidget);
    expect(find.text('2002 · 304 pages'), findsOneWidget);
    expect(find.text('A practical guide to Stoic philosophy.'), findsOneWidget);
    expect(find.text('Requested'), findsNWidgets(2));
  });

  testWidgets('the trailing Request control does not open book detail',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) = await _pumpRouter(tester);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final secondResult =
        find.byKey(const ValueKey('book-result:book-2:book-2:lookup:1'));
    await tester.tap(
      find.descendant(of: secondResult, matching: find.text('Choose format')),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsNothing);
    expect(find.text('Letters from a Stoic'), findsWidgets);
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('eBook + Audiobook'), findsOneWidget);

    final libraryRequestsBefore = adapter.libraryRequests;
    await tester.tap(find.text('eBook'));
    await tester.pumpAndSettle();
    expect(adapter.libraryRequests, greaterThan(libraryRequestsBefore));
  });

  testWidgets(
      'request wizard chooses format first then a meaningful catalog match',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, editionVariants: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'the hobbit');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final original = find.byKey(const ValueKey(
      'book-result:hobbit-original:hobbit-original:lookup:0',
    ));
    expect(original, findsOneWidget);
    expect(
      find.descendant(of: original, matching: find.text('Recommended')),
      findsOneWidget,
    );

    await tester.tap(
      find.descendant(of: original, matching: find.text('Choose format')),
    );
    await tester.pumpAndSettle();

    expect(find.text('Choose how you want to enjoy this book.'), findsOneWidget);
    expect(find.text('Which match looks right?'), findsNothing);
    await tester.tap(find.text('Audiobook'));
    await tester.pumpAndSettle();

    expect(find.text('Which match looks right?'), findsOneWidget);
    expect(find.text('Recommended'), findsNWidgets(2));
    expect(find.textContaining('George Allen & Unwin'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.textContaining('75th Anniversary Edition'),
      180,
      scrollable: find.byType(Scrollable).last,
    );
    await tester.drag(
      find.byType(Scrollable).last,
      const Offset(0, -180),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('75th Anniversary Edition'), findsOneWidget);
    expect(find.textContaining('Record #'), findsNothing);

    await tester.tap(
      find.ancestor(
        of: find.textContaining('75th Anniversary Edition'),
        matching: find.byType(InkWell),
      ),
    );
    await tester.pumpAndSettle();

    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['foreign_id'], 'hobbit-anniversary');
    expect(adapter.requestBodies.single['book_format'], 'audiobook');
    expect(adapter.requestBodies.single['book_selection'], {
      'foreign_author_id': 'author-tolkien',
      'author_name': 'J. R. R. Tolkien',
      'audiobook': {
        'foreign_edition_id': 'edition-anniversary-audio',
        'isbn13': '9780007487295',
        'asin': 'B0ANNIVERSARY',
        'edition_title': '75th Anniversary Edition',
        'publisher': 'HarperCollins',
        'year': 2012,
        'page_count': 320,
      },
    });
  });

  testWidgets(
      'fuzzy ownership keeps lookup metadata but uses the canonical library id',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, mismatchedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(adapter.statusForeignIds, isNotEmpty);
    expect(adapter.statusForeignIds, everyElement('library-flock'));
    expect(
      find.byKey(
        const ValueKey(
            'book-result:lookup-flock:library-flock:lookup:0'),
      ),
      findsOneWidget,
    );
    // The normal-row test above proves the tile gesture. Continue this case
    // through the exact route/extra the mismatched row owns so the remainder
    // can assert detail identity and mutation payload end to end.
    router.go(
      '/detail/book/library-flock?title=Flock&instance_id=books',
      extra: ChaptarrBook.fromJson({
        'title': 'Flock',
        'foreignBookId': 'lookup-flock',
        'author': {'authorName': 'Kate Stewart'},
      }),
    );
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path,
        '/detail/book/library-flock');
    expect(
      router.routeInformationProvider.value.uri.queryParameters['instance_id'],
      'books',
    );
    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.foreignId, 'library-flock');
    expect(screen.initialBook?.foreignBookId, 'lookup-flock');

    await tester.scrollUntilVisible(
      find.text('Request eBook'),
      250,
      scrollable: find.descendant(
        of: find.byType(RequesterBookDetailScreen),
        matching: find.byType(Scrollable),
      ),
    );
    await tester.tap(find.text('Request eBook'));
    await tester.pumpAndSettle();

    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['foreign_id'], 'library-flock');
    expect(adapter.requestBodies.single['instance_id'], 'books');
    expect(adapter.requestBodies.single['book_format'], 'ebook');
  });

  testWidgets('a single known match and format submits without extra steps',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, mismatchedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Request eBook'));
    await tester.pumpAndSettle();

    expect(find.text('Choose how you want to enjoy this book.'), findsNothing);
    expect(find.text('Which match looks right?'), findsNothing);
    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['foreign_id'], 'library-flock');
    expect(adapter.requestBodies.single['book_format'], 'ebook');
  });

  testWidgets(
      'an unresolved fuzzy match keeps its canonical id and blocks requests',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, unresolvedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final row = find.byKey(
      const ValueKey('book-result:lookup-flock:library-flock:lookup:0'),
    );
    expect(row, findsOneWidget);
    expect(adapter.statusForeignIds, isNotEmpty);
    expect(adapter.statusForeignIds, everyElement('library-flock'));
    expect(
      find.descendant(
        of: row,
        matching: find.text('Ask an admin to check this book’s format'),
      ),
      findsOneWidget,
    );
    expect(find.text('Request eBook'), findsNothing);

    router.go(
      '/detail/book/library-flock?title=Flock&instance_id=books',
      extra: ChaptarrBook.fromJson({
        'title': 'Flock',
        'foreignBookId': 'lookup-flock',
        'author': {'authorName': 'Kate Stewart'},
      }),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Format needs attention'), findsNWidgets(2));
    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );
    expect(find.textContaining('Request eBook'), findsNothing);
    expect(adapter.requestBodies, isEmpty);
  });

  testWidgets('a mixed available and requested ownership chip stays requested',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, mixedOwnership: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final chip = tester.widget<Text>(
      find.text('eBook available · Audiobook requested'),
    );
    expect(chip.style?.color, AppTheme.requested);
  });

  testWidgets('a bare monitor flag does not claim a search result was requested',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, bareMonitor: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Audiobook requested'), findsNothing);
    expect(find.text('Choose format'), findsWidgets);
  });

  testWidgets(
      'equivalent lookup rows resolve automatically without exposing record ids',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, ambiguousLookup: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Choose a matching library record'), findsNothing);
    expect(find.textContaining('Record #'), findsNothing);
    expect(
      find.byKey(const ValueKey(
          'book-result:lookup-flock:lookup-flock:lookup:0')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey(
          'book-result:lookup-flock:lookup-flock:lookup:1')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey(
          'book-result:library-flock:library-flock:library:0')),
      findsOneWidget,
    );
    expect(adapter.statusForeignIds, isNotEmpty);
    expect(
      adapter.statusForeignIds,
      containsAll(['lookup-flock', 'library-flock']),
    );

    await tester.tap(find.text('Choose format').first);
    await tester.pumpAndSettle();
    await tester.tap(find.text('eBook'));
    await tester.pumpAndSettle();

    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['foreign_id'], 'lookup-flock');
    expect(adapter.requestBodies.single['book_format'], 'ebook');
    expect(adapter.requestBodies.single['book_selection'], {
      'foreign_author_id': 'author-flock',
      'author_name': 'Kate Stewart',
    });
    expect(find.text('Confirm the matching title'), findsNothing);
    expect(
      find.textContaining('equivalent catalog matches grouped'),
      findsNothing,
    );
    expect(find.text('Which match looks right?'), findsNothing);
  });

  testWidgets('same-title library records are surfaced separately',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, duplicateLibraryRecords: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Choose a matching library record'), findsNothing);
    expect(
      find.byKey(
          const ValueKey('book-result:library-a:library-a:library:0')),
      findsOneWidget,
    );
    expect(
      find.byKey(
          const ValueKey('book-result:library-b:library-b:library:1')),
      findsOneWidget,
    );
    expect(
      adapter.statusForeignIds,
      containsAll(['lookup-flock', 'library-a', 'library-b']),
    );
  });

  testWidgets('a lookup row without a canonical id explains why it is blocked',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, blankIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final row = find.byKey(const ValueKey('book-result:::lookup:0'));
    expect(row, findsOneWidget);
    expect(
      find.descendant(
        of: row,
        matching:
            find.text('Ask an admin to check this book’s library record'),
      ),
      findsOneWidget,
    );
    expect(tester.widget<ListTile>(row).onTap, isNull);
    expect(
      find.descendant(of: row, matching: find.text('Recommended')),
      findsNothing,
    );
    expect(adapter.statusForeignIds, isEmpty);
    expect(adapter.requestBodies, isEmpty);
  });

  testWidgets('book status and guidance fit a narrow phone at 200 percent text',
      (tester) async {
    tester.view.physicalSize = const Size(320, 700);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
      tester.platformDispatcher.clearTextScaleFactorTestValue();
    });
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, unresolvedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );

    router.go('/detail/book/library-flock?title=Flock&instance_id=books');
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('Format needs attention'), findsNWidgets(2));
  });
}

void _usePhoneSize(WidgetTester tester) {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
}

const _booksState = AuthState(
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

Future<({
  ProviderContainer container,
  GoRouter router,
  _BooksSearchAdapter adapter,
})> _pumpRouter(
  WidgetTester tester, {
  bool mismatchedIdentity = false,
  bool unresolvedIdentity = false,
  bool mixedOwnership = false,
  bool bareMonitor = false,
  bool ambiguousLookup = false,
  bool duplicateLibraryRecords = false,
  bool blankIdentity = false,
  bool editionVariants = false,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _BooksSearchAdapter(
    mismatchedIdentity: mismatchedIdentity,
    unresolvedIdentity: unresolvedIdentity,
    mixedOwnership: mixedOwnership,
    bareMonitor: bareMonitor,
    ambiguousLookup: ambiguousLookup,
    duplicateLibraryRecords: duplicateLibraryRecords,
    blankIdentity: blankIdentity,
    editionVariants: editionVariants,
  );
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(_booksState)),
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
  return (container: container, router: router, adapter: adapter);
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

class _BooksSearchAdapter implements HttpClientAdapter {
  _BooksSearchAdapter({
    this.mismatchedIdentity = false,
    this.unresolvedIdentity = false,
    this.mixedOwnership = false,
    this.bareMonitor = false,
    this.ambiguousLookup = false,
    this.duplicateLibraryRecords = false,
    this.blankIdentity = false,
    this.editionVariants = false,
  });

  final bool mismatchedIdentity;
  final bool unresolvedIdentity;
  final bool mixedOwnership;
  final bool bareMonitor;
  final bool ambiguousLookup;
  final bool duplicateLibraryRecords;
  final bool blankIdentity;
  final bool editionVariants;
  int statusRequests = 0;
  int libraryRequests = 0;
  bool ebookSubmitted = false;
  final statusForeignIds = <String>[];
  final requestBodies = <Map<String, dynamic>>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    Object body;
    if (options.path == '/api/requests' && options.method == 'POST') {
      final bytes = <int>[];
      if (requestStream != null) {
        await for (final chunk in requestStream) {
          bytes.addAll(chunk);
        }
      }
      final request = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      requestBodies.add(request);
      ebookSubmitted = true;
      body = {
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
      };
    } else if (options.path == '/api/requests/book-library') {
      libraryRequests++;
      body = duplicateLibraryRecords
          ? {
              'titles': [
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-a',
                  'ebook': {'monitored': true, 'downloaded': true},
                  'audiobook': {'monitored': false, 'downloaded': false},
                },
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-b',
                  'ebook': {'monitored': false, 'downloaded': false},
                  'audiobook': {'monitored': true, 'downloaded': false},
                },
              ],
            }
          : unresolvedIdentity
          ? {
              'titles': [
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-flock',
                  'status_known': false,
                  'ebook': {'monitored': false, 'downloaded': false},
                  'audiobook': {'monitored': false, 'downloaded': false},
                },
              ],
            }
          : (mismatchedIdentity || ambiguousLookup)
          ? {
              'titles': [
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-flock',
                  'ebook': {
                    'monitored': ebookSubmitted,
                    'downloaded': false,
                  },
                  'audiobook': {'monitored': true, 'downloaded': false},
                },
              ],
            }
          : (mixedOwnership || bareMonitor)
          ? {
              'titles': [
                {
                  'title': 'Meditations',
                  'author': 'Marcus Aurelius',
                  'foreign_book_id': 'book-1',
                  'ebook': {
                    'monitored': mixedOwnership,
                    'downloaded': mixedOwnership,
                  },
                  'audiobook': {'monitored': true, 'downloaded': false},
                },
              ],
            }
          : {'titles': <Object>[]};
    } else if (options.path == '/api/requests/book-status') {
      statusRequests++;
      final foreignId = options.queryParameters['foreign_id'].toString();
      statusForeignIds.add(foreignId);
      body = unresolvedIdentity && foreignId == 'library-flock'
          ? {
              'status': 'unavailable',
              'book_formats': {
                'ebook': 'unavailable',
                'audiobook': 'unavailable',
              },
            }
          : (mismatchedIdentity || ambiguousLookup) &&
                  foreignId == 'library-flock'
          ? {
              'status': 'requested',
              'book_formats': {
                if (ebookSubmitted) 'ebook': 'requested',
                'audiobook': 'requested',
              },
            }
          : foreignId == 'book-1' && bareMonitor
          ? {
              'status': 'unavailable',
              'book_formats': {
                'ebook': 'unavailable',
                'audiobook': 'unavailable',
              },
            }
          : foreignId == 'book-1'
          ? {
              'status': 'requested',
              'book_formats': {
                'ebook': 'requested',
                'audiobook': 'requested',
              },
            }
          : {'status': 'unavailable'};
    } else if (options.path.endsWith('/api/v1/book/lookup')) {
      body = editionVariants
          ? [
              {
                'title': 'The Hobbit',
                'foreignBookId': 'hobbit-original',
                'year': 1937,
                'pageCount': 310,
                'author': {
                  'authorName': 'J. R. R. Tolkien',
                  'foreignAuthorId': 'author-tolkien',
                },
                'editions': [
                  {
                    'id': 1,
                    'foreignEditionId': 'edition-original-audio',
                    'format': 'audiobook',
                    'asin': 'B0ORIGINAL',
                    'publisher': 'George Allen & Unwin',
                    'isbn13': '9780007525492',
                  },
                ],
              },
              {
                'title': 'The Hobbit',
                'foreignBookId': 'hobbit-anniversary',
                'year': 2012,
                'pageCount': 320,
                'author': {
                  'authorName': 'J. R. R. Tolkien',
                  'foreignAuthorId': 'author-tolkien',
                },
                'editions': [
                  {
                    'id': 2,
                    'foreignEditionId': 'edition-anniversary-audio',
                    'format': 'audiobook',
                    'asin': 'B0ANNIVERSARY',
                    'title': '75th Anniversary Edition',
                    'publisher': 'HarperCollins',
                    'isbn13': '9780007487295',
                  },
                ],
              },
            ]
          : (mismatchedIdentity ||
              unresolvedIdentity ||
              ambiguousLookup ||
              duplicateLibraryRecords ||
              blankIdentity)
          ? [
              {
                'title': 'Flock',
                'foreignBookId': blankIdentity ? '' : 'lookup-flock',
                'year': 2024,
                'author': {
                  'id': 0,
                  'authorName': 'Kate Stewart',
                  'foreignAuthorId': 'author-flock',
                },
              },
              if (ambiguousLookup)
                {
                  'title': 'Flock',
                  'foreignBookId': 'lookup-flock',
                  'year': 2024,
                  'author': {
                    'id': 0,
                    'authorName': 'Kate Stewart',
                    'foreignAuthorId': 'author-flock',
                  },
                },
            ]
          : [
        {
          'title': 'Meditations',
          'foreignBookId': 'book-1',
          'year': 2002,
          'pageCount': 304,
          'overview': 'A practical guide to Stoic philosophy.',
          'genres': ['Philosophy'],
          'author': {
            'id': 0,
            'authorName': 'Marcus Aurelius',
            'foreignAuthorId': 'author-1',
          },
        },
        {
          'title': 'Letters from a Stoic',
          'foreignBookId': 'book-2',
          'year': 1965,
          'pageCount': 254,
          'overview': 'Seneca on living with wisdom and courage.',
          'genres': ['Philosophy'],
          'author': {
            'id': 0,
            'authorName': 'Seneca',
            'foreignAuthorId': 'author-2',
          },
        },
            ];
    } else if (options.path == '/api/trakt/anticipated') {
      body = <Object>[];
    } else {
      body = {
        'page': 1,
        'results': <Object>[],
        'total_pages': 0,
        'total_results': 0,
      };
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
