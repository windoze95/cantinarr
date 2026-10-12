import 'package:flutter/material.dart';

import '../data/ai_provider_models.dart';

/// Hidden against servers that do not advertise fallback support.
class ModelFallbackSetting extends StatelessWidget {
  final AiProviderOption? provider;
  final bool enabled;
  final ValueChanged<bool>? onChanged;
  final bool shared;

  const ModelFallbackSetting({
    super.key,
    required this.provider,
    required this.enabled,
    required this.onChanged,
    this.shared = false,
  });

  @override
  Widget build(BuildContext context) {
    final recommendation = provider?.modelFallback;
    if (recommendation == null) return const SizedBox.shrink();
    return SwitchListTile.adaptive(
      contentPadding: EdgeInsets.zero,
      title: const Text('Use the provider’s fallback model'),
      value: enabled,
      onChanged: onChanged,
      subtitle: Text(
        '${recommendation.source}'
        '${recommendation.model.isEmpty ? '' : ': ${recommendation.model}'}. '
        '${recommendation.description}'
        ' One replacement attempt only when the selected model is unavailable. Your saved selection, account, provider, and billing source stay the same. Administrators receive a notification with the model change.'
        '${shared ? ' This shared preference also applies to a separate remediation model override.' : ''}',
      ),
    );
  }
}
