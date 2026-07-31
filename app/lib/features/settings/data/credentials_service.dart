import 'package:dio/dio.dart';

import '../../ai_assistant/data/ai_provider_models.dart';

export '../../ai_assistant/data/ai_provider_models.dart'
    show AiModelOption, AiProviderOption;

const _aiValidationReceiveTimeout = Duration(seconds: 75);

class CredentialsStatus {
  final Map<String, bool> credentials;
  final AiCredentialConfig ai;

  const CredentialsStatus({
    required this.credentials,
    required this.ai,
  });

  factory CredentialsStatus.fromJson(Map<String, dynamic> json) {
    final rawCredentials = json['credentials'] as Map<String, dynamic>?;
    final credentials = rawCredentials != null
        ? rawCredentials.map((k, v) => MapEntry(k, v as bool? ?? false))
        : json.map((k, v) => MapEntry(k, v is bool ? v : false));

    final aiJson = json['ai'];
    return CredentialsStatus(
      credentials: credentials,
      ai: AiCredentialConfig.fromJson(
        aiJson is Map<String, dynamic> ? aiJson : const {},
      ),
    );
  }

  bool isConfigured(String key) => credentials[key] ?? false;
}

class AiCredentialConfig {
  final String provider;
  final String model;
  final List<AiProviderOption> providers;
  final bool healthCheckEnabled;
  final int healthCheckIntervalHours;
  final DateTime? healthLastCheckedAt;

  const AiCredentialConfig({
    required this.provider,
    required this.model,
    required this.providers,
    required this.healthCheckEnabled,
    required this.healthCheckIntervalHours,
    required this.healthLastCheckedAt,
  });

  factory AiCredentialConfig.fromJson(Map<String, dynamic> json) {
    final config = json['config'] as Map<String, dynamic>? ?? const {};
    final health = json['health_check'] as Map<String, dynamic>? ?? const {};
    final providersJson = json['providers'] as List? ?? const [];
    final providers = providersJson
        .map((e) => AiProviderOption.fromJson(e as Map<String, dynamic>))
        .toList();
    final defaultProvider =
        providers.isNotEmpty ? providers.first.id : 'anthropic';
    final selectedProvider = config['provider'] as String? ?? defaultProvider;
    AiProviderOption? selected;
    for (final provider in providers) {
      if (provider.id == selectedProvider) {
        selected = provider;
        break;
      }
    }

    return AiCredentialConfig(
      provider: selectedProvider,
      model: config['model'] as String? ??
          (selected?.models.isNotEmpty == true
              ? selected!.models.first.id
              : 'claude-opus-4-8'),
      providers: providers,
      healthCheckEnabled: health['enabled'] as bool? ?? true,
      healthCheckIntervalHours:
          (health['interval_hours'] as num?)?.toInt() ?? 24,
      healthLastCheckedAt: DateTime.tryParse(
        health['last_checked_at'] as String? ?? '',
      )?.toLocal(),
    );
  }
}

/// API service for admin credential management (write-only).
class CredentialsService {
  final Dio _dio;

  CredentialsService({required Dio backendDio}) : _dio = backendDio;

  /// Returns which credentials are configured (booleans, never values).
  Future<CredentialsStatus> getStatus() async {
    final resp = await _dio.get('/api/admin/credentials');
    return CredentialsStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Updates one or more credentials. Only non-empty values are written.
  Future<void> update(Map<String, String> credentials) async {
    await _dio.put(
      '/api/admin/credentials',
      data: credentials,
      options: Options(receiveTimeout: _aiValidationReceiveTimeout),
    );
  }

  /// Removes a single credential.
  Future<void> delete(String key) async {
    await _dio.delete('/api/admin/credentials/$key');
  }
}
