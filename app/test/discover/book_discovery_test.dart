import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'dart:ui' show PointerDeviceKind;
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_books_tab.dart';
import 'package:cantinarr/features/dashboard/ui/library_authors_row.dart';
import 'package:cantinarr/features/dashboard/ui/library_series_row.dart';
import 'package:cantinarr/features/dashboard/ui/recently_added_books_row.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/features/dashboard/ui/trending_books_row.dart';
import 'package:cantinarr/features/discover/ui/book_search_results_view.dart';
import 'package:cantinarr/features/request/ui/book_format_panel.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const nativeTitle = 'The Subtle Art of Not Giving a Fuck';
const nativeID = 'gr:48297245';
void main() {
  testWidgets('Books retains its native shelves without public traffic',
      (t) async {
    final h = await pump(t);
    final scroll = t.widget<SingleChildScrollView>(find
        .descendant(
            of: find.byType(DashboardBooksTab),
            matching: find.byType(SingleChildScrollView))
        .first);
    expect((scroll.child as Column).children.map((w) => w.runtimeType), [
      TrendingBooksRow,
      RecentlyAddedBooksRow,
      LibraryAuthorsRow,
      LibrarySeriesRow,
    ]);
    expect(h.adapter.retiredReads, isEmpty);
  });
  testWidgets('a Hardcover trending book keeps its native lookup, instance, '
      'and request state on the detail page', (t) async {
    const hardcoverId = 'hc:123';
    final h = await pump(t,
        adapter: Adapter()
          ..lookupID = hardcoverId
          ..trendingBooks = [
            {
              'hardcover_id': 123,
              'foreign_id': hardcoverId,
              'title': nativeTitle,
              'authors': ['Mark Manson'],
            },
          ]);
    expect(h.adapter.lookupTerms, isEmpty);
    await t.tap(find.text(nativeTitle));
    await t.pumpAndSettle();

    final page = t.widget<RequesterBookDetailScreen>(
        find.byType(RequesterBookDetailScreen));
    expect(page.foreignId, hardcoverId);
    expect(page.instanceId, 'books');
    expect(page.initialBook, isNull);
    expect(h.adapter.lookupTerms, [hardcoverId]);
    expect(find.text('A book by Mark Manson'), findsOneWidget);

    final panel = find.byType(BookFormatPanel);
    final state = t.state(panel);
    final target = find.byKey(const ValueKey('book-format-row:ebook'));
    await t.ensureVisible(target);
    await t.tap(target);
    await t.pumpAndSettle();
    expect(h.adapter.posts.single, containsPair('foreign_id', hardcoverId));
    expect(h.adapter.posts.single, containsPair('instance_id', 'books'));
    expect(h.adapter.posts.single, containsPair('book_format', 'ebook'));

    h.router.refresh();
    await t.pumpAndSettle();
    expect(t.state(panel), same(state));
    expect(find.text('Your request is saved.'), findsWidgets);
    expect(find.text('A book by Mark Manson'), findsOneWidget);
    expect(h.adapter.lookupTerms, [hardcoverId]);
    expect(h.adapter.retiredReads, isEmpty);
  });
  for (final route in [
    '/detail/book/ol:OL1W?title=Mark+Manson&source=openlibrary&instance_id=books',
    '/browse/books/popular',
    '/browse/books/genre?genre=fantasy'
  ]) {
    testWidgets('retired link is stable and makes no provider request: $route',
        (t) async {
      final h = await pump(t, location: route);
      expect(find.textContaining('Open Library book discovery has retired'),
          findsOneWidget);
      h.container.read(libraryRefreshTickProvider.notifier).state++;
      await t.pump(const Duration(seconds: 31));
      expect(find.textContaining('Open Library book discovery has retired'),
          findsOneWidget);
      expect(h.adapter.retiredReads, isEmpty);
      await t.tap(find.text('Search books'));
      await t.pumpAndSettle();
      expect(h.router.routerDelegate.currentConfiguration.uri.path,
          '/dashboard/books');
      if (route.contains('title=')) {
        final field = find.descendant(
            of: find.byType(CantinarrSearchBar),
            matching: find.byType(TextField));
        expect(t.widget<TextField>(field).controller!.text, 'Mark Manson');
      }
    });
  }
  testWidgets('books render within 300 ms while author lookup is still waiting',
      (t) async {
    final authors = Completer<void>();
    final h = await pump(t, adapter: Adapter()..authorWait = authors.future);
    final field = find.descendant(
        of: find.byType(CantinarrSearchBar), matching: find.byType(TextField));
    await t.enterText(field, nativeTitle);
    await t.pump(const Duration(milliseconds: 350));
    await t.pump(const Duration(milliseconds: 100));
    expect(
        find.byKey(const ValueKey('book-result:$nativeID:$nativeID:lookup:0')),
        findsOneWidget);
    expect(find.text('Searching authors…'), findsOneWidget);
    final view = find.byType(BookSearchResultsView);
    final bookRow =
        find.byKey(const ValueKey('book-result:$nativeID:$nativeID:lookup:0'));
    final before = t.getTopLeft(bookRow);
    authors.complete();
    await t.pump(const Duration(milliseconds: 100));
    expect(t.getTopLeft(bookRow).dy, closeTo(before.dy, 1));
    expect(
        t.getTopLeft(find.text('Mark Manson').last).dy, greaterThan(before.dy));
    expect(view, findsOneWidget);
    await t.tap(
        find.byKey(const ValueKey('book-result:$nativeID:$nativeID:lookup:0')));
    await t.pumpAndSettle();
    final page = t.widget<RequesterBookDetailScreen>(
        find.byType(RequesterBookDetailScreen));
    expect(page.foreignId, nativeID);
    expect(page.instanceId, 'books');
    expect(page.initialBook?.author?.authorName, 'Mark Manson');
    expect(page.searchTerm, nativeTitle);
    expect(h.adapter.retiredReads, isEmpty);
  });
  testWidgets(
      'pausing, scrolling, and opening search results do not fetch metadata',
      (t) async {
    final h = await pump(t, adapter: Adapter()..extraResults = 30);
    final field = find.descendant(
        of: find.byType(CantinarrSearchBar), matching: find.byType(TextField));
    await t.enterText(field, nativeTitle);
    await t.pump(const Duration(milliseconds: 350));
    await t.pumpAndSettle();
    final results = find.byType(BookSearchResultsView);
    final selected = t.widget<BookSearchResultsView>(results).results.first;
    final scrollable =
        find.descendant(of: results, matching: find.byType(Scrollable));
    final row =
        find.byKey(const ValueKey('book-result:$nativeID:$nativeID:lookup:0'));
    final mouse = await t.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: t.getCenter(row));
    await mouse.moveTo(t.getCenter(row));
    await t.pump(const Duration(seconds: 2));
    expect(h.adapter.lookupTerms, [nativeTitle]);

    await t.drag(scrollable, const Offset(0, -650));
    await t.pumpAndSettle();
    expect(
        t.state<ScrollableState>(scrollable).position.pixels, greaterThan(0));
    await t.pump(const Duration(seconds: 2));
    expect(h.adapter.lookupTerms, [nativeTitle]);
    await t.scrollUntilVisible(row, -350, scrollable: scrollable);
    await t.tap(row);
    await t.pumpAndSettle();
    final page = t.widget<RequesterBookDetailScreen>(
        find.byType(RequesterBookDetailScreen));
    expect(page.initialBook, same(selected));
    expect(page.initialBook?.foreignEditionId, 'gr:edition');
    expect(page.foreignId, nativeID);
    expect(page.instanceId, 'books');
    expect(page.searchTerm, nativeTitle);
    expect(find.text('A book by Mark Manson'), findsOneWidget);
    await t.pump(const Duration(seconds: 2));
    expect(h.adapter.lookupTerms, [nativeTitle]);
    await mouse.removePointer();
  });
  for (final format in ['ebook', 'audiobook', 'both']) {
    testWidgets(
        'one $format tap acknowledges before delayed or failed status refresh',
        (t) async {
      final a = Adapter();
      final h = await pump(t,
          adapter: a,
          location:
              '/detail/book/$nativeID?title=${Uri.encodeQueryComponent(nativeTitle)}&q=Mark+Manson&instance_id=books');
      final panel = find.byType(BookFormatPanel);
      final state = t.state(panel);
      final target = find.byKey(ValueKey(
          format == 'both' ? 'book-request-both' : 'book-format-row:$format'));
      await t.ensureVisible(target);
      final scroll = t
          .state<ScrollableState>(find
              .descendant(
                  of: find.byType(RequesterBookDetailScreen),
                  matching: find.byType(Scrollable))
              .first)
          .position;
      final offset = scroll.pixels;
      final pending = Completer<void>();
      a.statusWait = pending.future;
      await t.tap(target);
      await t.pump(const Duration(milliseconds: 100));
      expect(a.posts, hasLength(1));
      expect(a.posts.single, containsPair('foreign_id', nativeID));
      expect(a.posts.single, containsPair('instance_id', 'books'));
      expect(a.posts.single, containsPair('search_term', 'Mark Manson'));
      expect(a.posts.single, containsPair('book_format', format));
      expect(a.posts.single.containsKey('catalog_ref'), isFalse);
      expect(find.text('Your request is saved.'), findsWidgets);
      expect(t.state(panel), same(state));
      expect(scroll.pixels, offset);
      await t.tap(target);
      await t.pump();
      expect(a.posts, hasLength(1));
      h.container.read(libraryRefreshTickProvider.notifier).state++;
      await t.pump(const Duration(milliseconds: 100));
      expect(t.state(panel), same(state));
      expect(scroll.pixels, offset);
      a.failStatus = true;
      pending.complete();
      await t.pumpAndSettle();
      expect(t.state(panel), same(state));
      expect(scroll.pixels, offset);
      expect(
          find.byKey(const ValueKey('book-format-row:ebook')), findsOneWidget);
      expect(find.byKey(const ValueKey('book-format-row:audiobook')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('book-request-both')), findsOneWidget);
      expect(a.retiredReads, isEmpty);
      expect(a.savedReads.every((q) => q['include_live'] == false), isTrue);
      expect(find.text('Cancel request'), findsOneWidget);
    });
  }
  testWidgets('Request both keeps the owned format available', (t) async {
    final a = Adapter()..ownedFormat = 'ebook';
    await pump(t,
        adapter: a,
        location: '/detail/book/$nativeID?title=Selected&instance_id=books');
    final button = find.byKey(const ValueKey('book-request-both'));
    await t.ensureVisible(button);
    await t.tap(button);
    await t.pumpAndSettle();
    expect(a.posts.single['book_format'], 'both');
    expect(
        find.descendant(
            of: find.byKey(const ValueKey('book-format-row:ebook')),
            matching: find.text('Available')),
        findsOneWidget);
    expect(find.text('Cancel request'), findsOneWidget);
  });
  testWidgets('a native Open Library ID stays native after a link reload',
      (t) async {
    final h = await pump(t,
        location:
            '/detail/book/ol:OL1W?source=chaptarr&title=Selected&instance_id=books');
    expect(find.byType(BookFormatPanel), findsOneWidget);
    expect(find.textContaining('Open Library book discovery has retired'),
        findsNothing);
    expect(h.adapter.retiredReads, isEmpty);
  });
  testWidgets('a retired link stays readable without a library grant',
      (t) async {
    final h = await pump(t);
    (h.container.read(authProvider.notifier) as TestAuth)
        .replace(auth(grant: false));
    await t.pumpAndSettle();
    h.router.go('/detail/book/ol:OL1W?title=Selected');
    await t.pumpAndSettle();
    expect(find.textContaining('Open Library book discovery has retired'),
        findsOneWidget);
    expect(find.text('Search books'), findsOneWidget);
    expect(h.adapter.retiredReads, isEmpty);
  });
}

