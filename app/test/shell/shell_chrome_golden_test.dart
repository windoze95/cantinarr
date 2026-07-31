import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
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
import 'package:shared_preferences/shared_preferences.dart';

/// Goldens for the 899px-vs-900px shell chrome flip: phone chrome (hamburger
/// plus bottom nav) one pixel below the breakpoint, desktop chrome (persistent
/// sidebar, no bottom nav) at it. Same fakes as adaptive_layout_test.dart;
/// the scene is flat theme surfaces plus Ahem text, so it sits far below the
/// tolerant comparator threshold (see flutter_test_config.dart). Regenerate
/// with `flutter test --update-goldens` from `app/`.
void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

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
              ],
            ),
          ],
        ),
      ],
    );
  }

  testWidgets('shell chrome flips between 899 and 900 pixels', (tester) async {
    tester.view.physicalSize = const Size(899, 800);
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
        child: MaterialApp.router(
          routerConfig: buildRouter(),
          theme: AppTheme.dark,
          debugShowCheckedModeBanner: false,
        ),
      ),
    );
    await tester.pumpAndSettle();

    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/shell_chrome_phone_899.png'),
    );

    tester.view.physicalSize = const Size(900, 800);
    await tester.pumpAndSettle();

    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/shell_chrome_desktop_900.png'),
    );
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
