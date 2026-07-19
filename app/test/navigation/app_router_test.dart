import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/app_ambient_background.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/ai_assistant/ui/codex_connection_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/ui/auth_screen.dart';
import 'package:cantinarr/features/auth/ui/set_password_screen.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_shell.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/features/shell/ui/app_shell.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_module_shell.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  test('router instance stays stable across auth state changes', () {
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_authedState)),
      ],
    );
    addTearDown(container.dispose);

    final first = container.read(appRouterProvider);
    expect(first, isA<GoRouter>());

    // An auth-state change (e.g. token refresh or profile reload) must NOT
    // recreate the router — recreating it resets navigation to the initial
    // route, which is what bounced the user out of nested screens.
    (container.read(authProvider.notifier) as _FakeAuthNotifier).push(
      AuthState(
        connection: _authedState.connection!.copyWith(accessToken: 'access2'),
        user: _authedState.user,
      ),
    );

    final second = container.read(appRouterProvider);
    expect(identical(first, second), isTrue,
        reason:
            'auth changes should refresh redirects, not rebuild the router');
  });

  testWidgets('non-admin instance module routes redirect to dashboard',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/radarr/library');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('authentication returns an internal deep link to its target',
      (tester) async {
    final (:container, :router) = await _pumpRouter(tester, const AuthState());

    router.go('/settings/password');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/login');

    (container.read(authProvider.notifier) as _FakeAuthNotifier)
        .push(_authedState);
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/settings/password',
    );
  });

  testWidgets(
      'desktop secondary routes retain AppShell and hide module-global search',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      _authedState,
      surfaceSize: const Size(1200, 900),
    );

    // Search is global to module pages, not every authenticated screen.
    expect(find.byType(CantinarrSearchBar), findsOneWidget);

    router.go('/settings/password');
    await tester.pumpAndSettle();

    expect(find.byType(SetPasswordScreen), findsOneWidget);
    expect(find.byType(AppShell), findsOneWidget);
    expect(find.text('CANTINARR'), findsOneWidget);
    expect(find.byType(CantinarrSearchBar), findsNothing);
  });

  testWidgets('non-admin users are redirected from admin-only root routes',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    for (final path in [
      '/approvals',
      '/issues',
      '/agent-actions',
      '/agent-runs/1',
      '/setup',
      '/settings/credentials',
      '/settings/ai-tools',
      '/settings/users',
      '/settings/request-settings',
      '/settings/devices',
      '/settings/plex',
      '/settings/instance/new',
    ]) {
      router.go(path);
      await tester.pumpAndSettle();
      expect(
        router.routeInformationProvider.value.uri.path,
        '/dashboard/movies',
        reason: '$path must remain admin-only',
      );
    }
  });

  testWidgets('a requester can still open a specific issue thread',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/issues/42');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/issues/42');
  });

  testWidgets('a requester can open their ChatGPT connection settings',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/settings/chatgpt');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/settings/chatgpt');
    expect(find.byType(CodexConnectionScreen), findsOneWidget);
  });

  testWidgets('books route requires the Chaptarr grant', (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('books route remains available with the Chaptarr grant',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/dashboard/books');
  });

  testWidgets('book detail route requires the Chaptarr grant', (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('book detail route resolves with the Chaptarr grant',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/detail/book/29749107',
    );
    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
  });

  testWidgets('a blank book detail id degrades to the Books tab',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    // %20 decodes to a whitespace-only foreign id — malformed for a book.
    router.go('/detail/book/%20');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/dashboard/books');
  });

  testWidgets('malformed parameter routes redirect without throwing',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _adminState);

    for (final path in [
      '/detail/movie/not-a-number',
      '/detail/movie/0',
      '/detail/podcast/12',
    ]) {
      router.go(path);
      await tester.pumpAndSettle();
      expect(
        router.routeInformationProvider.value.uri.path,
        '/dashboard/movies',
        reason: '$path must not reach MediaDetailScreen',
      );
    }

    router.go('/settings/users/not-a-number/request-settings');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/settings/users');
  });

  // Scaffolds are transparent by theme, so every routed page must paint its
  // own ambient backdrop — a page without one lets the previous route show
  // through mid-transition as a double exposure.
  testWidgets('routed pages paint their own opaque ambient backdrop',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    // Module page: backdrop on the shell page AND the module shell page.
    expect(
      find.ancestor(
        of: find.byType(DashboardShell),
        matching: find.byType(AppAmbientBackground),
      ),
      findsNWidgets(2),
    );

    // Pushed secondary route: its own backdrop plus the shell page's.
    router.push('/settings/password');
    await tester.pumpAndSettle();
    expect(
      find.ancestor(
        of: find.byType(SetPasswordScreen),
        matching: find.byType(AppAmbientBackground),
      ),
      findsNWidgets(2),
    );
  });

  testWidgets('the login page paints its own opaque ambient backdrop',
      (tester) async {
    await _pumpRouter(tester, const AuthState());

    expect(
      find.ancestor(
        of: find.byType(AuthScreen),
        matching: find.byType(AppAmbientBackground),
      ),
      findsOneWidget,
    );
  });

  testWidgets('module switches dissolve the incoming shell over the old one',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _adminState);

    router.go('/sonarr/library');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 140));

    // Mid-dissolve: both module pages are mounted and the incoming page is
    // fading in (over its opaque backdrop) rather than double-exposing.
    expect(find.byType(DashboardShell), findsOneWidget);
    expect(find.byType(SonarrModuleShell), findsOneWidget);
    final fades = tester.widgetList<FadeTransition>(
      find.ancestor(
        of: find.byType(SonarrModuleShell),
        matching: find.byType(FadeTransition),
      ),
    );
    expect(
      fades.any((f) => f.opacity.value > 0 && f.opacity.value < 1),
      isTrue,
      reason: 'incoming module page should be mid-fade',
    );

    // Bounded pumps (not pumpAndSettle): the stubbed Sonarr library shows an
    // indeterminate spinner, which never settles. 140+200+100ms covers the
    // 280ms dissolve plus a frame for the outgoing route's removal.
    await tester.pump(const Duration(milliseconds: 200));
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.byType(DashboardShell), findsNothing);
    expect(find.byType(SonarrModuleShell), findsOneWidget);
  });
}

const _authedState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(ai: true),
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

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

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

Future<({ProviderContainer container, GoRouter router})> _pumpRouter(
  WidgetTester tester,
  AuthState authState, {
  Size? surfaceSize,
}) async {
  if (surfaceSize != null) {
    tester.view.physicalSize = surfaceSize;
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
  }

  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(authState)),
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

  void push(AuthState next) => state = AsyncData(next);
}

Dio _fakeDio() {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter();
  return dio;
}

class _JsonAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body = switch (options.path) {
      '/api/trakt/anticipated' => [],
      _ => {
          'page': 1,
          'results': [],
          'total_pages': 0,
          'total_results': 0,
        },
    };
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
