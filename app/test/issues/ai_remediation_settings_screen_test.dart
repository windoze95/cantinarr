import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/issues/ui/ai_remediation_settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('explains the server-owned shared AI billing boundary',
      (tester) async {
    final adapter = _RemediationSettingsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const AiRemediationSettingsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(
        find.textContaining('This server-owned agent always'), findsOneWidget);
    expect(find.textContaining('shared ChatGPT usage meter'), findsOneWidget);
    expect(
      find.textContaining(
        'Personal providers and per-user AI access switches are never used',
      ),
      findsOneWidget,
    );
    await _scrollUntilBuilt(tester, find.text('SHARED AI'));
    expect(
      find.textContaining('Always uses the provider and model currently'),
      findsOneWidget,
    );
    expect(find.textContaining('Provider override'), findsNothing);
    expect(find.textContaining('Model override'), findsNothing);
  });

  testWidgets('clears legacy provider and model fields when settings are saved',
      (tester) async {
    final adapter = _RemediationSettingsAdapter(
      provider: 'anthropic',
      model: 'claude-sonnet-4-6',
    );
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const AiRemediationSettingsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await _scrollUntilBuilt(tester, save);
    await tester.ensureVisible(save);
    await tester.drag(find.byType(ListView), const Offset(0, -100));
    await tester.pump();
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.saved?['provider'], '');
    expect(adapter.saved?['model'], '');
  });
}

Future<void> _scrollUntilBuilt(WidgetTester tester, Finder finder) async {
  for (var attempt = 0; attempt < 20; attempt++) {
    if (finder.evaluate().isNotEmpty) return;
    await tester.drag(find.byType(ListView), const Offset(0, -150));
    await tester.pump();
  }
  fail('Could not build $finder while scrolling');
}

class _RemediationSettingsAdapter implements HttpClientAdapter {
  _RemediationSettingsAdapter({this.provider = '', this.model = ''});

  final String provider;
  final String model;
  Map<String, dynamic>? saved;

  Map<String, dynamic> get _settings => {
        'enabled': true,
        'auto_dispatch': false,
        'allow_reporting': true,
        'mark_resolved_as_read': true,
        'mode': 'supervised',
        'provider': provider,
        'model': model,
        'max_steps': 12,
        'max_turn_tokens': 4096,
        'max_wall_clock_secs': 300,
        'daily_run_cap': 50,
        'circuit_breaker_giveups': 5,
        'max_user_wait_hours': 72,
        'observation_min_minutes': 10,
        'observation_quiet_minutes': 5,
        'observation_settle_minutes': 2,
      };

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final body = options.method == 'PUT'
        ? Map<String, dynamic>.from(options.data as Map)
        : _settings;
    if (options.method == 'PUT') saved = body;
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
