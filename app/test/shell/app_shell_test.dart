import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_chat_service.dart';
import 'package:cantinarr/features/ai_assistant/data/codex_oauth_service.dart';
import 'package:cantinarr/features/ai_assistant/logic/ai_chat_provider.dart';
import 'package:cantinarr/features/ai_assistant/ui/ai_chat_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/shell/ui/app_shell.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets(
      'programmatic form scrolling keeps focus while a user drag dismisses it',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final focusNode = FocusNode();
    final scrollController = ScrollController();
    addTearDown(focusNode.dispose);
    addTearDown(scrollController.dispose);

    final router = GoRouter(
      initialLocation: '/settings/form',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/settings/form',
              builder: (_, __) => Scaffold(
                body: ListView(
                  controller: scrollController,
                  children: [
                    TextField(focusNode: focusNode),
                    const SizedBox(height: 1200),
                  ],
                ),
              ),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    focusNode.requestFocus();
    await tester.pump();
    expect(focusNode.hasFocus, isTrue);

    scrollController.animateTo(
      100,
      duration: const Duration(milliseconds: 100),
      curve: Curves.linear,
    );
    await tester.pumpAndSettle();
    expect(
      focusNode.hasFocus,
      isTrue,
      reason: 'automatic field reveal must not dismiss the keyboard',
    );

    await tester.drag(find.byType(ListView), const Offset(0, -100));
    await tester.pumpAndSettle();
    expect(focusNode.hasFocus, isFalse);
  });

  testWidgets('side-scrolling a shelf leaves the search bar alone',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    const pageScroll = ValueKey('page-scroll');
    const posterRow = ValueKey('poster-row');

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => Scaffold(
                body: ListView(
                  key: pageScroll,
                  children: [
                    const SizedBox(height: 400),
                    SizedBox(
                      height: 200,
                      child: ListView.builder(
                        key: posterRow,
                        scrollDirection: Axis.horizontal,
                        itemCount: 20,
                        itemBuilder: (_, __) => const SizedBox(width: 120),
                      ),
                    ),
                    const SizedBox(height: 1600),
                  ],
                ),
              ),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    final topBar = find.byKey(const ValueKey('module-top-bar'));
    final expanded = tester.getSize(topBar).height;
    expect(expanded, greaterThan(0));

    await tester.drag(find.byKey(posterRow), const Offset(-200, 0));
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      expanded,
      reason: 'swiping a shelf forward must not hide the search bar',
    );

    // The page's own vertical scroll still owns the chrome.
    await tester.drag(find.byKey(pageScroll), const Offset(0, -200));
    await tester.pumpAndSettle();
    expect(tester.getSize(topBar).height, lessThan(expanded));

    await tester.drag(find.byKey(posterRow), const Offset(200, 0));
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      lessThan(expanded),
      reason: 'a shelf back at its start must not pop the search bar open',
    );
  });

  testWidgets('scrolling a pushed route leaves the module search bar alone',
      (tester) async {
    final router = await _pumpScrollShell(tester);
    final topBar = find.byKey(const ValueKey('module-top-bar'));
    final expanded = tester.getSize(topBar).height;
    expect(expanded, greaterThan(0));

    router.push('/movie/1');
    await tester.pumpAndSettle();
    await tester.drag(find.byKey(const ValueKey('detail-scroll')),
        const Offset(0, -300));
    await tester.pumpAndSettle();

    router.pop();
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      expanded,
      reason: 'the dashboard is still at the top, so its search bar is too',
    );
  });

  testWidgets('opening another module page brings the search bar back',
      (tester) async {
    final router = await _pumpScrollShell(tester);
    final topBar = find.byKey(const ValueKey('module-top-bar'));
    final expanded = tester.getSize(topBar).height;

    await tester.drag(
        find.byKey(const ValueKey('dash-scroll')), const Offset(0, -300));
    await tester.pumpAndSettle();
    expect(tester.getSize(topBar).height, lessThan(expanded));

    router.go('/radarr/library');
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      expanded,
      reason: 'a fresh module page starts at the top with its search bar shown',
    );
  });

  testWidgets(
      'search-bar assistant submit opens route over previous shell content',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final chatNotifier = _FakeAiChatNotifier();
    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
        GoRoute(
          path: '/assistant',
          builder: (_, __) => const AiChatScreen(aiAvailable: true),
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          aiChatProvider.overrideWith((ref) {
            ref.keepAlive();
            return chatNotifier;
          }),
          codexConnectionStatusProvider.overrideWith(
            (_) => const CodexConnectionStatus(
              selected: false,
              available: false,
              connected: false,
            ),
          ),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Dashboard home'), findsOneWidget);

    await tester.enterText(
      find.byType(TextField).first,
      'What should I watch tonight?',
    );
    await tester.pump();
    await tester.tap(find.byIcon(Icons.send_rounded));
    await tester.pumpAndSettle();

    expect(find.byType(AiChatScreen), findsOneWidget);
    expect(find.byTooltip('Exit assistant'), findsOneWidget);
    expect(chatNotifier.sentMessage, 'What should I watch tonight?');

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();

    expect(find.text('Dashboard home'), findsOneWidget);
    expect(find.byType(AiChatScreen), findsNothing);
    expect(find.text('What should I watch tonight?'), findsNothing);
  });

  testWidgets(
      'Ask AI pill surfaces on typed pause and never lingers on an empty field',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    // Hidden while unfocused, on focus, and even after a long focused
    // pause: only typing arms the pill.
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    await tester.tap(find.byType(TextField).first);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // Typing, then stopping, surfaces it — never while keys are coming.
    await tester.enterText(find.byType(TextField).first, 'Severance');
    await tester.pump();
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsOneWidget);

    // The clear X wipes the query in place (no onChanged fires): the pill
    // must vanish with it, not linger over the emptied field.
    await tester.tap(find.byIcon(Icons.close_rounded));
    await tester.pump();
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // And an empty field never re-offers it, no matter how long the wait.
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // Fresh typing re-arms the pause detector.
    await tester.enterText(find.byType(TextField).first, 'Severance');
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsOneWidget);

    // The shimmer repeats while aiReady, so bounded pumps only from here.
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    // AI mode engaged keeping the typed text, and the field kept focus.
    expect(find.text('Press send to ask AI'), findsOneWidget);
    final field = tester.widget<TextField>(find.byType(TextField).first);
    expect(field.focusNode!.hasFocus, isTrue);
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // A title-like edit doesn't knock the explicit choice back to search.
    await tester.enterText(find.byType(TextField).first, 'Sev');
    await tester.pump();
    expect(find.text('Press send to ask AI'), findsOneWidget);

    // Deleting to zero characters flicks AI mode back off — and the
    // emptied field stays pill-free until the user types again.
    await tester.enterText(find.byType(TextField).first, '');
    await tester.pump();
    expect(find.text('Press send to ask AI'), findsNothing);
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);
    await tester.pumpAndSettle();
  });

  testWidgets('non-admin drawer hides instance app modules', (tester) async {
    final semantics = tester.ensureSemantics();
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_multiRadarrState(isAdmin: false)),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();

    expect(find.text('Discover'), findsOneWidget);
    expect(find.bySemanticsIdentifier('nav-module-dashboard'), findsOneWidget);
    expect(find.bySemanticsIdentifier('nav-action-settings'), findsOneWidget);
    expect(find.text('Radarr'), findsNothing);
    expect(find.bySemanticsIdentifier('nav-module-radarr'), findsNothing);
    expect(find.text('Main Radarr'), findsNothing);
    expect(find.text('4K Radarr'), findsNothing);
    expect(find.byTooltip('Choose Radarr instance'), findsNothing);
    semantics.dispose();
  });

  testWidgets('admin drawer selector switches the active app instance',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_multiRadarrState(isAdmin: true)),
        ),
        backendClientProvider.overrideWithValue(_fakeDio()),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
    );
    addTearDown(container.dispose);

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
            GoRoute(
              path: '/radarr/library',
              builder: (_, __) => const Scaffold(body: Text('Radarr library')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();

    expect(find.text('Radarr'), findsOneWidget);
    expect(find.text('Main Radarr'), findsOneWidget);
    expect(find.text('4K Radarr'), findsNothing);

    await tester.tap(find.byTooltip('Choose Radarr instance'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('4K Radarr'));
    await tester.pumpAndSettle();

    expect(
      container.read(instanceProvider).activeRadarrInstanceId,
      'radarr-4k',
    );
    expect(find.text('Radarr library'), findsOneWidget);
  });

  testWidgets('admin drawer keeps all attention entries visible by default',
      (tester) async {
    await _pumpAdminDrawer(tester);

    expect(find.text('NEEDS ATTENTION'), findsOneWidget);
    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Issues'), findsOneWidget);
    expect(find.text('Agent fixes'), findsOneWidget);
    expect(find.text('Profile approvals'), findsOneWidget);
  });

  testWidgets(
      'conditional attention entries and empty section hide after empty loads',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    await _pumpAdminDrawer(tester);

    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Issues'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);
    expect(find.text('NEEDS ATTENTION'), findsNothing);
  });

  testWidgets('conditional attention entries fail open when queues are unknown',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    await _pumpAdminDrawer(tester, failAttentionQueues: true);

    expect(find.text('NEEDS ATTENTION'), findsOneWidget);
    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Issues'), findsOneWidget);
    expect(find.text('Agent fixes'), findsOneWidget);
    expect(find.text('Profile approvals'), findsOneWidget);
  });

  testWidgets(
      'conditional attention entries stay hidden while queues first load',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    // An in-flight first load must not flash the entries fail-open: the
    // cold-start assumption is empty-until-proven, and only a FAILED
    // refresh makes a queue unknowable enough to show them.
    await _pumpAdminDrawer(tester, hangAttentionQueues: true);

    expect(find.text('NEEDS ATTENTION'), findsNothing);
    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Issues'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);
  });

  testWidgets('tracking-only issue restores Issues without an attention badge',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    await _pumpAdminDrawer(
      tester,
      issues: const [
        {
          'id': 1,
          'status': 'observing',
          'media_type': 'movie',
          'tmdb_id': 1,
          'title': 'Tracked movie',
        },
      ],
    );

    expect(find.text('NEEDS ATTENTION'), findsOneWidget);
    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);
    expect(find.text('Issues'), findsOneWidget);

    final issuesTile = tester.widget<ListTile>(
      find.ancestor(
        of: find.text('Issues'),
        matching: find.byType(ListTile),
      ),
    );
    expect(issuesTile.trailing, isNull);
  });
}

