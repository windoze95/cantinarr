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
    final providerOptions =
        providers.isNotEmpty ? providers : fallbackProviders;
    final defaultProvider =
        providerOptions.isNotEmpty ? providerOptions.first.id : 'anthropic';
    final selectedProvider = config['provider'] as String? ?? defaultProvider;
    AiProviderOption? selected;
    for (final provider in providerOptions) {
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
      providers: providerOptions,
    );
  }

  static const fallbackProviders = [
    AiProviderOption(
      id: 'anthropic',
      label: 'Anthropic',
      credentialKey: 'anthropic_key',
      models: [
        AiModelOption(
          id: 'claude-opus-4-8',
          label: 'Claude Opus 4.8',
          description: 'Most capable Claude Opus-tier model',
        ),
        AiModelOption(
          id: 'claude-fable-5',
          label: 'Claude Fable 5',
          description: 'Highest-capability Claude model',
        ),
        AiModelOption(
          id: 'claude-sonnet-4-6',
          label: 'Claude Sonnet 4.6',
          description: 'Balanced speed and intelligence',
        ),
        AiModelOption(
          id: 'claude-haiku-4-5',
          label: 'Claude Haiku 4.5',
          description: 'Fastest, lowest-cost Claude option',
        ),
      ],
    ),
    AiProviderOption(
      id: 'openai',
      label: 'OpenAI',
      credentialKey: 'openai_key',
      models: [
        AiModelOption(
          id: 'gpt-5.5',
          label: 'GPT-5.5',
          description: 'Flagship OpenAI model',
        ),
        AiModelOption(
          id: 'gpt-5.4',
          label: 'GPT-5.4',
          description: 'Affordable frontier model',
        ),
        AiModelOption(
          id: 'gpt-5.4-mini',
          label: 'GPT-5.4 mini',
          description: 'Lower latency and cost',
        ),
        AiModelOption(
          id: 'gpt-5.4-nano',
          label: 'GPT-5.4 nano',
          description: 'Smallest current GPT-5.4 model',
        ),
        AiModelOption(
          id: 'gpt-4.1',
          label: 'GPT-4.1',
          description: 'Stable previous-generation model',
        ),
        AiModelOption(
          id: 'gpt-4.1-mini',
          label: 'GPT-4.1 mini',
          description: 'Fast previous-generation model',
        ),
      ],
    ),
    AiProviderOption(
      id: 'gemini',
      label: 'Google Gemini',
      credentialKey: 'gemini_key',
      models: [
        AiModelOption(
          id: 'gemini-3.5-flash',
          label: 'Gemini 3.5 Flash',
          description: 'Current stable Gemini Flash model',
        ),
        AiModelOption(
          id: 'gemini-2.5-pro',
          label: 'Gemini 2.5 Pro',
          description: 'Advanced reasoning and coding',
        ),
        AiModelOption(
          id: 'gemini-2.5-flash',
          label: 'Gemini 2.5 Flash',
          description: 'Low-latency reasoning',
        ),
        AiModelOption(
          id: 'gemini-2.5-flash-lite',
          label: 'Gemini 2.5 Flash-Lite',
          description: 'Fastest budget Gemini option',
        ),
      ],
    ),
  ];
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
