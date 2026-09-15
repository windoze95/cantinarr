import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/settings/data/credentials_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter: records the composed request options and returns a
/// canned JSON body, so tests can pin what actually goes on the wire —
/// including per-request timeout overrides.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter(this.responseJson);

  final dynamic responseJson;
  RequestOptions? lastRequest;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    lastRequest = options;
    return ResponseBody.fromString(
      jsonEncode(responseJson),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

void main() {
  group('CredentialsStatus', () {
    test('uses provider metadata from the server', () {
      final status = CredentialsStatus.fromJson({
        'credentials': {
          'anthropic_key': true,
          'openai_key': false,
        },
        'tmdb_using_builtin': true,
        'ai': {
          'config': {
            'provider': 'openai',
            'model': 'gpt-5.4-mini',
          },
          'openai_reasoning_effort': 'low',
          'local_openai_base_url': 'http://llm-host:11434/v1',
          'local_openai_reasoning_effort': 'none',
          'providers': [
            {
              'id': 'openai',
              'label': 'OpenAI',
              'credential_key': 'openai_key',
              'supports_reasoning_effort': true,
              'models': [
                {
                  'id': 'gpt-5.4-mini',
                  'label': 'GPT-5.4 mini',
                },
              ],
            },
          ],
          'health_check': {
            'enabled': false,
            'interval_hours': 24,
            'last_checked_at': '2026-07-13T12:00:00Z',
          },
        },
      });

      expect(status.isConfigured('anthropic_key'), true);
      expect(status.tmdbUsingBuiltin, isTrue);
      expect(status.ai.provider, 'openai');
      expect(status.ai.model, 'gpt-5.4-mini');
      expect(status.ai.openaiReasoningEffort, 'low');
      expect(status.ai.localOpenaiBaseUrl, 'http://llm-host:11434/v1');
      expect(status.ai.localOpenaiReasoningEffort, 'none');
      expect(status.ai.providers.single.credentialKey, 'openai_key');
      expect(status.ai.providers.single.supportsReasoningEffort, isTrue);
      expect(status.ai.healthCheckEnabled, isFalse);
      expect(status.ai.healthCheckIntervalHours, 24);
      expect(status.ai.healthLastCheckedAt, isNotNull);
    });

    test('handles legacy AI status without provider metadata', () {
      final status = CredentialsStatus.fromJson({
        'anthropic_key': true,
        'ai': true,
      });

      expect(status.isConfigured('anthropic_key'), true);
      expect(status.tmdbUsingBuiltin, isFalse);
      expect(status.ai.provider, 'anthropic');
      expect(status.ai.model, 'claude-opus-4-8');
      expect(status.ai.openaiReasoningEffort, isEmpty);
      expect(status.ai.localOpenaiBaseUrl, isEmpty);
      expect(status.ai.localOpenaiReasoningEffort, isEmpty);
      // A server that predates the setting reads as off, which is also what
      // it does with the endpoint.
      expect(status.ai.localOpenaiUseProxy, isFalse);
      expect(status.ai.providers, isEmpty);
    });

    test('parses an OAuth provider without a credential key', () {
      final status = CredentialsStatus.fromJson({
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
              'models': [
                {'id': 'gpt-5.4', 'label': 'GPT-5.4'},
              ],
            },
          ],
        },
      });

      final provider = status.ai.providers.single;
      expect(provider.id, 'codex');
      expect(provider.credentialKey, isEmpty);
      expect(provider.authType, 'user_oauth');
      expect(provider.usesUserOAuth, isTrue);
      expect(provider.supportsBaseUrl, isFalse);
      expect(provider.supportsReasoningEffort, isFalse);
      expect(provider.supportsProxyOptIn, isFalse);
      expect(provider.sharedOnly, isFalse);
    });

    test('parses the local provider endpoint settings and its proxy opt-in',
        () {
      final status = CredentialsStatus.fromJson({
        'credentials': const <String, bool>{},
        'ai': {
          'config': {
            'provider': 'local_openai',
            'model': 'qwen3.6:35b-a3b',
          },
          'local_openai_base_url': 'https://llm.example.com/v1',
          'local_openai_reasoning_effort': 'none',
          'local_openai_use_proxy': true,
          'providers': [
            {
              'id': 'local_openai',
              'label': 'Local (OpenAI-compatible)',
              'auth_type': 'api_key',
              'credential_key': 'local_openai_key',
              'supports_base_url': true,
              'supports_reasoning_effort': true,
              'supports_proxy_opt_in': true,
              'shared_only': true,
              'models': <Map<String, dynamic>>[],
            },
          ],
        },
      });

      expect(status.ai.localOpenaiBaseUrl, 'https://llm.example.com/v1');
      expect(status.ai.localOpenaiReasoningEffort, 'none');
      expect(status.ai.localOpenaiUseProxy, isTrue);
      final provider = status.ai.providers.single;
      expect(provider.supportsProxyOptIn, isTrue);
      expect(provider.sharedOnly, isTrue);
    });
  });

  group('update', () {
    // The save-time probe now round-trips the full tool catalog through the
    // provider (issue #497), so headers can take well past the 15s base
    // timeout to arrive. The long-call options are what raise the web
    // connectTimeout alongside receiveTimeout — a bare receiveTimeout raise
    // silently keeps the 15s time-to-first-headers bound on web, and the
    // browser aborts into "Failed to save settings.".
    test('uses the long-call receive timeout, keeping the base connect timeout',
        () async {
      final adapter = _FakeAdapter({'status': 'ok'});
      final dio = Dio(BaseOptions(
        baseUrl: 'http://localhost',
        connectTimeout: const Duration(seconds: 15),
        receiveTimeout: const Duration(seconds: 15),
      ))
        ..httpClientAdapter = adapter;

      await CredentialsService(backendDio: dio).update({
        'anthropic_key': 'sk-ant-test',
        'ai_provider': 'anthropic',
        'ai_model': 'claude-haiku-4-5',
      });

      expect(adapter.lastRequest?.uri.path, '/api/admin/credentials');
      expect(adapter.lastRequest?.receiveTimeout, const Duration(seconds: 75));
      // connectTimeout stays the base default on native: longRequestOptions
      // passes null and Options.compose falls back to BaseOptions.
      expect(adapter.lastRequest?.connectTimeout, const Duration(seconds: 15));
    });
  });
}
