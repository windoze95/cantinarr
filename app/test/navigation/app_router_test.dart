import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
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
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_authedState)),
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

    router.go('/radarr/library');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
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
