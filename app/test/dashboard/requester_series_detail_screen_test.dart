import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/requester_series_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

class _SeriesDetailAdapter implements HttpClientAdapter {
  _SeriesDetailAdapter({this.status = 200, this.body});

  final int status;
  final Map<String, dynamic>? body;
  final names = <String?>[];
  final instanceIds = <String?>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/book-series-detail') {
      names.add(options.queryParameters['name'] as String?);
      instanceIds.add(options.queryParameters['instance_id'] as String?);
      return ResponseBody.fromString(
        jsonEncode(status == 200 ? (body ?? const {}) : {'error': 'nope'}),
        status,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    }
    return ResponseBody.fromString(
      jsonEncode(const <String, dynamic>{}),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

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

Map<String, dynamic> _title({
  required String title,
  required String position,
  String foreignBookId = 'fb-1',
  String author = 'Jim Butcher',
  int year = 2000,
  bool ebookDownloaded = false,
  bool audiobookDownloaded = false,
  bool ebookMonitored = false,
}) =>
    {
      'title': title,
      'author': author,
      'year': year,
      'foreign_book_id': foreignBookId,
      'cover': '',
      'status_known': true,
      'position': position,
      'ebook': {'monitored': ebookMonitored, 'downloaded': ebookDownloaded},
      'audiobook': {'monitored': false, 'downloaded': audiobookDownloaded},
    };

Map<String, dynamic> _detailBody(List<Map<String, dynamic>> titles) => {
      'series': {
        'name': 'The Dresden Files',
        'covers': <String>[],
        'title_count': 61,
        'available_count': 6,
      },
      'titles': titles,
    };

String? lastPushedLocation;

Future<_SeriesDetailAdapter> _pumpSeriesPage(
  WidgetTester tester, {
  int status = 200,
  Map<String, dynamic>? body,
  String name = 'The Dresden Files',
}) async {
  lastPushedLocation = null;
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _SeriesDetailAdapter(status: status, body: body);
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
    initialLocation: '/detail/series/x?instance_id=books',
    routes: [
      GoRoute(
        path: '/detail/series/:id',
        builder: (_, state) => RequesterSeriesDetailScreen(
          seriesName: name,
          instanceId: state.uri.queryParameters['instance_id'],
        ),
      ),
      GoRoute(
        path: '/detail/book/:id',
        builder: (_, state) {
          lastPushedLocation = state.uri.toString();
          return const Scaffold(body: Text('book page'));
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
  tester.view.physicalSize = const Size(900, 1600);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  testWidgets('lists the series in reading order, gaps included',
      (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpSeriesPage(
      tester,
      body: _detailBody([
        _title(
            title: 'Storm Front',
            position: '1',
            foreignBookId: 'fb-1',
            ebookDownloaded: true,
            audiobookDownloaded: true),
        _title(
            title: 'Fool Moon',
            position: '2',
            foreignBookId: 'fb-2',
            ebookMonitored: true),
        _title(title: 'Grave Peril', position: '3', foreignBookId: 'fb-3'),
        _title(title: 'Side Jobs', position: '', foreignBookId: 'fb-4'),
      ]),
    );

    expect(adapter.names, contains('The Dresden Files'));
    expect(adapter.instanceIds, contains('books'));
    expect(find.text('6 of 61 books available'), findsOneWidget);
    expect(find.text('Storm Front'), findsOneWidget);
    // The gap is the reason to open the page.
    expect(find.text('Not requested'), findsNWidgets(2));
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Requested'), findsOneWidget);
    // Positions are the library's own labels; a title stating none shows a
    // dash rather than being renumbered into the run.
    expect(find.text('1'), findsOneWidget);
    expect(find.text('—'), findsOneWidget);
  });

  testWidgets('keeps an odd position label rather than renumbering it',
      (tester) async {
    _sizeViewport(tester);

    await _pumpSeriesPage(
      tester,
      body: _detailBody([
        _title(title: 'Split Volume', position: '2A', foreignBookId: 'fb-1'),
      ]),
    );

    expect(find.text('2A'), findsOneWidget);
  });

  testWidgets('opens the book page for the tapped title', (tester) async {
    _sizeViewport(tester);

    await _pumpSeriesPage(
      tester,
      body: _detailBody([
        _title(title: 'Storm Front', position: '1', foreignBookId: 'fb-1'),
      ]),
    );

    await tester.tap(find.text('Storm Front'));
    await tester.pumpAndSettle();

    expect(lastPushedLocation, contains('/detail/book/fb-1'));
    expect(lastPushedLocation, contains('instance_id=books'));
  });

  testWidgets('a missing series says the library came up empty',
      (tester) async {
    _sizeViewport(tester);

    await _pumpSeriesPage(tester, status: 404);

    expect(find.textContaining('not in your book library'), findsOneWidget);
    // The name the row already showed still titles the page.
    expect(find.text('The Dresden Files'), findsOneWidget);
  });

  testWidgets('an unreadable library does not claim the series is missing',
      (tester) async {
    _sizeViewport(tester);

    await _pumpSeriesPage(tester, status: 500);

    expect(find.textContaining('could not be loaded'), findsOneWidget);
    expect(find.textContaining('not in your book library'), findsNothing);
  });

  testWidgets('no access says so rather than blaming the connection',
      (tester) async {
    _sizeViewport(tester);

    await _pumpSeriesPage(tester, status: 403);

    expect(find.textContaining('do not have access to this book library'),
        findsOneWidget);
  });
}
