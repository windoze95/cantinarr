import 'package:cantinarr/features/ai_assistant/data/ai_provider_models.dart';
import 'package:cantinarr/features/ai_assistant/ui/model_fallback_setting.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final provider in ['openai', 'anthropic', 'local_openai']) {
    testWidgets('$provider has no fallback without provider metadata',
        (tester) async {
      await tester.pumpWidget(MaterialApp(
        home: Scaffold(
          body: ModelFallbackSetting(
            provider: AiProviderOption(
                id: provider, label: provider, credentialKey: '', models: []),
            enabled: true,
            onChanged: (_) => fail('Unsupported fallback was offered'),
          ),
        ),
      ));
      expect(find.byType(SwitchListTile), findsNothing);
    });
  }

  testWidgets('dynamic fallback needs no manually chosen model',
      (tester) async {
    bool? changed;
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: ModelFallbackSetting(
          provider: const AiProviderOption(
            id: 'codex',
            label: 'OpenAI (OAuth)',
            credentialKey: '',
            models: [],
            modelFallback: AiModelRecommendation(
              model: '',
              source: 'OpenAI model catalog',
              description: 'OpenAI chooses the linked account’s default.',
            ),
          ),
          enabled: false,
          onChanged: (value) => changed = value,
        ),
      ),
    ));
    expect(find.textContaining('OpenAI model catalog.'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
    await tester.tap(find.byType(SwitchListTile));
    expect(changed, isTrue);
  });
}
