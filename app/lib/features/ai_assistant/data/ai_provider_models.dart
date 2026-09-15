/// Server-described AI provider and model choices shared by personal and
/// included AI settings screens.
class AiProviderOption {
  final String id;
  final String label;
  final String credentialKey;
  final String authType;

  /// Whether the shared profile accepts an admin-set endpoint override for
  /// this provider (openai only). Older servers never send the flag, which
  /// also means they reject the override key, so the field stays hidden.
  final bool supportsBaseUrl;

  /// Whether the shared profile accepts an admin-pinned reasoning effort for
  /// this provider, with the same old-server gating.
  final bool supportsReasoningEffort;

  /// Whether the shared profile can declare this provider's endpoint an
  /// internet host, so its traffic follows the outbound proxy. Only the local
  /// provider has an admin-typed endpoint; same old-server gating.
  final bool supportsProxyOptIn;

  /// Whether this provider exists only as the admin-configured shared
  /// profile (the local OpenAI-compatible entry). Personal payloads never
  /// carry it; the flag exists for the admin screen.
  final bool sharedOnly;
  final List<AiModelOption> models;

  const AiProviderOption({
    required this.id,
    required this.label,
    required this.credentialKey,
    this.authType = 'api_key',
    this.supportsBaseUrl = false,
    this.supportsReasoningEffort = false,
    this.supportsProxyOptIn = false,
    this.sharedOnly = false,
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
        supportsBaseUrl: json['supports_base_url'] as bool? ?? false,
        supportsReasoningEffort:
            json['supports_reasoning_effort'] as bool? ?? false,
        supportsProxyOptIn: json['supports_proxy_opt_in'] as bool? ?? false,
        sharedOnly: json['shared_only'] as bool? ?? false,
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
