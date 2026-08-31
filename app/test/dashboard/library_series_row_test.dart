import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/features/dashboard/ui/library_series_row.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_books_tab.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// Serves the Books tab's backend calls, with the series body under test.
class _SeriesAdapter implements HttpClientAdapter {
  _SeriesAdapter({this.status = 200, this.series, this.bySort, this.total});

  final int status;
  final List<Map<String, dynamic>>? series;
  final Map<String, List<Map<String, dynamic>>>? bySort;
  final int? total;
  final sorts = <String?>[];
  final instanceIds = <String?>[];
  Completer<void>? gate;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/book-series') {
      final sort = options.queryParameters['sort'] as String?;
      sorts.add(sort);
      instanceIds.add(options.queryParameters['instance_id'] as String?);
      if (gate != null) await gate!.future;
      if (status != 200) {
        return ResponseBody.fromString(
          jsonEncode({'error': 'unreachable'}),
          status,
          headers: {
            Headers.contentTypeHeader: [Headers.jsonContentType],
          },
        );
      }
      final rows = bySort != null
          ? (bySort![sort] ?? const <Map<String, dynamic>>[])
          : (series ?? const <Map<String, dynamic>>[]);
      return _json({'series': rows, 'total': total ?? rows.length});
    }
    if (options.path == '/api/requests/book-authors') {
      return _json({'authors': const [], 'total': 0});
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

Map<String, dynamic> _series({
  String name = 'The Dresden Files',
  List<String> covers = const [],
  int titleCount = 61,
  int availableCount = 6,
}) =>
    {
      'name': name,
      'covers': covers,
      'title_count': titleCount,
      'available_count': availableCount,
    };

String? lastPushedLocation;

Future<_SeriesAdapter> _pumpBooksTab(
  WidgetTester tester, {
  int status = 200,
  List<Map<String, dynamic>>? series,
  Map<String, List<Map<String, dynamic>>>? bySort,
  int? total,
}) async {
  lastPushedLocation = null;
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _SeriesAdapter(
    status: status,
    series: series,
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
          return Scaffold(
            body: Text('series page: ${state.pathParameters['id']}'),
          );
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
  testWidgets('shows the library series and how complete each one is',
      (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpBooksTab(tester, series: [
      _series(),
      _series(name: 'Discworld', titleCount: 84, availableCount: 84),
      _series(name: 'Wonder', titleCount: 4, availableCount: 0),
    ]);

    expect(find.text('Series'), findsOneWidget);
    expect(find.text('The Dresden Files'), findsOneWidget);
    // The gap is the point of the row: it says what is missing, not just what
    // is held.
    expect(find.text('6 of 61 books available'), findsOneWidget);
    expect(find.text('84 books · all available'), findsOneWidget);
    expect(adapter.instanceIds, contains('books'));
    expect(adapter.sorts, ['books']);
  });

  testWidgets('opens the series page, name and all', (tester) async {
    _sizeViewport(tester);

    // A real library holds series whose names contain a slash, which must
    // survive being put in a path.
    const awkward = 'Le Comte de Monte-Cristo / The Count of Monte Cristo';
    await _pumpBooksTab(tester, series: [_series(name: awkward)]);

    await tester.tap(find.text(awkward));
    await tester.pumpAndSettle();

    expect(find.text('series page: $awkward'), findsOneWidget);
    expect(lastPushedLocation, contains('instance_id=books'));
  });

  testWidgets('picking an order refetches it from the server', (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpBooksTab(tester, bySort: {
      'books': [
        _series(name: 'Zed Holds Many', titleCount: 90, availableCount: 90),
        _series(name: 'Aaron Holds One', titleCount: 9, availableCount: 1),
      ],
      'name': [
        _series(name: 'Aaron Holds One', titleCount: 9, availableCount: 1),
        _series(name: 'Zed Holds Many', titleCount: 90, availableCount: 90),
      ],
    });

    String firstCard() => tester
        .widgetList<SeriesStackCard>(find.byType(SeriesStackCard))
        .first
        .series
        .name;
    expect(firstCard(), 'Zed Holds Many');

    await tester.tap(find.text('Most books'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Name').last);
    await tester.pumpAndSettle();

    expect(adapter.sorts, ['books', 'name']);
    expect(firstCard(), 'Aaron Holds One');
  });

  testWidgets('a library with more series than the row says so',
      (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, series: [_series()], total: 143);

    expect(find.text('1 of 143'), findsOneWidget);
  });

  testWidgets('an unreadable library hides the row rather than claiming none',
      (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, status: 500, series: [_series()]);

    expect(find.text('Series'), findsNothing);
  });

  testWidgets('a library holding no series shows no row', (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, series: const []);

    expect(find.text('Series'), findsNothing);
  });

  _mainStackTests();

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

void _mainStackTests() {
  testWidgets('stacks a series\' earliest covers', (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, series: [
      _series(covers: const [
        '/MediaCover/1.jpg',
        '/MediaCover/2.jpg',
        '/MediaCover/3.jpg',
      ]),
    ]);

    final card = tester.widget<SeriesStackCard>(find.byType(SeriesStackCard));
    expect(card.covers, hasLength(3));
    // The stack is the whole point: one cover is indistinguishable from the
    // book cards in the row directly above this one.
    expect(find.byType(CachedImage), findsNWidgets(3));
  });

  testWidgets('a series with one cover draws one, in the same frame',
      (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(
        tester, series: [_series(covers: const ['/MediaCover/1.jpg'])]);

    final card = tester.widget<SeriesStackCard>(find.byType(SeriesStackCard));
    expect(card.covers, hasLength(1));
    expect(find.byType(CachedImage), findsOneWidget);
    // Same reserved width either way, so a row mixing one-cover and
    // three-cover series stays even instead of ragged.
    expect(
      tester.getSize(find.byType(SeriesStackCard)).width,
      SeriesStackCard.totalWidth(card.width),
    );
  });

  testWidgets('a series with no art still occupies the frame', (tester) async {
    _sizeViewport(tester);

    await _pumpBooksTab(tester, series: [_series(covers: const [])]);

    // One placeholder frame rather than a collapsed, ragged card.
    expect(find.byType(SeriesStackCard), findsOneWidget);
    expect(find.byType(CachedImage), findsOneWidget);
    expect(find.text('The Dresden Files'), findsOneWidget);
  });
}
