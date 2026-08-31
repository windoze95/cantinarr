import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
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
  _RecentAdapter({
    this.recentStatus = 200,
    this.items,
    this.libraryStatus = 200,
    this.titles,
  });

  final int recentStatus;
  final List<Map<String, dynamic>>? items;
  final int libraryStatus;
  final List<Map<String, dynamic>>? titles;
  final recentInstanceIds = <String?>[];
  final libraryInstanceIds = <String?>[];

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
      libraryInstanceIds
          .add(options.queryParameters['instance_id'] as String?);
      if (libraryStatus != 200) {
        return ResponseBody.fromString(
          jsonEncode({'error': 'unreachable'}),
          libraryStatus,
          headers: {
            Headers.contentTypeHeader: [Headers.jsonContentType],
          },
        );
      }
      return _json({'titles': titles ?? const []});
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
  int libraryStatus = 200,
  List<Map<String, dynamic>>? titles,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _RecentAdapter(
    recentStatus: recentStatus,
    items: items,
    libraryStatus: libraryStatus,
    titles: titles,
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

/// One owned-books digest entry for foreign book id `fb-1`.
Map<String, dynamic> _digestEntry({
  required bool ebookMonitored,
  required bool ebookDownloaded,
  required bool audiobookMonitored,
  required bool audiobookDownloaded,
  bool statusKnown = true,
  String foreignBookId = 'fb-1',
}) => {
      'title': 'Ahsoka',
      'author': 'E. K. Johnston',
      'foreign_book_id': foreignBookId,
      'ebook': {'monitored': ebookMonitored, 'downloaded': ebookDownloaded},
      'audiobook': {
        'monitored': audiobookMonitored,
        'downloaded': audiobookDownloaded,
      },
      'status_known': statusKnown,
    };

void main() {
  // The server now sends one card per title. These two cases keep the row
  // honest against an *older* server, which still sends a card per format.
  testWidgets('an older server\'s per-format cards still render', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(tester, items: _twoFormatsOfOneTitle());

    expect(find.text('Recently Added'), findsOneWidget);
    expect(find.byType(MediaCard), findsNWidgets(2));
    // With no ownership to show, each card falls back to its own record's
    // format label — which is what made two cards worth showing before the
    // card started leading with title-level ownership instead.
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('eBook'), findsOneWidget);
  });

  _mainMergedCardTests();

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

  testWidgets('stays silent when the user has no book access', (tester) async {
    await _pumpBooksTab(tester, recentStatus: 403);

    expect(find.text('Recently Added'), findsNothing);
    // A missing row must not look like a failure.
    expect(find.textContaining('access'), findsNothing);
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
    // The two fetches must never drift onto different instances.
    expect(adapter.libraryInstanceIds, isNotEmpty);
    expect(adapter.libraryInstanceIds.every((id) => id == 'books'), isTrue);
  });

  testWidgets(
      'a fully-downloaded title shows a green Available pill and the '
      'eBook + Audiobook subtitle', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(
      tester,
      items: _twoFormatsOfOneTitle(),
      titles: [
        _digestEntry(
          ebookMonitored: false,
          ebookDownloaded: true,
          audiobookMonitored: false,
          audiobookDownloaded: true,
        ),
      ],
    );

    final cards = tester.widgetList<MediaCard>(find.byType(MediaCard));
    expect(cards, hasLength(2));
    for (final card in cards) {
      expect(card.statusLabel, 'Available');
      expect(card.statusColor, AppTheme.available);
    }
    expect(find.text('eBook + Audiobook'), findsNWidgets(2));
  });

  testWidgets(
      'an empty digest leaves cards with no pill and their own format label',
      (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(
      tester,
      items: _twoFormatsOfOneTitle(),
      titles: const [],
    );

    final cards = tester.widgetList<MediaCard>(find.byType(MediaCard));
    expect(cards, hasLength(2));
    for (final card in cards) {
      expect(card.statusLabel, isNull);
      expect(card.statusColor, isNull);
    }
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
  });

  testWidgets(
      'a recent record with an empty foreignBookId never matches an '
      'empty-id digest row', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(
      tester,
      items: [
        {
          'book_id': 7,
          'foreign_book_id': '',
          'title': 'Orphaned Record',
          'format': 'ebook',
          'cover': '',
          'imported_at': '2026-07-24T12:00:00Z',
        },
      ],
      titles: [
        _digestEntry(
          ebookMonitored: false,
          ebookDownloaded: true,
          audiobookMonitored: false,
          audiobookDownloaded: true,
          foreignBookId: '',
        ),
      ],
    );

    final card = tester.widget<MediaCard>(find.byType(MediaCard));
    expect(card.statusLabel, isNull);
  });

  testWidgets(
      'ROADMAP Phase 2 criterion 1: a monitored missing format renders '
      'Requested', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(
      tester,
      items: _twoFormatsOfOneTitle(),
      titles: [
        _digestEntry(
          ebookMonitored: false,
          ebookDownloaded: true,
          audiobookMonitored: true,
          audiobookDownloaded: false,
        ),
      ],
    );

    final cards = tester.widgetList<MediaCard>(find.byType(MediaCard));
    expect(cards, hasLength(2));
    for (final card in cards) {
      expect(card.statusLabel, 'Requested');
      expect(card.statusColor, AppTheme.requested);
      expect(card.subtitle, 'eBook + Audiobook requested');
    }
  });

  testWidgets(
      'ROADMAP Phase 2 criterion 2: a never-requested missing format '
      'renders Partial', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(
      tester,
      items: _twoFormatsOfOneTitle(),
      titles: [
        _digestEntry(
          ebookMonitored: false,
          ebookDownloaded: true,
          audiobookMonitored: false,
          audiobookDownloaded: false,
        ),
      ],
    );

    final cards = tester.widgetList<MediaCard>(find.byType(MediaCard));
    expect(cards, hasLength(2));
    for (final card in cards) {
      expect(card.statusLabel, 'Partial');
      expect(card.statusColor, AppTheme.requested);
      expect(card.subtitle, 'eBook');
    }
  });

  testWidgets(
      'an unreadable ownership digest hides the whole row, not just its pills',
      (tester) async {
    await _pumpBooksTab(
      tester,
      items: _twoFormatsOfOneTitle(),
      libraryStatus: 500,
    );

    expect(find.text('Recently Added'), findsNothing);
    expect(find.byType(MediaCard), findsNothing);
    expect(find.textContaining('error'), findsNothing);
    expect(find.textContaining('Error'), findsNothing);
  });

  testWidgets(
      'an older server\'s two cards for one title cannot contradict each other',
      (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBooksTab(
      tester,
      items: _twoFormatsOfOneTitle(),
      titles: [
        _digestEntry(
          ebookMonitored: false,
          ebookDownloaded: true,
          audiobookMonitored: true,
          audiobookDownloaded: false,
        ),
      ],
    );

    final cards =
        tester.widgetList<MediaCard>(find.byType(MediaCard)).toList();
    expect(cards, hasLength(2));
    expect(cards[0].id, isNot(cards[1].id));
    expect(cards[0].statusLabel, cards[1].statusLabel);
    expect(cards[0].statusColor, cards[1].statusColor);
    expect(cards[0].subtitle, cards[1].subtitle);
    expect(cards[0].statusLabel, 'Requested');
    expect(cards[0].statusColor, AppTheme.requested);
    expect(cards[0].subtitle, 'eBook + Audiobook requested');
  });
}

void _mainMergedCardTests() {
  testWidgets('a card covering both formats claims neither on its own',
      (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    // What the server sends for a title whose ebook and audiobook both landed:
    // one card, and no single format that describes it.
    await _pumpBooksTab(tester, items: [
      {
        'book_id': 8,
        'foreign_book_id': 'fb-1',
        'title': 'Ahsoka',
        'format': '',
        'cover': '',
        'imported_at': '2026-07-24T12:00:00Z',
      },
    ]);

    expect(find.byType(MediaCard), findsOneWidget);
    final card = tester.widget<MediaCard>(find.byType(MediaCard));
    // No ownership resolved and no single format: saying "eBook" here would
    // name one half of a card that covers both.
    expect(card.subtitle, isNull);
    expect(card.statusLabel, isNull);
  });
}
