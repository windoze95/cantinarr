import 'package:cantinarr/l10n/l10n.dart';
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
      title: Text(context.l10n.aiUseFallback),
      value: enabled,
      onChanged: onChanged,
      subtitle: Text(
        context.l10n
            .settingsMessageValue1Value2Value3OneReplacementAttemptOnlyWhen(
                (recommendation.source).toString(),
                (recommendation.model.isEmpty
                        ? ''
                        : ': ${recommendation.model}')
                    .toString(),
                (recommendation.description).toString(),
                (shared
                        ? context.l10n
                            .settingsThisSharedPreferenceAlsoAppliesToASeparate
                        : '')
                    .toString()),
      ),
    );
  }
}
