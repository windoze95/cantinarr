import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_shell.dart';
import 'package:cantinarr/features/shell/ui/app_shell.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// The desktop layout flips at 900px: persistent sidebar with the active
/// module's pages instead of a hamburger drawer, and no module bottom nav.
/// Narrower layouts must keep the mobile chrome untouched.
void main() {
  GoRouter buildRouter() {
    return GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            StatefulShellRoute.indexedStack(
              builder: (context, state, navigationShell) => DashboardShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: navigationShell.goBranch,
                child: navigationShell,
              ),
              branches: [
                StatefulShellBranch(routes: [
                  GoRoute(
                    path: '/dashboard/movies',
                    builder: (_, __) =>
                        const Scaffold(body: Text('Movies tab')),
                  ),
                ]),
                StatefulShellBranch(routes: [
                  GoRoute(
                    path: '/dashboard/tv',
                    builder: (_, __) => const Scaffold(body: Text('TV tab')),
                  ),
                ]),
                StatefulShellBranch(routes: [
                  GoRoute(
                    path: '/dashboard/releases',
                    builder: (_, __) =>
                        const Scaffold(body: Text('Releases tab')),
                  ),
                ]),
              ],
            ),
          ],
        ),
      ],
    );
  }

  Future<void> pumpShell(WidgetTester tester, {required Size size}) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_userState)),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: buildRouter()),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('desktop shows sidebar with module pages and no bottom nav',
      (tester) async {
    await pumpShell(tester, size: const Size(1400, 900));

    // Persistent sidebar: module list visible without opening any drawer.
    expect(find.text('Dashboard'), findsOneWidget);
    expect(find.byIcon(Icons.menu), findsNothing);

    // Active module expands into its pages.
    expect(find.text('Movies'), findsOneWidget);
    expect(find.text('TV Shows'), findsOneWidget);
    expect(find.text('Releases'), findsOneWidget);
    // No Chaptarr grant -> no Books page.
    expect(find.text('Books'), findsNothing);

    // The mobile bottom nav is gone on desktop.
    expect(find.byType(BottomNavigationBar), findsNothing);

    // Sidebar page items navigate between branches.
    await tester.tap(find.text('TV Shows'));
    await tester.pumpAndSettle();
    expect(find.text('TV tab'), findsOneWidget);
  });

  testWidgets('phone keeps hamburger drawer and bottom nav', (tester) async {
    await pumpShell(tester, size: const Size(390, 844));

    expect(find.byIcon(Icons.menu), findsOneWidget);
    expect(find.byType(BottomNavigationBar), findsOneWidget);

    // Bottom nav still switches tabs.
    await tester.tap(find.text('Releases'));
    await tester.pumpAndSettle();
    expect(find.text('Releases tab'), findsOneWidget);

    // Drawer lists modules but not per-module pages (bottom nav covers them):
    // 'Movies' exists only as a bottom-nav label, not a drawer entry.
    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();
    expect(find.text('Dashboard'), findsOneWidget);
  });
}

const _userState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
  ),
  user: UserProfile(id: 1, username: 'viewer', role: 'user'),
);

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
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
    return ResponseBody.fromString(
      jsonEncode([]),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
