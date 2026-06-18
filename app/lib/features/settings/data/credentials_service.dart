import 'package:dio/dio.dart';

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

    return CredentialsStatus(
      credentials: credentials,
      ai: AiCredentialConfig.fromJson(
        json['ai'] as Map<String, dynamic>? ?? const {},
      ),
    );
  }

  bool isConfigured(String key) => credentials[key] ?? false;
}

class AiCredentialConfig {
  final String provider;
  final String model;
  final List<AiProviderOption> providers;

  const AiCredentialConfig({
    required this.provider,
    required this.model,
    required this.providers,
  });

  factory AiCredentialConfig.fromJson(Map<String, dynamic> json) {
    final config = json['config'] as Map<String, dynamic>? ?? const {};
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
    );
  }
}

class AiProviderOption {
  final String id;
  final String label;
  final String credentialKey;
  final List<AiModelOption> models;

  const AiProviderOption({
    required this.id,
    required this.label,
    required this.credentialKey,
    required this.models,
  });

  factory AiProviderOption.fromJson(Map<String, dynamic> json) =>
      AiProviderOption(
        id: json['id'] as String,
        label: json['label'] as String? ?? json['id'] as String,
        credentialKey: json['credential_key'] as String,
        models: ((json['models'] as List?) ?? const [])
            .map((e) => AiModelOption.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

class AiModelOption {
  final String id;
  final String label;
  final String description;

  const AiModelOption({
    required this.id,
    required this.label,
    required this.description,
  });

  factory AiModelOption.fromJson(Map<String, dynamic> json) => AiModelOption(
        id: json['id'] as String,
        label: json['label'] as String? ?? json['id'] as String,
        description: json['description'] as String? ?? '',
      );
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
    await _dio.put('/api/admin/credentials', data: credentials);
  }

  /// Removes a single credential.
  Future<void> delete(String key) async {
    await _dio.delete('/api/admin/credentials/$key');
  }
}
