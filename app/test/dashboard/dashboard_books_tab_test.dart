import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
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
    expect(find.text('Requested'), findsOneWidget);
    expect(find.byIcon(Icons.chevron_right), findsWidgets);

    final statusRequestsBeforeRefresh = adapter.statusRequests;
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();
    expect(adapter.statusRequests, greaterThan(statusRequestsBeforeRefresh));

    await tester.tap(find.byKey(const ValueKey('book-result:book-1')));
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Marcus Aurelius'), findsOneWidget);
    expect(find.text('2002 · 304 pages'), findsOneWidget);
    expect(find.text('A practical guide to Stoic philosophy.'), findsOneWidget);
    expect(find.text('Requested'), findsNWidgets(3));
  });

  testWidgets('the trailing Request control does not open book detail',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(tester);
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

    final secondResult = find.byKey(const ValueKey('book-result:book-2'));
    await tester.tap(
      find.descendant(of: secondResult, matching: find.text('Request')),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsNothing);
    expect(find.text('Letters from a Stoic'), findsWidgets);
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('eBook + Audiobook'), findsOneWidget);
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
})> _pumpRouter(WidgetTester tester) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _BooksSearchAdapter();
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
  int statusRequests = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body;
    if (options.path == '/api/requests/book-library') {
      body = {'titles': <Object>[]};
    } else if (options.path == '/api/requests/book-status') {
      statusRequests++;
      body = options.queryParameters['foreign_id'] == 'book-1'
          ? {
              'status': 'requested',
              'book_formats': {
                'ebook': 'requested',
                'audiobook': 'requested',
              },
            }
          : {'status': 'unavailable'};
    } else if (options.path.endsWith('/api/v1/book/lookup')) {
      body = [
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
