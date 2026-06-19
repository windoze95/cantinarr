import 'package:cantinarr/core/models/backend_connection.dart';
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
