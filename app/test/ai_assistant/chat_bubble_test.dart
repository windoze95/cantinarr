import 'package:cantinarr/features/ai_assistant/data/ai_models.dart';
import 'package:cantinarr/features/ai_assistant/ui/chat_bubble.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

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
}
