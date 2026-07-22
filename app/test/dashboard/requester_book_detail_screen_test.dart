import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_book_screen.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// The requester book detail surface, exercised through the real router with a
/// faked backend: an owned digest row resolves the presentation, the push
/// payload's title names an unresolvable book, and a dead id degrades to a
/// graceful not-found state that points back to the Books tab.
void main() {
  testWidgets('an owned digest row resolves the full book presentation',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Ahsoka'), findsOneWidget);
    expect(find.text('E. K. Johnston'), findsOneWidget);
    expect(find.text('2016'), findsOneWidget);
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Not requested'), findsOneWidget);
    // Flock-style mixed truth: a monitored audiobook is Requested while the
    // untouched eBook remains the one exact action.
    await tester.scrollUntilVisible(
      find.text('Request eBook'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.text('Request eBook'), findsOneWidget);
    expect(find.text('Manage book'), findsNothing);
  });

  testWidgets('a deep link resolves rich metadata and both requested formats',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Frank Herbert'), findsOneWidget);
    expect(find.text('1969 · 336 pages'), findsOneWidget);
    expect(
      find.text('The desert planet has a new emperor.\n\nA second chapter & more.'),
      findsOneWidget,
    );
    expect(find.textContaining('<b>'), findsNothing);
    expect(find.text('Science Fiction'), findsOneWidget);
    expect(find.text('Requested'), findsNWidgets(2));
  });

  testWidgets('a canonical deep link accepts one strong title metadata match',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(mismatchedLookupId: true),
    );

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Frank Herbert'), findsOneWidget);
    expect(find.text('1969 · 336 pages'), findsOneWidget);
  });

  testWidgets(
      'a provider-id mismatch rejects same-title metadata from another author',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(
        mismatchedLookupId: true,
        mismatchedLookupAuthor: true,
      ),
    );

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Brian Herbert'), findsNothing);
    expect(find.text('1969 · 336 pages'), findsNothing);
    expect(find.text('Science Fiction'), findsNothing);
    expect(find.textContaining('desert planet'), findsNothing);
  });

  testWidgets('a known format stays visible beside an unknown sibling',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      adapter: _BooksAdapter(partiallyUnknownStatus: true),
    );

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Format needs attention'), findsOneWidget);
    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );
  });

  testWidgets('an admin can open both exact-format records in Chaptarr',
      (tester) async {
    final (:router, container: _) =
        await _pumpRouter(tester, authState: _adminBooksState);

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.text('Manage book'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.text('Manage book'), findsOneWidget);
    await tester.tap(find.text('Manage book'));
    await tester.pumpAndSettle();

    expect(find.byType(ChaptarrBookScreen), findsOneWidget);
    final screen = tester.widget<ChaptarrBookScreen>(
      find.byType(ChaptarrBookScreen),
    );
    expect(screen.instanceId, 'books');
    expect(screen.records, hasLength(2));
    expect(screen.records.map((book) => book.mediaType),
        orderedEquals(['ebook', 'audiobook']));
  });

  testWidgets('a requester can download each exact available book format',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      authState: _downloadBooksState,
      adapter: _BooksAdapter(bookFiles: true),
    );

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.byTooltip('Download eBook'),
      250,
      scrollable: _detailScrollable(),
    );
    expect(find.byTooltip('Download eBook'), findsOneWidget);
    expect(find.byTooltip('Download audiobook'), findsOneWidget);
    expect(find.textContaining('/library/'), findsNothing);
    expect(find.textContaining(r'Z:\'), findsNothing);
  });

  testWidgets('admin Chaptarr detail has the same per-format downloads',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      authState: _adminDownloadBooksState,
      adapter: _BooksAdapter(bookFiles: true),
    );

    router.go('/detail/book/29749107?title=Ahsoka');
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.text('Manage book'),
      250,
      scrollable: _detailScrollable(),
    );
    await tester.tap(find.text('Manage book'));
    await tester.pumpAndSettle();

    expect(find.byType(ChaptarrBookScreen), findsOneWidget);
    expect(find.byTooltip('Download eBook'), findsOneWidget);
    expect(find.byTooltip('Download Audiobook'), findsOneWidget);
  });

  testWidgets('an absolute owned cover origin is never sent to the client',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      ownedCover: 'http://chaptarr:8787/MediaCover/Books/42/cover.jpg',
    );

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    final cover = tester.widget<CachedImage>(
      find.descendant(
        of: find.byType(RequesterBookDetailScreen),
        matching: find.byType(CachedImage),
      ),
    );
    expect(cover.url, isNull);
  });

  testWidgets('an admin link requires an exact live foreign id match',
      (tester) async {
    final (:router, container: _) =
        await _pumpRouter(tester, authState: _adminBooksState);

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    // The live list contains Ahsoka, but not this metadata-only Dune result.
    expect(find.text('Dune Messiah'), findsOneWidget);
    expect(find.text('Manage book'), findsNothing);
  });

  testWidgets('a pinned detail ignores ownership from the active instance',
      (tester) async {
    final adapter = _BooksAdapter(divergentLibraries: true);
    final (:router, :container) = await _pumpRouter(
      tester,
      authState: _adminBooksState,
      adapter: adapter,
    );

    router.go(
      '/detail/book/29749107?title=Ahsoka&instance_id=books',
    );
    await tester.pumpAndSettle();
    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Not requested'), findsOneWidget);

    container
        .read(instanceProvider.notifier)
        .setActiveChaptarrInstance('books-two');
    await tester.pumpAndSettle();

    expect(find.text('Requested'), findsOneWidget);
    expect(find.text('Not requested'), findsOneWidget);
    expect(find.text('Available'), findsNothing);
    expect(adapter.libraryInstanceIds, everyElement('books'));
    expect(adapter.statusInstanceIds, everyElement('books'));
  });

  testWidgets('an unresolvable id shows a graceful state with a Books tab exit',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/does-not-exist');
    await tester.pumpAndSettle();

    expect(
      find.text(
        'This book could not be found. It may have been removed from '
        'the library.',
      ),
      findsOneWidget,
    );

    await tester.tap(find.text('Browse Books'));
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/dashboard/books');
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

const _adminBooksState = AuthState(
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
      ServiceInstance(
        id: 'books-two',
        serviceType: 'chaptarr',
        name: 'Other Books',
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

const _downloadBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true, mediaDownloads: true),
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

const _adminDownloadBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true, mediaDownloads: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

Future<({ProviderContainer container, GoRouter router})> _pumpRouter(
  WidgetTester tester, {
  AuthState authState = _booksState,
  String ownedCover = '',
  _BooksAdapter? adapter,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(authState)),
      backendClientProvider.overrideWithValue(_fakeDio(
        ownedCover: ownedCover,
        adapter: adapter,
      )),
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

Dio _fakeDio({String ownedCover = '', _BooksAdapter? adapter}) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter =
      adapter ?? _BooksAdapter(ownedCover: ownedCover);
  return dio;
}

/// Serves requester metadata/status plus the live Chaptarr records an admin
/// resolves before showing the internal module link.
class _BooksAdapter implements HttpClientAdapter {
  final String ownedCover;
  final bool divergentLibraries;
  final bool mismatchedLookupId;
  final bool mismatchedLookupAuthor;
  final bool partiallyUnknownStatus;
  final bool bookFiles;
  final libraryInstanceIds = <String>[];
  final statusInstanceIds = <String>[];

  _BooksAdapter({
    this.ownedCover = '',
    this.divergentLibraries = false,
    this.mismatchedLookupId = false,
    this.mismatchedLookupAuthor = false,
    this.partiallyUnknownStatus = false,
    this.bookFiles = false,
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body;
    if (options.path == '/api/requests/book-library') {
      final instanceId = options.queryParameters['instance_id'].toString();
      libraryInstanceIds.add(instanceId);
      final otherLibrary = divergentLibraries && instanceId == 'books-two';
      body = {
        'titles': [
          {
            'title': 'Ahsoka',
            'author': 'E. K. Johnston',
            'year': 2016,
            // Empty by default so most tests never start a real image fetch.
            'cover': ownedCover,
            'foreign_book_id': '29749107',
            'ebook': {
              'monitored': otherLibrary,
              'downloaded': otherLibrary,
            },
            'audiobook': {
              'monitored': !otherLibrary,
              'downloaded': false,
            },
          },
          if (mismatchedLookupId)
            {
              'title': 'Dune Messiah',
              'author': 'Frank Herbert',
              'year': 0,
              'cover': '',
              'foreign_book_id': '555',
              'ebook': {
                'monitored': true,
                'downloaded': false,
              },
              'audiobook': {
                'monitored': true,
                'downloaded': false,
              },
            },
        ],
      };
    } else if (options.path == '/api/requests/book-status') {
      statusInstanceIds.add(
        options.queryParameters['instance_id'].toString(),
      );
      body = switch (options.queryParameters['foreign_id']) {
        '29749107' => {
            'status': 'requested',
            'book_formats': {'audiobook': 'requested'},
          },
        '555' => {
            'status': partiallyUnknownStatus ? 'partial' : 'requested',
            'status_known': !partiallyUnknownStatus,
            'book_formats': partiallyUnknownStatus
                ? {
                    'ebook': 'requested',
                    'audiobook': 'future-status',
                  }
                : {
                    'ebook': 'requested',
                    'audiobook': 'requested',
                  },
          },
        _ => {'status': 'unavailable'},
      };
    } else if (options.path.endsWith('/api/v1/book/lookup')) {
      body = [
        {
          'title': 'Dune Messiah',
          'foreignBookId': mismatchedLookupId ? 'lookup-555' : '555',
          'year': 1969,
          'pageCount': 336,
          'overview': '<b>The desert planet has a new emperor.</b><br/><br/>'
              'A second chapter &amp; more.',
          'genres': ['Science Fiction'],
          'author': {
            'id': 0,
            'authorName': mismatchedLookupAuthor
                ? 'Brian Herbert'
                : 'Frank Herbert',
            'foreignAuthorId': 'author-2',
          },
        },
      ];
    } else if (options.path.endsWith('/api/v1/book/42')) {
      body = _liveBook(id: 42, mediaType: 'ebook');
    } else if (options.path.endsWith('/api/v1/book/43')) {
      body = _liveBook(id: 43, mediaType: 'audiobook');
    } else if (options.path.endsWith('/api/v1/book')) {
      body = [
        _liveBook(id: 42, mediaType: 'ebook'),
        _liveBook(id: 43, mediaType: 'audiobook'),
      ];
    } else if (options.path.endsWith('/api/v1/bookfile')) {
      final bookId = options.queryParameters['bookId'] as int?;
      body = bookFiles && (bookId == 42 || bookId == 43)
          ? [
              {
                'id': bookId! + 100,
                'bookId': bookId,
                'path': bookId == 42
                    ? '/library/E. K. Johnston/Ahsoka.epub'
                    : r'Z:\Audiobooks\E. K. Johnston\Ahsoka.m4b',
                'size': bookId == 42 ? 4200000 : 420000000,
                'quality': {
                  'quality': {'name': bookId == 42 ? 'EPUB' : 'M4B'},
                },
              },
            ]
          : <Object>[];
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

Map<String, dynamic> _liveBook({
  required int id,
  required String mediaType,
}) =>
    {
      'id': id,
      'title': 'Ahsoka',
      'foreignBookId': '29749107',
      'mediaType': mediaType,
      'monitored': true,
      'releaseDate': '2016-10-11T00:00:00Z',
      'overview': 'A former Jedi searches for a new path.',
      'author': {
        'id': 7,
        'authorName': 'E. K. Johnston',
        'foreignAuthorId': 'author-1',
      },
      'statistics': {'bookFileCount': 0, 'bookCount': 1},
    };

Finder _detailScrollable() => find.descendant(
      of: find.byType(RequesterBookDetailScreen),
      matching: find.byType(Scrollable),
    );
