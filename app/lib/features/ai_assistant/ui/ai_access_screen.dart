import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/ai_settings_service.dart';
import '../data/codex_oauth_service.dart';

/// Self-service AI source selection for one Cantinarr account.
///
/// Personal credentials are intentionally separate from the server's included
/// provider. A selected but broken personal provider fails closed; this screen
/// makes switching back to included access an explicit action.
class AiAccessScreen extends ConsumerStatefulWidget {
  const AiAccessScreen({super.key});

  @override
  ConsumerState<AiAccessScreen> createState() => _AiAccessScreenState();
}

class _AiAccessScreenState extends ConsumerState<AiAccessScreen> {
  static const _customModel = '__custom__';

  late final TextEditingController _apiKeyController;
  late final TextEditingController _customModelController;
  String? _provider;
  String? _model;
  bool _saving = false;
  bool _clearing = false;

  @override
  void initState() {
    super.initState();
    _apiKeyController = TextEditingController();
    _customModelController = TextEditingController();
  }

  @override
  void dispose() {
    _apiKeyController.dispose();
    _customModelController.dispose();
    super.dispose();
  }

  void _ensureSelection(AiSettings settings) {
    if (_provider != null) return;
    final configured = settings.personal.config;
    final provider = configured?.provider.isNotEmpty == true
        ? configured!.provider
        : settings.providers.firstOrNull?.id;
    _provider = provider;
    final option = settings.provider(provider ?? '');
    final configuredModel =
        configured?.provider == provider ? configured?.model ?? '' : '';
    if (configuredModel.isNotEmpty &&
        option?.models.any((model) => model.id == configuredModel) != true) {
      _model = _customModel;
      _customModelController.text = configuredModel;
    } else {
      _model = configuredModel.isNotEmpty
          ? configuredModel
          : option?.models.firstOrNull?.id ?? _customModel;
    }
  }

  void _selectProvider(AiSettings settings, String provider) {
    final option = settings.provider(provider);
    setState(() {
      _provider = provider;
      _model = option?.models.firstOrNull?.id ?? _customModel;
      _apiKeyController.clear();
      _customModelController.clear();
    });
  }

  String? _selectedModel() {
    if (_model == _customModel) {
      final value = _customModelController.text.trim();
      return value.isEmpty ? null : value;
    }
    return _model;
  }

  Future<void> _saveAndUse(AiSettings settings, {bool saveKey = false}) async {
    final provider = _provider;
    final model = _selectedModel();
    if (provider == null || model == null) {
      _message('Choose a model or enter a custom model ID.');
      return;
    }
    if (saveKey && _apiKeyController.text.trim().isEmpty) {
      _message('Enter an API key first.');
      return;
    }

    setState(() => _saving = true);
    try {
      final service = ref.read(aiSettingsServiceProvider);
      if (saveKey) {
        await service.setApiKey(provider, _apiKeyController.text.trim());
      }
      await service.usePersonal(provider: provider, model: model);
      _apiKeyController.clear();
      await _refresh();
      _message('Personal ${settings.providerLabel(provider)} is now active.');
    } catch (error) {
      _message(_friendlyError(error, 'Could not update personal AI access.'));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _deleteKey(AiSettings settings, String provider) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text('Remove ${settings.providerLabel(provider)} key?'),
        content: const Text(
          'The key will be deleted. If this is your selected personal '
          'provider, AI will stop rather than silently use included access.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child:
                const Text('Remove', style: TextStyle(color: AppTheme.error)),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    setState(() => _saving = true);
    try {
      await ref.read(aiSettingsServiceProvider).deleteApiKey(provider);
      await _refresh();
      _message('${settings.providerLabel(provider)} key removed.');
    } catch (error) {
      _message(_friendlyError(error, 'Could not remove the API key.'));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _useIncluded() async {
    setState(() => _clearing = true);
    try {
      await ref.read(aiSettingsServiceProvider).useIncluded();
      await _refresh();
      _message('Included AI selected. Your personal credentials were kept.');
    } catch (error) {
      _message(_friendlyError(error, 'Could not select included AI.'));
    } finally {
      if (mounted) setState(() => _clearing = false);
    }
  }

  Future<void> _openChatGPT() async {
    await context.push('/settings/chatgpt');
    if (!mounted) return;
    ref.invalidate(codexConnectionStatusProvider);
    await _refresh();
  }

  Future<void> _refresh() async {
    ref.invalidate(aiSettingsProvider);
    try {
      await ref.read(authProvider.notifier).refreshConfig();
    } catch (_) {
      // The live settings response is authoritative on this screen. Cached app
      // availability refreshes again on resume if this request fails.
    }
  }

  void _message(String text) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
  }

  @override
  Widget build(BuildContext context) {
    final settings = ref.watch(aiSettingsProvider);
    return Scaffold(
      appBar: AppBar(
        title: const Text('AI Access'),
        actions: [
          IconButton(
            onPressed: () => ref.invalidate(aiSettingsProvider),
            icon: const Icon(Icons.refresh_rounded),
            tooltip: 'Refresh AI access',
          ),
        ],
      ),
      body: CenteredContent(
        child: settings.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppTheme.accent),
          ),
          error: (error, _) => _LoadError(
            onRetry: () => ref.invalidate(aiSettingsProvider),
          ),
          data: (value) {
            _ensureSelection(value);
            return _buildSettings(value);
          },
        ),
      ),
    );
  }

