import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import 'ai_provider_models.dart';

enum AiAccessSource { personal, shared, none }

AiAccessSource _accessSource(String? value) => switch (value) {
      'personal' => AiAccessSource.personal,
      'shared' => AiAccessSource.shared,
      _ => AiAccessSource.none,
    };

class AiProviderConfig {
  final String provider;
  final String model;

  const AiProviderConfig({required this.provider, required this.model});

  bool get isEmpty => provider.isEmpty;

  factory AiProviderConfig.fromJson(Map<String, dynamic> json) =>
      AiProviderConfig(
        provider: json['provider'] as String? ?? '',
        model: json['model'] as String? ?? '',
      );
}

class PersonalAiSettings {
  final bool selected;
  final AiProviderConfig? config;
  final Map<String, bool> credentials;

  const PersonalAiSettings({
    required this.selected,
    required this.config,
    required this.credentials,
  });

  bool isConfigured(String provider) => credentials[provider] ?? false;

  factory PersonalAiSettings.fromJson(Map<String, dynamic> json) {
    final rawConfig = json['config'];
    final rawCredentials = json['credentials'];
    return PersonalAiSettings(
      selected: json['selected'] as bool? ?? rawConfig != null,
      config: rawConfig is Map<String, dynamic>
          ? AiProviderConfig.fromJson(rawConfig)
          : null,
      credentials: rawCredentials is Map<String, dynamic>
          ? rawCredentials.map(
              (key, value) => MapEntry(key, value as bool? ?? false),
            )
          : const {},
    );
  }
}

class SharedAiSettings {
  final bool granted;
  final bool configured;
  final AiProviderConfig config;

  const SharedAiSettings({
    required this.granted,
    required this.configured,
    required this.config,
  });

  factory SharedAiSettings.fromJson(Map<String, dynamic> json) {
    final rawConfig = json['config'];
    return SharedAiSettings(
      granted: json['granted'] as bool? ?? false,
      configured: json['configured'] as bool? ?? false,
      config: rawConfig is Map<String, dynamic>
          ? AiProviderConfig.fromJson(rawConfig)
          : const AiProviderConfig(provider: '', model: ''),
    );
  }
}

class EffectiveAiSettings {
  final bool available;
  final AiAccessSource source;
  final String provider;
  final String model;
  final String reason;

  const EffectiveAiSettings({
    required this.available,
    required this.source,
    required this.provider,
    required this.model,
    required this.reason,
  });

  factory EffectiveAiSettings.fromJson(Map<String, dynamic> json) =>
      EffectiveAiSettings(
        available: json['available'] as bool? ?? false,
        source: _accessSource(json['source'] as String?),
        provider: json['provider'] as String? ?? '',
        model: json['model'] as String? ?? '',
        reason: json['reason'] as String? ?? '',
      );
}

class AiSettings {
  final List<AiProviderOption> providers;
  final PersonalAiSettings personal;
  final SharedAiSettings shared;
  final EffectiveAiSettings effective;

  const AiSettings({
    required this.providers,
    required this.personal,
    required this.shared,
    required this.effective,
  });

  AiProviderOption? provider(String id) {
    for (final option in providers) {
      if (option.id == id) return option;
    }
    return null;
  }

  String providerLabel(String id) => provider(id)?.label ?? _fallbackLabel(id);

  factory AiSettings.fromJson(Map<String, dynamic> json) {
    final personal = json['personal'];
    final shared = json['shared'];
    final effective = json['effective'];
    return AiSettings(
      providers: ((json['providers'] as List?) ?? const [])
          .whereType<Map<String, dynamic>>()
          .map(AiProviderOption.fromJson)
          .where((provider) => provider.id.isNotEmpty)
          .toList(),
      personal: PersonalAiSettings.fromJson(
        personal is Map<String, dynamic> ? personal : const {},
      ),
      shared: SharedAiSettings.fromJson(
        shared is Map<String, dynamic> ? shared : const {},
      ),
      effective: EffectiveAiSettings.fromJson(
        effective is Map<String, dynamic> ? effective : const {},
      ),
    );
  }
}

String _fallbackLabel(String provider) => switch (provider) {
      'anthropic' => 'Anthropic',
      'openai' => 'OpenAI',
      'gemini' => 'Google Gemini',
      'codex' => 'ChatGPT (Codex)',
      _ => provider,
    };

class AiSettingsService {
  final Dio _dio;

  AiSettingsService({required Dio backendDio}) : _dio = backendDio;

  Future<AiSettings> getSettings() async {
    final response = await _dio.get('/api/ai/settings');
    return AiSettings.fromJson(response.data as Map<String, dynamic>);
  }

  Future<AiSettings> usePersonal({
    required String provider,
    required String model,
  }) async {
    final response = await _dio.put(
      '/api/ai/settings',
      data: {'provider': provider, 'model': model},
    );
    return AiSettings.fromJson(response.data as Map<String, dynamic>);
  }

  /// Clears only the personal override. Stored personal keys and account links
  /// remain available for the user to select again later.
  Future<void> useIncluded() async {
    await _dio.delete('/api/ai/settings');
  }

  Future<void> setApiKey(String provider, String apiKey) async {
    await _dio.put(
      '/api/ai/credentials/${Uri.encodeComponent(provider)}',
      data: {'api_key': apiKey},
    );
  }

  Future<void> deleteApiKey(String provider) async {
    await _dio.delete(
      '/api/ai/credentials/${Uri.encodeComponent(provider)}',
    );
  }
}

final aiSettingsServiceProvider = Provider<AiSettingsService>(
  (ref) => AiSettingsService(backendDio: ref.watch(backendClientProvider)),
);

final aiSettingsProvider = FutureProvider.autoDispose<AiSettings>(
  (ref) => ref.watch(aiSettingsServiceProvider).getSettings(),
);