AuthState auth({bool grant = true}) => AuthState(
    connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(chaptarr: grant),
        instances: [
          if (grant)
            const ServiceInstance(
                id: 'books',
                name: 'Books',
                serviceType: 'chaptarr',
                isDefault: true)
        ]),
    user: const UserProfile(
        id: 1,
        username: 'test',
        role: 'user',
        permissions: ['media:discover', 'media:request']));

class TestAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => auth();
  void replace(AuthState next) {
    state = AsyncData(next);
  }
}

Dio dio(Adapter a) =>
    Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = a;
Future<({GoRouter router, ProviderContainer container, Adapter adapter})> pump(
    WidgetTester t,
    {Adapter? adapter,
    String location = '/dashboard/books',
    Stream<WsEvent>? events}) async {
  t.view.physicalSize = const Size(390, 900);
  t.view.devicePixelRatio = 1;
  addTearDown(() {
    t.view.resetPhysicalSize();
    t.view.resetDevicePixelRatio();
  });
  final a = adapter ?? Adapter();
  final c = ProviderContainer(overrides: [
    authProvider.overrideWith(TestAuth.new),
    backendClientProvider.overrideWithValue(dio(a)),
    libraryChangedEventsProvider
        .overrideWith((_) => events ?? const Stream.empty())
  ]);
  addTearDown(c.dispose);
  await c.read(authProvider.future);
  await c.pump();
  final r = c.read(appRouterProvider)..go(location);
  await t.pumpWidget(UncontrolledProviderScope(
      container: c,
      child: OwnedTestContainer(
          container: c, child: MaterialApp.router(routerConfig: r))));
  await t.pumpAndSettle();
  return (router: r, container: c, adapter: a);
}