  Widget _buildSettings(AiSettings settings) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        _EffectiveSourcePanel(settings: settings),
        const SizedBox(height: 22),
        _PersonalSourcePanel(
          settings: settings,
          provider: _provider,
          model: _model,
          customModelValue: _customModel,
          customModelController: _customModelController,
          apiKeyController: _apiKeyController,
          saving: _saving,
          onProviderSelected: (provider) => _selectProvider(settings, provider),
          onModelSelected: (model) => setState(() => _model = model),
          onSaveKeyAndUse: () => _saveAndUse(settings, saveKey: true),
          onUseConfigured: () => _saveAndUse(settings),
          onDeleteKey: (provider) => _deleteKey(settings, provider),
          onOpenChatGPT: _openChatGPT,
        ),
        const SizedBox(height: 16),
        _IncludedSourcePanel(
          settings: settings,
          clearing: _clearing,
          onUseIncluded: _useIncluded,
        ),
      ],
    );
  }
}

class _EffectiveSourcePanel extends StatelessWidget {
  final AiSettings settings;

  const _EffectiveSourcePanel({required this.settings});

  @override
  Widget build(BuildContext context) {
    final effective = settings.effective;
    final personalBroken =
        effective.source == AiAccessSource.personal && !effective.available;
    final color = effective.available
        ? AppTheme.available
        : personalBroken
            ? AppTheme.warning
            : AppTheme.signal;
    final sourceLabel = switch (effective.source) {
      AiAccessSource.personal => 'Personal',
      AiAccessSource.shared => 'Included',
      AiAccessSource.none => 'No active source',
    };
    final provider = effective.provider.isEmpty
        ? ''
        : settings.providerLabel(effective.provider);

    return AppPanel(
      accentColor: color,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 48,
            height: 48,
            decoration: BoxDecoration(
              color: color.withValues(alpha: 0.14),
              borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
              border: Border.all(color: color.withValues(alpha: 0.28)),
            ),
            child: Icon(
              effective.available
                  ? Icons.bolt_rounded
                  : Icons.power_settings_new_rounded,
              color: color,
            ),
          ),
          const SizedBox(width: 14),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  effective.available ? 'POWERING YOUR ASSISTANT' : 'AI SOURCE',
                  style: TextStyle(
                    color: color,
                    fontSize: 10,
                    fontWeight: FontWeight.w800,
                    letterSpacing: 1.35,
                  ),
                ),
                const SizedBox(height: 5),
                Text(
                  provider.isEmpty ? sourceLabel : '$sourceLabel · $provider',
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (effective.model.isNotEmpty) ...[
                  const SizedBox(height: 3),
                  Text(
                    effective.model,
                    style: const TextStyle(color: AppTheme.textMuted),
                  ),
                ],
                if (personalBroken) ...[
                  const SizedBox(height: 9),
                  const Text(
                    'Your personal choice needs attention. Cantinarr did not '
                    'fall back to the server account.',
                    style: TextStyle(
                      color: AppTheme.warning,
                      fontSize: 13,
                      height: 1.4,
                    ),
                  ),
                ] else if (!effective.available) ...[
                  const SizedBox(height: 9),
                  Text(
                    _reasonCopy(effective.reason),
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 13,
                      height: 1.4,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _PersonalSourcePanel extends StatelessWidget {
  final AiSettings settings;
  final String? provider;
  final String? model;
  final String customModelValue;
  final TextEditingController customModelController;
  final TextEditingController apiKeyController;
  final bool saving;
  final ValueChanged<String> onProviderSelected;
  final ValueChanged<String> onModelSelected;
  final VoidCallback onSaveKeyAndUse;
  final VoidCallback onUseConfigured;
  final ValueChanged<String> onDeleteKey;
  final VoidCallback onOpenChatGPT;

  const _PersonalSourcePanel({
    required this.settings,
    required this.provider,
    required this.model,
    required this.customModelValue,
    required this.customModelController,
    required this.apiKeyController,
    required this.saving,
    required this.onProviderSelected,
    required this.onModelSelected,
    required this.onSaveKeyAndUse,
    required this.onUseConfigured,
    required this.onDeleteKey,
    required this.onOpenChatGPT,
  });

  @override
  Widget build(BuildContext context) {
    final option = settings.provider(provider ?? '');
    final configured =
        option != null && settings.personal.isConfigured(option.id);
    final active = settings.effective.source == AiAccessSource.personal;
    final activeThisProvider =
        active && settings.personal.config?.provider == option?.id;
    final modelIds = option?.models.map((model) => model.id).toSet() ?? {};
    final modelValue = model == customModelValue || modelIds.contains(model)
        ? model
        : (option?.models.firstOrNull?.id ?? customModelValue);

    return AppPanel(
      accentColor: active ? AppTheme.accent : AppTheme.signal,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _SourceHeader(
            icon: Icons.person_outline_rounded,
            eyebrow: 'PERSONAL',
            title: 'Your provider',
            status: active ? 'Selected' : 'Optional override',
            active: active,
          ),
          const SizedBox(height: 7),
          const Text(
            'A personal provider takes priority over included AI. Keys and '
            'ChatGPT authorization stay encrypted on your Cantinarr server.',
            style: TextStyle(color: AppTheme.textSecondary, height: 1.42),
          ),
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: settings.providers
                .map(
                  (item) => ChoiceChip(
                    label: Text(item.label),
                    selected: item.id == option?.id,
                    onSelected: (_) => onProviderSelected(item.id),
                  ),
                )
                .toList(),
          ),
          if (option != null) ...[
            const SizedBox(height: 16),
            DropdownButtonFormField<String>(
              key: ValueKey('personal-model-${option.id}-$modelValue'),
              initialValue: modelValue,
              isExpanded: true,
              decoration: const InputDecoration(
                labelText: 'Model',
                isDense: true,
              ),
              items: [
                ...option.models.map(
                  (item) => DropdownMenuItem(
                    value: item.id,
                    child: Text(item.label),
                  ),
                ),
                DropdownMenuItem(
                  value: customModelValue,
                  child: const Text('Custom model ID'),
                ),
              ],
              onChanged: (value) {
                if (value != null) onModelSelected(value);
              },
            ),
            if (modelValue == customModelValue) ...[
              const SizedBox(height: 12),
              TextField(
                controller: customModelController,
                decoration: const InputDecoration(
                  labelText: 'Custom model ID',
                  isDense: true,
                ),
              ),
            ],
            const SizedBox(height: 14),
            if (option.usesOAuth)
              _ChatGPTActions(
                configured: configured,
                active: activeThisProvider,
                saving: saving,
                onManage: onOpenChatGPT,
                onUse: onUseConfigured,
              )
            else ...[
              TextField(
                controller: apiKeyController,
                obscureText: true,
                enableSuggestions: false,
                autocorrect: false,
                decoration: InputDecoration(
                  labelText: '${option.label} API key',
                  hintText: configured
                      ? 'Enter a new key to replace the saved one'
                      : 'Paste a personal API key',
                  prefixIcon: const Icon(Icons.key_rounded),
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: ElevatedButton.icon(
                      onPressed: saving ? null : onSaveKeyAndUse,
                      icon: saving
                          ? const SizedBox(
                              width: 17,
                              height: 17,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Icon(Icons.lock_outline_rounded, size: 18),
                      label: Text(
                          configured ? 'Replace key & use' : 'Save key & use'),
                    ),
                  ),
                  if (configured) ...[
                    const SizedBox(width: 8),
                    IconButton(
                      onPressed: saving ? null : () => onDeleteKey(option.id),
                      icon: const Icon(Icons.delete_outline_rounded),
                      color: AppTheme.error,
                      tooltip: 'Remove ${option.label} key',
                    ),
                  ],
                ],
              ),
              if (configured && !activeThisProvider) ...[
                const SizedBox(height: 8),
                OutlinedButton(
                  onPressed: saving ? null : onUseConfigured,
                  child: Text('Use saved ${option.label} key'),
                ),
              ],
            ],
          ],
        ],
      ),
    );
  }
}

class _ChatGPTActions extends StatelessWidget {
  final bool configured;
  final bool active;
  final bool saving;
  final VoidCallback onManage;
  final VoidCallback onUse;

