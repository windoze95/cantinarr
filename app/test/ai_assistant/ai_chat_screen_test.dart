import 'package:cantinarr/features/ai_assistant/ui/ai_chat_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('assistant screen exposes exit and composer controls',
      (tester) async {
    final router = GoRouter(
      initialLocation: '/assistant',
      routes: [
        GoRoute(
          path: '/dashboard/movies',
          builder: (_, __) => const Scaffold(body: Text('Dashboard')),
        ),
        GoRoute(
          path: '/assistant',
          builder: (_, __) => const AiChatScreen(aiAvailable: true),
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byTooltip('Exit assistant'), findsOneWidget);
    expect(find.byType(TextField), findsOneWidget);
    expect(find.text("What's trending?"), findsOneWidget);

    await tester.tap(find.byTooltip('Exit assistant'));
    await tester.pumpAndSettle();

    expect(find.text('Dashboard'), findsOneWidget);
  });

  testWidgets('assistant conversation persists after route close and reopen',
      (tester) async {
    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        GoRoute(
          path: '/dashboard/movies',
          builder: (context, __) => Scaffold(
            body: Center(
              child: ElevatedButton(
                onPressed: () => context.push('/assistant'),
                child: const Text('Open assistant'),
              ),
            ),
          ),
        ),
        GoRoute(
          path: '/assistant',
          builder: (_, __) => const AiChatScreen(aiAvailable: true),
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.text('Open assistant'));
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('New chat'));
    await tester.pumpAndSettle();
    expect(
        find.text('Chat cleared! What can I help you find?'), findsOneWidget);

    await tester.tap(find.byTooltip('Exit assistant'));
    await tester.pumpAndSettle();
    expect(find.text('Open assistant'), findsOneWidget);

    await tester.tap(find.text('Open assistant'));
    await tester.pumpAndSettle();

    expect(
        find.text('Chat cleared! What can I help you find?'), findsOneWidget);
  });
}