class Adapter implements HttpClientAdapter {
  final lookupTerms = <String>[];
  String lookupID = nativeID;
  List<Map<String, dynamic>> trendingBooks = const [];
  int extraResults = 0;
  final retiredReads = <String>[];
  final posts = <Map<String, dynamic>>[];
  final savedReads = <Map<String, dynamic>>[];
  Future<void>? authorWait, statusWait;
  bool failStatus = false;
  String? ownedFormat;
  List<Map<String, dynamic>> get delivery => posts.isEmpty
      ? []
      : [
          for (final format in (posts.last['book_format'] == 'both'
              ? ['ebook', 'audiobook']
              : [posts.last['book_format']]))
            {
              'request_id': 1,
              'format': format,
              'state': 'queued',
              'message': 'Your request is saved.',
              'can_cancel': true,
              'can_manage': true
            }
        ];
  @override
  Future<ResponseBody> fetch(RequestOptions o, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    Object data = <String, dynamic>{};
    var code = 200;
    if (o.path == '/api/discover/books/trending') {
      // The live feed stays out of the way unless the test supplies books.
      data = {
        'instance_id': 'books',
        'connected': trendingBooks.isNotEmpty,
        'books': trendingBooks,
      };
    } else if (o.path.startsWith('/api/discover/books') ||
        o.path == '/api/genres/book' ||
        o.path.startsWith('/api/media/book/')) {
      retiredReads.add(o.path);
      code = 410;
    } else if (o.path.endsWith('/book/lookup')) {
      lookupTerms.add(o.queryParameters['term'].toString());
      data = [
        {
          'foreignBookId': lookupID,
          'foreignEditionId': 'gr:edition',
          'title': nativeTitle,
          'author': {'authorName': 'Mark Manson', 'foreignAuthorId': 'gr:1'},
          'overview': 'A book by Mark Manson'
        },
        {
          'foreignBookId': 'gr:summary',
          'title': 'Summary of $nativeTitle',
          'author': {'authorName': 'Another Author'}
        },
        for (var i = 0; i < extraResults; i++)
          {'foreignBookId': 'gr:extra-$i', 'title': 'Another book $i'},
      ];
    } else if (o.path.endsWith('/author/lookup')) {
      if (authorWait != null) await authorWait;
      data = [
        {'id': 0, 'authorName': 'Mark Manson', 'foreignAuthorId': 'gr:1'}
      ];
    } else if (o.path.endsWith('/author') || o.path.endsWith('/book')) {
      data = [];
    } else if (o.path == '/api/requests/delivery-status') {
      savedReads.add(Map<String, dynamic>.from(o.queryParameters));
      data = {
        'success': true,
        'status': 'requested',
        'request_id': posts.isEmpty ? 0 : 1,
        'delivery': delivery
      };
    } else if (o.path == '/api/requests/book-status') {
      if (statusWait != null) await statusWait;
      if (failStatus) code = 503;
      data = {
        'status': 'unavailable',
        'status_known': true,
        'book_formats': {
          for (final f in ['ebook', 'audiobook'])
            f: ownedFormat == f ? 'available' : 'unavailable'
        }
      };
    } else if (o.path == '/api/requests' && o.method == 'POST') {
      posts.add(Map<String, dynamic>.from(o.data as Map));
      data = {
        'status': 'requested',
        'request_id': 1,
        'delivery': delivery,
        'message': 'Your request is saved.',
        'book_formats': {for (final d in delivery) d['format']: 'requested'},
        'book_format_waits': {
          for (final d in delivery) d['format']: {'reason': 'queued'}
        }
      };
    } else if (o.path == '/api/requests/book-library') {
      data = {'titles': []};
    } else if (o.path == '/api/requests/book-recent') {
      data = {'books': []};
    } else if (o.path == '/api/requests/book-authors') {
      data = {'authors': []};
    } else if (o.path == '/api/requests/book-series') {
      data = {'series': []};
    } else if (o.path == '/api/requests') {
      data = [];
    } else if (o.path.startsWith('/api/discover') ||
        o.path.startsWith('/api/trakt')) {
      data = {'results': [], 'page': 1, 'total_pages': 1};
    }
    return ResponseBody.fromString(jsonEncode(data), code, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}

class OwnedTestContainer extends StatefulWidget {
  final ProviderContainer container;
  final Widget child;
  const OwnedTestContainer(
      {super.key, required this.container, required this.child});
  @override
  State<OwnedTestContainer> createState() => OwnedTestContainerState();
}

class OwnedTestContainerState extends State<OwnedTestContainer> {
  @override
  void dispose() {
    widget.container.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
