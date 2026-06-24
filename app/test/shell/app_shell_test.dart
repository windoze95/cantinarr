import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_chat_service.dart';
import 'package:cantinarr/features/ai_assistant/logic/ai_chat_provider.dart';
import 'package:cantinarr/features/ai_assistant/ui/ai_chat_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/shell/ui/app_shell.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
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
          builder: (context, state, child) => AppShell(child: child),
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
          aiChatProvider.overrideWith((ref) => chatNotifier),
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
          builder: (context, state, child) => AppShell(child: child),
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

    expect(find.text('Dashboard'), findsOneWidget);
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
          builder: (context, state, child) => AppShell(child: child),
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
    final path = options.path;
    final Object body;
    if (path.endsWith('/movie') || path.endsWith('/series')) {
      body = [];
    } else if (path == '/api/admin/issues') {
      body = {'issues': []};
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
