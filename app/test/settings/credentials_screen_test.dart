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
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CredentialsAdapter();

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

    expect(find.text('ChatGPT (Codex)'), findsOneWidget);
    expect(find.text('Shared account'), findsOneWidget);
    expect(
      find.text(
        'Connect one server ChatGPT account for users with included access.',
      ),
      findsOneWidget,
    );
    expect(find.textContaining('one ChatGPT account and one Codex meter'),
        findsOneWidget);
    expect(find.text('Key missing'), findsNothing);
  });
}

class _CredentialsAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
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
              'label': 'ChatGPT (Codex)',
              'auth_type': 'user_oauth',
              'credential_key': '',
              'models': [
                {'id': 'gpt-5.4', 'label': 'GPT-5.4'},
              ],
            },
          ],
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
