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

  testWidgets('non-admin drawer hides instance app modules', (tester) async {
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
    expect(find.text('Radarr'), findsNothing);
    expect(find.text('Main Radarr'), findsNothing);
    expect(find.text('4K Radarr'), findsNothing);
    expect(find.byTooltip('Choose Radarr instance'), findsNothing);
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
  });

  testWidgets(
      'conditional attention entries and empty section hide after empty loads',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
    });

    await _pumpAdminDrawer(tester);

    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Issues'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('NEEDS ATTENTION'), findsNothing);
  });

  testWidgets('conditional attention entries fail open when queues are unknown',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
    });

    await _pumpAdminDrawer(tester, failAttentionQueues: true);

    expect(find.text('NEEDS ATTENTION'), findsOneWidget);
    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Issues'), findsOneWidget);
    expect(find.text('Agent fixes'), findsOneWidget);
  });

  testWidgets('tracking-only issue restores Issues without an attention badge',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
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
}) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter(
    issues: issues,
    failAttentionQueues: failAttentionQueues,
  );
  return dio;
}

class _JsonAdapter implements HttpClientAdapter {
  const _JsonAdapter({
    this.issues = const [],
    this.failAttentionQueues = false,
  });

  final List<Map<String, dynamic>> issues;
  final bool failAttentionQueues;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    if (failAttentionQueues &&
        (path == '/api/admin/requests' ||
            path == '/api/admin/issues' ||
            path == '/api/admin/agent-actions')) {
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
