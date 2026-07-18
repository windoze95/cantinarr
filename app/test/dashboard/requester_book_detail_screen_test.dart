import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
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
    expect(find.text('E. K. Johnston · 2016'), findsOneWidget);
    // The ebook is monitored (not downloaded) → In Library, and its open
    // audiobook keeps the requester affordance available: an already-requested
    // ebook plus an open audiobook reads "Request more".
    expect(find.text('In Library'), findsOneWidget);
    expect(find.text('Request more'), findsOneWidget);
  });

  testWidgets('the deep-link title names a book the library cannot resolve',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester);

    router.go('/detail/book/555?title=Dune%20Messiah');
    await tester.pumpAndSettle();

    expect(find.text('Dune Messiah'), findsOneWidget);
    // No request rows and no ownership → a plain Request affordance.
    expect(find.text('Request'), findsOneWidget);
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

Future<({ProviderContainer container, GoRouter router})> _pumpRouter(
    WidgetTester tester) async {
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(_booksState)),
      backendClientProvider.overrideWithValue(_fakeDio()),
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

Dio _fakeDio() {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _BooksAdapter();
  return dio;
}

/// Serves the two endpoints the detail surface reads — the owned-books digest
/// and the per-user book request status — plus empty generic payloads for the
/// dashboard feeds behind the initial route.
class _BooksAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body;
    if (options.path == '/api/requests/book-library') {
      body = {
        'titles': [
          {
            'title': 'Ahsoka',
            'author': 'E. K. Johnston',
            'year': 2016,
            // Empty on purpose: a non-empty cover would start a real image
            // fetch inside the test.
            'cover': '',
            'foreign_book_id': '29749107',
            'ebook': {'monitored': true, 'downloaded': false},
            'audiobook': {'monitored': false, 'downloaded': false},
          },
        ],
      };
    } else if (options.path == '/api/requests/book-status') {
      body = options.queryParameters['foreign_id'] == '29749107'
          ? {
              'status': 'requested',
              'book_formats': {'ebook': 'requested'},
            }
          : {'status': 'unavailable'};
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
