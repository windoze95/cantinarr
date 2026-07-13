/// Server-described AI provider and model choices shared by personal and
/// included AI settings screens.
class AiProviderOption {
  final String id;
  final String label;
  final String credentialKey;
  final String authType;
  final List<AiModelOption> models;

  const AiProviderOption({
    required this.id,
    required this.label,
    required this.credentialKey,
    this.authType = 'api_key',
    required this.models,
  });

  bool get usesOAuth => authType != 'api_key';
  bool get usesUserOAuth => usesOAuth;

  factory AiProviderOption.fromJson(Map<String, dynamic> json) =>
      AiProviderOption(
        id: json['id'] as String? ?? '',
        label: json['label'] as String? ?? json['id'] as String? ?? '',
        credentialKey: json['credential_key'] as String? ?? '',
        authType: json['auth_type'] as String? ?? 'api_key',
        models: ((json['models'] as List?) ?? const [])
            .whereType<Map<String, dynamic>>()
            .map(AiModelOption.fromJson)
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
        id: json['id'] as String? ?? '',
        label: json['label'] as String? ?? json['id'] as String? ?? '',
        description: json['description'] as String? ?? '',
      );
}
