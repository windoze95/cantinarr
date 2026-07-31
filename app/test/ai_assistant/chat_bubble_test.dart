import 'package:cantinarr/features/ai_assistant/data/ai_models.dart';
import 'package:cantinarr/features/ai_assistant/ui/chat_bubble.dart';
import 'package:cantinarr/features/config_changes/data/config_change_models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

import 'package:cantinarr/core/widgets/cached_image.dart';

void main() {
  testWidgets('shows media carousel while assistant message is streaming',
      (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ChatBubble(
            message: ChatMessage(
              id: 'assistant-1',
              role: ChatRole.assistant,
              content: 'There are 2 main Minions movies:',
              timestamp: DateTime(2026),
              isStreaming: true,
              mediaResults: const [
                MediaResultItem(
                  id: 211672,
                  title: 'Minions',
                  year: '2015',
                  mediaType: 'movie',
                ),
              ],
            ),
          ),
        ),
      ),
    );

    expect(find.text('There are 2 main Minions movies:'), findsOneWidget);
    expect(find.text('Minions (2015)'), findsOneWidget);
  });

  testWidgets('shows media placeholders while display media is loading',
      (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ChatBubble(
            message: ChatMessage(
              id: 'assistant-1',
              role: ChatRole.assistant,
              content: 'There are 6 movies in that franchise:',
              timestamp: DateTime(2026),
              isStreaming: true,
              toolActivity: const [
                ToolActivity(
                  name: 'display_media',
                  label: 'Preparing results',
                ),
              ],
            ),
          ),
        ),
      ),
    );

    expect(find.text('There are 6 movies in that franchise:'), findsOneWidget);
    expect(find.byType(ListView), findsOneWidget);
    expect(find.byType(AspectRatio), findsNWidgets(4));
  });

  testWidgets('only a typed receipt creates configuration actions',
      (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          home: Scaffold(
            body: Column(
              children: [
                ChatBubble(
                  message: ChatMessage(
                    id: 'assistant-text',
                    role: ChatRole.assistant,
                    content:
                        'Review change and Restore previous settings are just text.',
                    timestamp: DateTime(2026),
                  ),
                ),
                ChatBubble(
                  message: ChatMessage(
                    id: 'assistant-receipt',
                    role: ChatRole.assistant,
                    content: 'Done.',
                    timestamp: DateTime(2026),
                    configurationChanges: [_change()],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );

    expect(find.widgetWithText(OutlinedButton, 'Review change'), findsOneWidget);
    expect(
      find.widgetWithText(TextButton, 'Restore previous settings'),
      findsOneWidget,
    );
  });

  testWidgets('custom-format receipts do not advertise unsupported restore',
      (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          home: Scaffold(
            body: ChatBubble(
              message: ChatMessage(
                id: 'assistant-receipt',
                role: ChatRole.assistant,
                content: 'Done.',
                timestamp: DateTime(2026),
                configurationChanges: [
                  _change(resourceType: 'custom_format'),
                ],
              ),
            ),
          ),
        ),
      ),
    );

    expect(find.widgetWithText(OutlinedButton, 'Review change'), findsOneWidget);
    expect(
      find.widgetWithText(TextButton, 'Restore previous settings'),
      findsNothing,
    );
  });

  testWidgets('an applied restore record never offers another restore action',
      (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          home: Scaffold(
            body: ChatBubble(
              message: ChatMessage(
                id: 'assistant-receipt',
                role: ChatRole.assistant,
                content: 'Restored.',
                timestamp: DateTime(2026),
                configurationChanges: [
                  _change(operation: 'revert', canRevert: true),
                ],
              ),
            ),
          ),
        ),
      ),
    );

    expect(
      find.widgetWithText(TextButton, 'Restore previous settings'),
      findsNothing,
    );
  });

  testWidgets('book chat cards use the external cover and book detail route',
      (tester) async {
    String? pushedLocation;
    final router = GoRouter(
      routes: [
        GoRoute(
          path: '/',
          builder: (_, __) => Scaffold(
            body: ChatBubble(
              message: ChatMessage(
                id: 'assistant-book',
                role: ChatRole.assistant,
                content: 'Try this one:',
                timestamp: DateTime(2026),
                mediaResults: const [
                  MediaResultItem(
                    id: 0,
                    title: 'Example Book',
                    year: '2020',
                    mediaType: 'book',
                    foreignId: 'edition/OL123',
                    posterUrl: 'https://covers.example.org/1.jpg',
                  ),
                ],
              ),
            ),
          ),
        ),
        GoRoute(
          path: '/detail/:type/:id',
          builder: (_, state) {
            pushedLocation = state.uri.toString();
            return const Scaffold(body: SizedBox());
          },
        ),
      ],
    );
    await tester.pumpWidget(MaterialApp.router(routerConfig: router));
    await tester.pumpAndSettle();

    // The card loads the server-vetted external cover verbatim — never a
    // TMDB URL built from a foreign id.
    final image = tester.widget<CachedImage>(find.byType(CachedImage));
    expect(image.url, 'https://covers.example.org/1.jpg');

    await tester.tap(find.text('Example Book (2020)'));
    await tester.pumpAndSettle();
    expect(pushedLocation, isNotNull);
    expect(pushedLocation, contains('/detail/book/edition%2FOL123'));
    expect(Uri.parse(pushedLocation!).queryParameters['title'], 'Example Book');
  });

  testWidgets('book chat cards without a cover fall back to the book icon',
      (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ChatBubble(
            message: ChatMessage(
              id: 'assistant-book-2',
              role: ChatRole.assistant,
              content: 'Another:',
              timestamp: DateTime(2026),
              mediaResults: const [
                MediaResultItem(
                  id: 0,
                  title: 'Coverless Book',
                  mediaType: 'book',
                  foreignId: 'fb-2',
                ),
              ],
            ),
          ),
        ),
      ),
    );
    final image = tester.widget<CachedImage>(find.byType(CachedImage));
    expect(image.url, isNull);
    expect(find.byIcon(Icons.menu_book), findsOneWidget);
  });
}

ConfigChange _change({
  String resourceType = 'quality_profile',
  String operation = 'update',
  bool? canRevert,
}) =>
    ConfigChange.fromJson({
      'id': 42,
      'actor_user_id': 1,
      'actor_name': 'Alex',
      'source': 'ai_chat',
      'service_type': 'sonarr',
      'instance_id': 'sonarr-main',
      'instance_name': 'Main Sonarr',
      'resource_type': resourceType,
      'resource_id': '7',
      'resource_name': 'Very High (4K)',
      'operation': operation,
      'status': 'applied',
      'summary': 'Prefer English releases',
      'changes': const [],
      'created_at': '2026-07-20T21:57:00Z',
      if (canRevert != null) 'can_revert': canRevert,
    });