Future<void> _pumpAdminDrawer(
  WidgetTester tester, {
  List<Map<String, dynamic>> issues = const [],
  bool failAttentionQueues = false,
  bool hangAttentionQueues = false,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final router = GoRouter(
    initialLocation: '/dashboard/movies',
    routes: [
      ShellRoute(
        builder: (context, state, child) =>
            AppShell(currentPath: state.uri.path, child: child),
        routes: [
          GoRoute(
            path: '/dashboard/movies',
            builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
          ),
        ],
      ),
    ],
  );

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_authenticatedAiState),
        ),
        backendClientProvider.overrideWithValue(_fakeDio(
          issues: issues,
          failAttentionQueues: failAttentionQueues,
          hangAttentionQueues: hangAttentionQueues,
        )),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();

  await tester.tap(find.byIcon(Icons.menu));
  await tester.pumpAndSettle();
}

/// Shell over long scrollable pages: two module routes and one pushed route.
Future<GoRouter> _pumpScrollShell(WidgetTester tester) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  Widget page(String key) => Scaffold(
        body: ListView(
          key: ValueKey(key),
          children: const [SizedBox(height: 3000)],
        ),
      );

  final router = GoRouter(
    initialLocation: '/dashboard/movies',
    routes: [
      ShellRoute(
        builder: (context, state, child) =>
            AppShell(currentPath: state.uri.path, child: child),
        routes: [
          GoRoute(
            path: '/dashboard/movies',
            builder: (_, __) => page('dash-scroll'),
          ),
          GoRoute(
            path: '/radarr/library',
            builder: (_, __) => page('radarr-scroll'),
          ),
          GoRoute(path: '/movie/1', builder: (_, __) => page('detail-scroll')),
        ],
      ),
    ],
  );

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_authenticatedAiState),
        ),
        backendClientProvider.overrideWithValue(_fakeDio()),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return router;
}

