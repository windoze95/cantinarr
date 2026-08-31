import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/requester_author_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

class _AuthorDetailAdapter implements HttpClientAdapter {
  _AuthorDetailAdapter({this.status = 200, this.body});

  final int status;
  final Map<String, dynamic>? body;
  final foreignIds = <String?>[];
  final instanceIds = <String?>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/book-author') {
      foreignIds.add(options.queryParameters['foreign_id'] as String?);
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
  String foreignBookId = 'fb-1',
  int year = 2024,
  bool ebookMonitored = false,
  bool ebookDownloaded = false,
  bool audiobookMonitored = false,
  bool audiobookDownloaded = false,
  bool statusKnown = true,
}) =>
    {
      'title': title,
      'author': 'Martha Wells',
      'year': year,
      'foreign_book_id': foreignBookId,
      'cover': '',
      'status_known': statusKnown,
      'ebook': {'monitored': ebookMonitored, 'downloaded': ebookDownloaded},
      'audiobook': {
        'monitored': audiobookMonitored,
        'downloaded': audiobookDownloaded,
      },
    };

Map<String, dynamic> _detailBody(List<Map<String, dynamic>> titles) => {
      'author': {
        'foreign_author_id': 'fa-1',
        'name': 'Martha Wells',
        'image': '',
        'title_count': titles.length,
        'available_count': 1,
      },
      'titles': titles,
    };

/// The location the page last pushed, so book navigation can be asserted.
String? lastPushedLocation;

Future<_AuthorDetailAdapter> _pumpAuthorPage(
  WidgetTester tester, {
  int status = 200,
  Map<String, dynamic>? body,
}) async {
  lastPushedLocation = null;
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _AuthorDetailAdapter(status: status, body: body);
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
    initialLocation: '/detail/author/fa-1?name=Martha%20Wells&instance_id=books',
    routes: [
      GoRoute(
        path: '/detail/author/:id',
        builder: (_, state) => RequesterAuthorDetailScreen(
          foreignAuthorId: state.pathParameters['id']!,
          nameHint: state.uri.queryParameters['name'],
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
  tester.view.physicalSize = const Size(900, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  testWidgets('lists the author\'s titles with their availability',
      (tester) async {
    _sizeViewport(tester);

    final adapter = await _pumpAuthorPage(
      tester,
      body: _detailBody([
        _title(
          title: 'System Collapse',
          foreignBookId: 'fb-1',
          ebookDownloaded: true,
          audiobookDownloaded: true,
        ),
        _title(
          title: 'Witch King',
          foreignBookId: 'fb-2',
          year: 2023,
          ebookMonitored: true,
        ),
        _title(title: 'Network Effect', foreignBookId: 'fb-3', year: 2020),
      ]),
    );

    expect(adapter.foreignIds, contains('fa-1'));
    // The pinned library, not whichever one the drawer happens to hold.
    expect(adapter.instanceIds, contains('books'));
    expect(find.text('System Collapse'), findsOneWidget);
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Requested'), findsOneWidget);
    // A book the library tracks but nobody asked for says so, rather than
    // rendering the same blank as a state that could not be read.
    expect(find.text('Not requested'), findsOneWidget);
  });

  testWidgets('an undetermined format state carries no pill', (tester) async {
    _sizeViewport(tester);

    await _pumpAuthorPage(
      tester,
      body: _detailBody([
        _title(
          title: 'System Collapse',
          ebookDownloaded: true,
          statusKnown: false,
        ),
      ]),
    );

    expect(find.text('System Collapse'), findsOneWidget);
    expect(find.text('Available'), findsNothing);
    expect(find.text('Not requested'), findsNothing);
  });

  testWidgets('opens the book page for the tapped title', (tester) async {
    _sizeViewport(tester);

    await _pumpAuthorPage(
      tester,
      body: _detailBody([_title(title: 'Witch King', foreignBookId: 'fb-2')]),
    );

    await tester.tap(find.text('Witch King'));
    await tester.pumpAndSettle();

    expect(lastPushedLocation, contains('/detail/book/fb-2'));
    expect(lastPushedLocation, contains('instance_id=books'));
    expect(find.text('book page'), findsOneWidget);
  });

  testWidgets('a missing author says the library was searched and came up empty',
      (tester) async {
    _sizeViewport(tester);

    await _pumpAuthorPage(tester, status: 404);

    expect(
      find.textContaining('not in your book library'),
      findsOneWidget,
    );
    // The name the row already showed still titles the page.
    expect(find.text('Martha Wells'), findsOneWidget);
  });

  testWidgets('an unreadable library does not claim the author is missing',
      (tester) async {
    _sizeViewport(tester);

    await _pumpAuthorPage(tester, status: 500);

    expect(find.textContaining('could not be loaded'), findsOneWidget);
    expect(find.textContaining('not in your book library'), findsNothing);
  });

  testWidgets('no access says so rather than blaming the connection',
      (tester) async {
    _sizeViewport(tester);

    await _pumpAuthorPage(tester, status: 403);

    expect(
      find.textContaining('do not have access to this book library'),
      findsOneWidget,
    );
  });
}
