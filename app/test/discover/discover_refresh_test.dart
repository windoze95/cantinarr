import 'dart:async';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/ui/discover_refresh.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('entry, Back, tab return, resume and library events refresh the visible screen', (tester) async {
    final events = StreamController<WsEvent>.broadcast();
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(_Auth.new),
      realtimeEventsProvider.overrideWithValue(events.stream),
    ]);
    addTearDown(container.dispose);
    addTearDown(events.close);
    await container.read(authProvider.future);
    var movies = 0;
    var tv = 0;
    late StatefulNavigationShell tabs;
    final router = GoRouter(initialLocation: '/movies', routes: [
      StatefulShellRoute.indexedStack(
        builder: (_, __, shell) { tabs = shell; return shell; },
        branches: [
          StatefulShellBranch(routes: [GoRoute(path: '/movies', builder: (_, __) =>
            DiscoverRefresh(path: '/movies', onRefresh: () async { movies++; },
              child: const Scaffold(body: Text('Movies'))))]),
          StatefulShellBranch(routes: [GoRoute(path: '/tv', builder: (_, __) =>
            DiscoverRefresh(path: '/tv', onRefresh: () async { tv++; },
              child: const Scaffold(body: Text('TV'))))]),
        ],
      ),
      GoRoute(path: '/detail', builder: (_, __) => const Scaffold(body: Text('Detail'))),
    ]);
    addTearDown(router.dispose);
    await tester.pumpWidget(UncontrolledProviderScope(container: container,
      child: MaterialApp.router(routerConfig: router)));
    await tester.pumpAndSettle();
    expect((movies, tv), (1, 0));
    router.push('/detail');
    await tester.pumpAndSettle();
    router.pop();
    await tester.pumpAndSettle();
    expect((movies, tv), (2, 0));
    tabs.goBranch(1);
    await tester.pumpAndSettle();
    expect((movies, tv), (2, 1));
    tabs.goBranch(0);
    await tester.pumpAndSettle();
    expect((movies, tv), (3, 1));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect((movies, tv), (4, 1));
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();
    expect((movies, tv), (5, 1));
    events.add(const WsEvent(type: 'request_status_changed', data: {}));
    events.add(const WsEvent(type: 'request_updated', data: {}));
    await tester.pump();
    await tester.pump(const Duration(seconds: 3));
    await tester.pumpAndSettle();
    expect((movies, tv), (6, 1));
    router.push('/detail');
    await tester.pumpAndSettle();
    events.add(const WsEvent(type: 'request_updated', data: {}));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump(const Duration(seconds: 4));
    expect((movies, tv), (6, 1));
  });
}

class _Auth extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
    connection: BackendConnection(serverUrl: 'http://localhost',
      accessToken: 'test', refreshToken: 'test'),
    user: UserProfile(id: 1, username: 'test', role: 'admin'),
  );
}
