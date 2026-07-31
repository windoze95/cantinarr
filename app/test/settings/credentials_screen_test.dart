import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
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
}

class _CredentialsAdapter implements HttpClientAdapter {
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
        'ai': {
          'config': {
            'provider': 'codex',
            'model': 'gpt-5.4',
          },
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
              'models': [
                {'id': 'gpt-4.1-mini', 'label': 'GPT-4.1 mini'},
              ],
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
