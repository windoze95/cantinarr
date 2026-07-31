import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses personal, included, and effective settings independently', () {
    final settings = AiSettings.fromJson(_settingsJson(
      personalSelected: true,
      personalProvider: 'openai',
      personalConfigured: false,
      sharedGranted: true,
      effectiveSource: 'personal',
      effectiveAvailable: false,
      reason: 'personal_credential_missing',
    ));

    expect(settings.providers.map((provider) => provider.id),
        containsAll(['openai', 'codex']));
    expect(settings.personal.selected, isTrue);
    expect(settings.personal.config?.provider, 'openai');
    expect(settings.personal.isConfigured('openai'), isFalse);
    expect(settings.personal.isConfigured('codex'), isTrue);
    expect(settings.shared.granted, isTrue);
    expect(settings.shared.config.provider, 'codex');
    expect(settings.effective.source, AiAccessSource.personal);
    expect(settings.effective.available, isFalse);
    expect(settings.effective.reason, 'personal_credential_missing');
  });

  test('uses the self-service settings and write-only credential routes',
      () async {
    final adapter = _AiSettingsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;
    final service = AiSettingsService(backendDio: dio);

    await service.getSettings();
    await service.setApiKey(
      'openai',
      'sk-personal',
      model: 'gpt-5.4-mini',
    );
    await service.usePersonal(provider: 'openai', model: 'gpt-5.4-mini');
    await service.deleteApiKey('openai');
    await service.useIncluded();

    expect(
      adapter.requests.map((request) => (request.$1, request.$2)),
      [
        ('GET', '/api/ai/settings'),
        ('PUT', '/api/ai/credentials/openai'),
        ('PUT', '/api/ai/settings'),
        ('DELETE', '/api/ai/credentials/openai'),
        ('DELETE', '/api/ai/settings'),
      ],
    );
    expect(adapter.requests[1].$3, {
      'api_key': 'sk-personal',
      'model': 'gpt-5.4-mini',
    });
    expect(adapter.requests[2].$3, {
      'provider': 'openai',
      'model': 'gpt-5.4-mini',
    });
  });

  test('combines a personal API key and model selection in one save', () async {
    final adapter = _AiSettingsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;
    final service = AiSettingsService(backendDio: dio);

    await service.usePersonal(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      apiKey: 'synthetic-personal-key',
    );

    expect(adapter.requests, hasLength(1));
    expect(adapter.requests.single.$1, 'PUT');
    expect(adapter.requests.single.$2, '/api/ai/settings');
    expect(adapter.requests.single.$3, {
      'provider': 'openai',
      'model': 'gpt-4.1-mini',
      'api_key': 'synthetic-personal-key',
    });
  });
}

Map<String, dynamic> _settingsJson({
  bool personalSelected = false,
  String personalProvider = '',
  bool personalConfigured = true,
  bool sharedGranted = true,
  String effectiveSource = 'shared',
  bool effectiveAvailable = true,
  String reason = '',
}) =>
    {
      'providers': [
        {
          'id': 'openai',
          'label': 'OpenAI',
          'auth_type': 'api_key',
          'credential_key': 'openai_key',
          'models': [
            {'id': 'gpt-5.4-mini', 'label': 'GPT-5.4 mini'},
          ],
        },
        {
          'id': 'codex',
          'label': 'OpenAI (OAuth)',
          'auth_type': 'user_oauth',
          'credential_key': '',
          'models': [
            {'id': 'default', 'label': 'OpenAI recommended'},
          ],
        },
      ],
      'personal': {
        'selected': personalSelected,
        'config': personalSelected
            ? {'provider': personalProvider, 'model': 'gpt-5.4-mini'}
            : null,
        'credentials': {
          'anthropic': false,
          'openai': personalConfigured,
          'gemini': false,
          'codex': true,
        },
      },
      'shared': {
        'granted': sharedGranted,
        'configured': true,
        'config': {'provider': 'codex', 'model': 'default'},
      },
      'effective': {
        'available': effectiveAvailable,
        'source': effectiveSource,
        'provider': effectiveSource == 'personal' ? personalProvider : 'codex',
        'model': effectiveSource == 'personal' ? 'gpt-5.4-mini' : 'default',
        'reason': reason,
      },
    };

class _AiSettingsAdapter implements HttpClientAdapter {
  final List<(String, String, dynamic)> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    requests.add((options.method, options.uri.path, body));

    if (options.method == 'PUT' && options.uri.path == '/api/ai/settings') {
      return _json(_settingsJson(
        personalSelected: true,
        personalProvider: 'openai',
        effectiveSource: 'personal',
      ));
    }
    if (options.method == 'GET') return _json(_settingsJson());
    return ResponseBody.fromString('', 204);
  }

  ResponseBody _json(Map<String, dynamic> value) => ResponseBody.fromString(
        jsonEncode(value),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );

  @override
  void close({bool force = false}) {}
}