const _authenticatedAiState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(ai: true),
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'admin'),
);

AuthState _multiRadarrState({required bool isAdmin}) {
  return AuthState(
    connection: const BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(
          id: 'radarr-main',
          serviceType: 'radarr',
          name: 'Main Radarr',
          isDefault: true,
        ),
        ServiceInstance(
          id: 'radarr-4k',
          serviceType: 'radarr',
          name: '4K Radarr',
        ),
      ],
    ),
    user: UserProfile(
      id: 1,
      username: isAdmin ? 'admin' : 'viewer',
      role: isAdmin ? 'admin' : 'user',
    ),
  );
}

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

class _FakeAiChatNotifier extends AiChatNotifier {
  String? sentMessage;

  _FakeAiChatNotifier() : super(chatService: AiChatService(backendDio: Dio()));

  @override
  Future<void> sendMessage(String text) async {
    sentMessage = text;
  }
}

Dio _fakeDio({
  List<Map<String, dynamic>> issues = const [],
  bool failAttentionQueues = false,
  bool hangAttentionQueues = false,
}) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter(
    issues: issues,
    failAttentionQueues: failAttentionQueues,
    hangAttentionQueues: hangAttentionQueues,
  );
  return dio;
}

class _JsonAdapter implements HttpClientAdapter {
  const _JsonAdapter({
    this.issues = const [],
    this.failAttentionQueues = false,
    this.hangAttentionQueues = false,
  });

  final List<Map<String, dynamic>> issues;
  final bool failAttentionQueues;
  final bool hangAttentionQueues;

  static bool _isAttentionQueue(String path) =>
      path == '/api/admin/requests' ||
      path == '/api/admin/issues' ||
      path == '/api/admin/agent-actions' ||
      path == '/api/admin/profile-change-proposals';

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    if (hangAttentionQueues && _isAttentionQueue(path)) {
      // A first load still in flight: the response simply never arrives.
      return Completer<ResponseBody>().future;
    }
    if (failAttentionQueues && _isAttentionQueue(path)) {
      return ResponseBody.fromString(
        '{"error":"temporarily unavailable"}',
        503,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    final Object body;
    if (path.endsWith('/movie') || path.endsWith('/series')) {
      body = [];
    } else if (path == '/api/admin/issues') {
      body = {'issues': issues};
    } else if (path == '/api/admin/agent-actions') {
      body = {'actions': []};
    } else if (path == '/api/admin/profile-change-proposals') {
      body = {'proposals': []};
    } else {
      body = [];
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