  const _ChatGPTActions({
    required this.configured,
    required this.active,
    required this.saving,
    required this.onManage,
    required this.onUse,
  });

  @override
  Widget build(BuildContext context) {
    if (!configured) {
      return ElevatedButton.icon(
        onPressed: onManage,
        icon: const Icon(Icons.open_in_browser_rounded, size: 18),
        label: const Text('Connect personal ChatGPT'),
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        OutlinedButton.icon(
          onPressed: onManage,
          icon: const Icon(Icons.manage_accounts_outlined, size: 18),
          label: const Text('Manage personal ChatGPT'),
        ),
        if (!active) ...[
          const SizedBox(height: 8),
          ElevatedButton(
            onPressed: saving ? null : onUse,
            child: const Text('Use personal ChatGPT'),
          ),
        ],
      ],
    );
  }
}

class _IncludedSourcePanel extends StatelessWidget {
  final AiSettings settings;
  final bool clearing;
  final VoidCallback onUseIncluded;

  const _IncludedSourcePanel({
    required this.settings,
    required this.clearing,
    required this.onUseIncluded,
  });

  @override
  Widget build(BuildContext context) {
    final shared = settings.shared;
    final selected = settings.effective.source == AiAccessSource.shared;
    final active = selected && settings.effective.available;
    final provider = shared.config.provider.isEmpty
        ? 'No provider selected'
        : settings.providerLabel(shared.config.provider);
    final status = !shared.granted
        ? 'Not included'
        : active
            ? 'Active'
            : selected && shared.configured
                ? 'Unavailable'
                : shared.configured
                    ? 'Ready'
                    : 'Needs admin setup';

    return AppPanel(
      accentColor: active
          ? AppTheme.available
          : selected
              ? AppTheme.warning
              : AppTheme.signal,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _SourceHeader(
            icon: Icons.family_restroom_rounded,
            eyebrow: 'INCLUDED',
            title: 'Provided by this server',
            status: status,
            active: active,
          ),
          const SizedBox(height: 10),
          Text(
            provider,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 16,
              fontWeight: FontWeight.w600,
            ),
          ),
          if (shared.config.model.isNotEmpty) ...[
            const SizedBox(height: 2),
            Text(
              shared.config.model,
              style: const TextStyle(color: AppTheme.textMuted, fontSize: 12),
            ),
          ],
          const SizedBox(height: 8),
          Text(
            !shared.granted
                ? 'Your admin has not included shared AI access for this '
                    'Cantinarr account. You can still use any personal provider.'
                : shared.configured
                    ? 'Use the server provider without adding a personal key. '
                        'Your saved personal credentials are kept for later.'
                    : 'Your account is included, but the server provider is not '
                        'ready. Ask an admin to finish its setup.',
            style: const TextStyle(color: AppTheme.textSecondary, height: 1.42),
          ),
          if (shared.granted && !selected) ...[
            const SizedBox(height: 14),
            ElevatedButton.icon(
              onPressed: clearing ? null : onUseIncluded,
              icon: clearing
                  ? const SizedBox(
                      width: 17,
                      height: 17,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.group_outlined, size: 18),
              label: const Text('Use included access'),
            ),
          ],
        ],
      ),
    );
  }
}

