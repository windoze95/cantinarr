import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/settings/settings_anchors.dart';
import 'package:cantinarr/features/settings/ui/credentials_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('Codex provider is described as shared included access',
      (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('OpenAI (OAuth)'), findsOneWidget);
    expect(find.text('Shared account'), findsOneWidget);
    expect(
      find.text(
        'Connect one server OpenAI OAuth account for users with included access.',
      ),
      findsOneWidget,
    );
    expect(find.textContaining('one ChatGPT account and one Codex meter'),
        findsOneWidget);
    expect(find.text('Key missing'), findsNothing);
    expect(find.text('Daily shared-model test'), findsOneWidget);

    final healthToggle = find.byKey(const ValueKey('ai-health-check-toggle'));
    await tester.ensureVisible(healthToggle);
    await tester.tap(healthToggle);
    await tester.drag(
      find.byType(Scrollable).first,
      const Offset(0, -1200),
    );
    await tester.pumpAndSettle();
    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate?['ai_health_check_enabled'], 'false');
  });

  testWidgets('saves an OpenAI key with its selected shared model',
      (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('ai-provider-codex')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('OpenAI').last);
    await tester.pumpAndSettle();

    await tester.drag(
      find.byType(Scrollable).first,
      const Offset(0, -900),
    );
    await tester.pumpAndSettle();
    final openAIKey = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'OpenAI API key',
    );
    await tester.ensureVisible(openAIKey);
    await tester.enterText(openAIKey, 'synthetic-shared-key');

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, {
      'openai_key': 'synthetic-shared-key',
      'ai_provider': 'openai',
      'ai_model': 'gpt-4.1-mini',
    });
  });

  testWidgets('Grok OAuth provider shows the shared xAI account panel',
      (tester) async {
    final adapter = _CredentialsAdapter(provider: 'grok_oauth');
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('xAI Grok (OAuth)'), findsOneWidget);
    expect(find.text('Shared account'), findsOneWidget);
    expect(
      find.text(
        'Connect one server xAI Grok account for users with included access.',
      ),
      findsOneWidget,
    );
    expect(find.text('Shared xAI Grok allowance'), findsOneWidget);
    expect(find.text('Connect shared xAI Grok'), findsOneWidget);
  });

  testWidgets('saves an xAI API key under grok_key', (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.drag(
      find.byType(Scrollable).first,
      const Offset(0, -1400),
    );
    await tester.pumpAndSettle();
    final grokKey = find.byWidgetPredicate(
      (widget) =>
          widget is TextField && widget.decoration?.hintText == 'xAI API key',
    );
    await tester.ensureVisible(grokKey);
    await tester.enterText(grokKey, 'synthetic-grok-key');

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate?['grok_key'], 'synthetic-grok-key');
  });

  testWidgets(
      'a highlight deep link scrolls to the Gemini section on load',
      (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(
            highlightId: SettingsAnchors.credentialsGemini,
          ),
        ),
      ),
    );
    // The body mounts only after the async status load; the anchor's trigger
    // fires then, so settling covers load, scroll, and the highlight fade.
    await tester.pumpAndSettle();

    expect(
      tester
          .state<ScrollableState>(find.byType(Scrollable).first)
          .position
          .pixels,
      greaterThan(0),
    );
    expect(find.text('Google Gemini (AI)'), findsOneWidget);
  });







  testWidgets(
      'hides the reasoning effort control when the server does not advertise it',
      (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
    );
    await _pumpCredentials(tester, adapter);

    expect(
        find.byKey(const ValueKey('openai-reasoning-effort')), findsNothing);
  });

  testWidgets('prefills the reasoning effort from the credentials status',
      (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsReasoningEffort: true,
      openAiReasoningEffort: 'low',
    );
    await _pumpCredentials(tester, adapter);

    expect(
        find.byKey(const ValueKey('openai-reasoning-effort')), findsOneWidget);
    expect(find.text('OpenAI reasoning effort'), findsOneWidget);
    expect(find.text('Low'), findsOneWidget);
  });

  testWidgets('saves only a changed reasoning effort', (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsReasoningEffort: true,
      openAiReasoningEffort: '',
    );
    await _pumpCredentials(tester, adapter);

    final control = find.byKey(const ValueKey('openai-reasoning-effort'));
    await tester.ensureVisible(control);
    await tester.tap(control);
    await tester.pumpAndSettle();
    await tester.tap(find.text('None').last);
    await tester.pumpAndSettle();

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, {'openai_reasoning_effort': 'none'});
    expect(find.text('AI test passed. Settings saved.'), findsOneWidget);
  });

  testWidgets('switching back to Auto saves an empty effort', (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsReasoningEffort: true,
      openAiReasoningEffort: 'medium',
    );
    await _pumpCredentials(tester, adapter);

    final control = find.byKey(const ValueKey('openai-reasoning-effort'));
    await tester.ensureVisible(control);
    await tester.tap(control);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Auto').last);
    await tester.pumpAndSettle();

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, {'openai_reasoning_effort': ''});
  });

  testWidgets('an untouched reasoning effort is not a change', (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsReasoningEffort: true,
      openAiReasoningEffort: 'low',
    );
    await _pumpCredentials(tester, adapter);

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, isNull);
    expect(find.text('No changes to save'), findsOneWidget);
  });

  testWidgets('selecting the local provider shows its endpoint controls',
      (tester) async {
    final adapter = _CredentialsAdapter(includeLocalProvider: true);
    await _pumpCredentials(tester, adapter);

    await tester.tap(find.byKey(const ValueKey('ai-provider-codex')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Local (OpenAI-compatible)').last);
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('local-openai-base-url')), findsOneWidget);
    expect(find.byKey(const ValueKey('local-openai-reasoning-effort')),
        findsOneWidget);
    // No catalog: the model picker collapses to the custom-model field.
    expect(find.text('Custom model ID'), findsWidgets);
    // The self-hosted notice replaces the paid-quota warning. The screen
    // builds lazily, so bring it into view before asserting.
    final notice = find.textContaining('Costs depend on that server');
    await tester.scrollUntilVisible(notice, 200,
        scrollable: find.byType(Scrollable).first);
    expect(notice, findsOneWidget);
  });

  testWidgets('prefills the local endpoint pair from the credentials status',
      (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'local_openai',
      model: 'qwen3.6:35b-a3b',
      includeLocalProvider: true,
      localBaseUrl: 'http://llm-host:11434/v1',
      localReasoningEffort: 'none',
    );
    await _pumpCredentials(tester, adapter);

    final url = find.byKey(const ValueKey('local-openai-base-url'));
    expect(url, findsOneWidget);
    expect(
      tester.widget<TextField>(url).controller?.text,
      'http://llm-host:11434/v1',
    );
    expect(find.text('None'), findsOneWidget);
    // The stored custom model prefills the custom-model field.
    expect(find.text('qwen3.6:35b-a3b'), findsOneWidget);
  });

  testWidgets('local provider save requires the base URL', (tester) async {
    final adapter = _CredentialsAdapter(includeLocalProvider: true);
    await _pumpCredentials(tester, adapter);

    await tester.tap(find.byKey(const ValueKey('ai-provider-codex')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Local (OpenAI-compatible)').last);
    await tester.pumpAndSettle();

    await tester.enterText(
        find.widgetWithText(TextField, 'Provider model ID'),
        'qwen3.6:35b-a3b');

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, isNull);
    expect(find.text('Enter the server base URL'), findsOneWidget);
  });

  testWidgets('local provider save sends its scoped keys', (tester) async {
    final adapter = _CredentialsAdapter(includeLocalProvider: true);
    await _pumpCredentials(tester, adapter);

    await tester.tap(find.byKey(const ValueKey('ai-provider-codex')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Local (OpenAI-compatible)').last);
    await tester.pumpAndSettle();

    await tester.enterText(
        find.widgetWithText(TextField, 'Provider model ID'), 'qwen3.6:35b-a3b');
    final url = find.byKey(const ValueKey('local-openai-base-url'));
    await tester.ensureVisible(url);
    await tester.enterText(url, ' http://llm-host:11434/v1 ');
    final effort = find.byKey(const ValueKey('local-openai-reasoning-effort'));
    await tester.ensureVisible(effort);
    await tester.tap(effort);
    await tester.pumpAndSettle();
    await tester.tap(find.text('None').last);
    await tester.pumpAndSettle();

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, {
      'ai_provider': 'local_openai',
      'ai_model': 'qwen3.6:35b-a3b',
      'local_openai_base_url': 'http://llm-host:11434/v1',
      'local_openai_reasoning_effort': 'none',
    });
  });
}

Future<void> _pumpCredentials(
  WidgetTester tester,
  _CredentialsAdapter adapter,
) async {
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [backendClientProvider.overrideWithValue(dio)],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: const CredentialsScreen(),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _CredentialsAdapter implements HttpClientAdapter {
  _CredentialsAdapter({
    this.provider = 'codex',
    this.model,
    this.openAiSupportsReasoningEffort = false,
    this.openAiReasoningEffort,
    this.includeLocalProvider = false,
    this.localBaseUrl,
    this.localReasoningEffort,
  });

  final String provider;
  final String? model;
  final bool openAiSupportsReasoningEffort;
  final String? openAiReasoningEffort;
  final bool includeLocalProvider;
  final String? localBaseUrl;
  final String? localReasoningEffort;
  Map<String, dynamic>? lastUpdate;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'PUT' && requestStream != null) {
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      lastUpdate = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      return ResponseBody.fromString(
        jsonEncode({'status': 'ok'}),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    }
    return ResponseBody.fromString(
      jsonEncode({
        'credentials': const <String, bool>{},
        'tmdb_using_builtin': false,
        'ai': {
          'config': {
            'provider': provider,
            'model': model ?? (provider == 'grok_oauth' ? 'grok-4.6' : 'gpt-5.4'),
          },
          if (openAiReasoningEffort != null)
            'openai_reasoning_effort': openAiReasoningEffort,
          if (localBaseUrl != null) 'local_openai_base_url': localBaseUrl,
          if (localReasoningEffort != null)
            'local_openai_reasoning_effort': localReasoningEffort,
          'providers': [
            {
              'id': 'codex',
              'label': 'OpenAI (OAuth)',
              'auth_type': 'user_oauth',
              'credential_key': '',
              'models': [
                {'id': 'gpt-5.4', 'label': 'GPT-5.4'},
              ],
            },
            {
              'id': 'openai',
              'label': 'OpenAI',
              'auth_type': 'api_key',
              'credential_key': 'openai_key',
              if (openAiSupportsReasoningEffort)
                'supports_reasoning_effort': true,
              'models': [
                {'id': 'gpt-4.1-mini', 'label': 'GPT-4.1 mini'},
              ],
            },
            {
              'id': 'grok',
              'label': 'xAI Grok',
              'auth_type': 'api_key',
              'credential_key': 'grok_key',
              'models': [
                {'id': 'grok-4.6', 'label': 'Grok 4.6'},
              ],
            },
            {
              'id': 'grok_oauth',
              'label': 'xAI Grok (OAuth)',
              'auth_type': 'user_oauth',
              'credential_key': '',
              'models': [
                {'id': 'grok-4.6', 'label': 'Grok 4.6'},
              ],
            },
            if (includeLocalProvider)
              {
                'id': 'local_openai',
                'label': 'Local (OpenAI-compatible)',
                'auth_type': 'api_key',
                'credential_key': 'local_openai_key',
                'supports_base_url': true,
                'supports_reasoning_effort': true,
                'shared_only': true,
                'models': <Map<String, dynamic>>[],
              },
          ],
          'health_check': {
            'enabled': true,
            'interval_hours': 24,
            'last_checked_at': null,
          },
        },
      }),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