class _SourceHeader extends StatelessWidget {
  final IconData icon;
  final String eyebrow;
  final String title;
  final String status;
  final bool active;

  const _SourceHeader({
    required this.icon,
    required this.eyebrow,
    required this.title,
    required this.status,
    required this.active,
  });

  @override
  Widget build(BuildContext context) {
    final color = active ? AppTheme.available : AppTheme.signal;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, color: color, size: 24),
        const SizedBox(width: 11),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                eyebrow,
                style: TextStyle(
                  color: color,
                  fontSize: 10,
                  fontWeight: FontWeight.w800,
                  letterSpacing: 1.25,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                title,
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
        ),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
          decoration: BoxDecoration(
            color: color.withValues(alpha: 0.13),
            borderRadius: BorderRadius.circular(AppTheme.radiusPill),
          ),
          child: Text(
            status,
            style: TextStyle(
              color: color,
              fontSize: 11,
              fontWeight: FontWeight.w700,
            ),
          ),
        ),
      ],
    );
  }
}

class _LoadError extends StatelessWidget {
  final VoidCallback onRetry;

  const _LoadError({required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off_outlined,
                color: AppTheme.textSecondary, size: 42),
            const SizedBox(height: 12),
            const Text(
              'Could not load AI access settings.',
              style: TextStyle(color: AppTheme.textSecondary),
            ),
            const SizedBox(height: 12),
            OutlinedButton(onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}

String _reasonCopy(String reason) => switch (reason) {
      'shared_access_disabled' =>
        'Add a personal provider, or ask your admin to include AI access.',
      'shared_credential_missing' =>
        'Included access is enabled, but the server provider needs setup.',
      'personal_credential_missing' =>
        'The selected personal API key is missing.',
      'personal_codex_disconnected' =>
        'The selected personal ChatGPT account needs to be connected.',
      'shared_codex_disconnected' =>
        'Included ChatGPT needs the server admin to connect its shared account.',
      'codex_unavailable' =>
        'ChatGPT is configured, but Codex is unavailable on this server.',
      'storage_error' =>
        'Cantinarr could not read the saved AI credentials. Ask the admin to '
            'check the server logs and encryption key.',
      _ => 'Choose a personal provider or use included access.',
    };

String _friendlyError(Object error, String fallback) {
  final match = RegExp(r'"error":"([^"]+)"').firstMatch(error.toString());
  return match?.group(1) ?? fallback;
}
