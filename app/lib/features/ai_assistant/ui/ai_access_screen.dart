import 'package:cantinarr/l10n/l10n.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../auth/logic/auth_provider.dart';
import '../../settings/settings_anchors.dart';
import '../data/ai_provider_models.dart';
import 'model_fallback_setting.dart';
import '../data/ai_settings_service.dart';
import '../data/codex_oauth_service.dart';
import '../data/grok_oauth_service.dart';

/// Self-service AI source selection for one Cantinarr account.
///
/// Personal credentials are intentionally separate from the server's included
/// provider. A selected but broken personal provider fails closed; this screen
/// makes switching back to included access an explicit action.
class AiAccessScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const AiAccessScreen({super.key, this.highlightId});

  @override
  ConsumerState<AiAccessScreen> createState() => _AiAccessScreenState();
}

class _AiAccessScreenState extends ConsumerState<AiAccessScreen> {
  final _draft = SettingsDraft();
  Object get _draftValues => [
        _provider,
        _model,
        _modelFallbackEnabled,
        if (_model == _customModel) _customModelController.text,
      ];
  static const _customModel = '__custom__';

  late final TextEditingController _apiKeyController;
  late final TextEditingController _customModelController;
  String? _provider;
  String? _model;
  bool _modelFallbackEnabled = false;
  bool _saving = false;
  bool _clearing = false;
  bool _personalExpanded = false;

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
    _modelFallbackEnabled = configured?.modelFallbackEnabled ?? false;
    // Nothing chosen yet: adopt the server-advertised zero-config default
    // (OpenAI OAuth + the fast tier) before falling back to list order.
    final fallback = settings.provider(settings.defaultProvider ?? '') != null
        ? settings.defaultProvider
        : settings.providers.firstOrNull?.id;
    final provider = configured?.provider.isNotEmpty == true
        ? configured!.provider
        : fallback;
    _provider = provider;
    final option = settings.provider(provider ?? '');
    final configuredModel =
        configured?.provider == provider ? configured?.model ?? '' : '';
    if (configuredModel.isNotEmpty &&
        option?.models.any((model) => model.id == configuredModel) != true) {
      _model = _customModel;
      _customModelController.text = configuredModel;
    } else if (configuredModel.isNotEmpty) {
      _model = configuredModel;
    } else {
      final serverDefaultModel = provider == settings.defaultProvider &&
              option?.models
                      .any((model) => model.id == settings.defaultModel) ==
                  true
          ? settings.defaultModel
          : null;
      _model =
          serverDefaultModel ?? option?.models.firstOrNull?.id ?? _customModel;
    }
    _draft.markSaved(_draftValues);
  }

  void _selectProvider(AiSettings settings, String provider) {
    final option = settings.provider(provider);
    setState(() {
      _modelFallbackEnabled = settings.personal.config?.provider == provider &&
          (settings.personal.config?.modelFallbackEnabled ?? false);
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
      _message(context.l10n.settingsChooseAModelOrEnterACustomModel);
      return;
    }
    if (saveKey && _apiKeyController.text.trim().isEmpty) {
      _message(context.l10n.settingsEnterAnApiKeyFirst);
      return;
    }

    setState(() => _saving = true);
    try {
      final service = ref.read(aiSettingsServiceProvider);
      await service.usePersonal(
        provider: provider,
        model: model,
        modelFallbackEnabled: settings.provider(provider)?.modelFallback != null
            ? _modelFallbackEnabled
            : null,
        apiKey: saveKey ? _apiKeyController.text.trim() : null,
      );
      _apiKeyController.clear();
      _draft.markSaved(_draftValues);
      await _refresh();
      _message(
        context.l10n.settingsMessagePersonalValue1SettingsSaved(
            (settings.providerLabel(provider)).toString()),
      );
    } catch (error) {
      _message(_friendlyError(
          error, context.l10n.settingsCouldNotUpdatePersonalAiAccess));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _deleteKey(AiSettings settings, String provider) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(context.l10n.settingsMessageRemoveValue1Key(
            (settings.providerLabel(provider)).toString())),
        content: Text(
          context.l10n.settingsTheKeyWillBeDeletedIfThisIs,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(context.l10n.settingsCancel),
          ),
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: Text(context.l10n.settingsRemove,
                style: TextStyle(color: AppTheme.error)),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    setState(() => _saving = true);
    try {
      await ref.read(aiSettingsServiceProvider).deleteApiKey(provider);
      await _refresh();
      _message(context.l10n.settingsMessageValue1KeyRemoved(
          (settings.providerLabel(provider)).toString()));
    } catch (error) {
      _message(
          _friendlyError(error, context.l10n.settingsCouldNotRemoveTheApiKey));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _useIncluded() async {
    setState(() => _clearing = true);
    try {
      await ref.read(aiSettingsServiceProvider).useIncluded();
      await _refresh();
      if (mounted) setState(() => _personalExpanded = false);
      _message(context
          .l10n.settingsIncludedAiSelectedYourPersonalCredentialsWereKept);
    } catch (error) {
      _message(
          _friendlyError(error, context.l10n.settingsCouldNotSelectIncludedAi));
    } finally {
      if (mounted) setState(() => _clearing = false);
    }
  }

  Future<void> _openOAuth(String? provider) async {
    // Explicit per-provider routing: a future OAuth provider must add its own
    // branch rather than silently landing on another provider's sign-in.
    switch (provider) {
      case 'grok_oauth':
        await context.push('/settings/grok');
        if (!mounted) return;
        ref.invalidate(grokConnectionStatusProvider);
      case 'codex':
        await context.push('/settings/chatgpt');
        if (!mounted) return;
        ref.invalidate(codexConnectionStatusProvider);
      default:
        return;
    }
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
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () =>
            _apiKeyController.text.isNotEmpty ||
            _draft.hasChanges(_draftValues),
        isSaving: _saving || _clearing,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) {
    final settings = ref.watch(aiSettingsProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(context.l10n.settingsAiAccess),
        actions: [
          IconButton(
            onPressed: () => ref.invalidate(aiSettingsProvider),
            icon: const Icon(Icons.refresh_rounded),
            tooltip: context.l10n.settingsRefreshAiAccess,
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
    final includedActive = settings.effective.source == AiAccessSource.shared &&
        settings.effective.available;
    return ListView(
      // Build every child while a settings-search highlight needs to find
      // its anchor (see SettingsHighlight).
      cacheExtent: SettingsHighlight.cacheExtentFor(widget.highlightId),
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        _EffectiveSourcePanel(settings: settings),
        const SizedBox(height: 22),
        SettingsHighlight(
          anchorId: SettingsAnchors.aiAccessIncluded,
          highlightId: widget.highlightId,
          child: _IncludedSourcePanel(
            settings: settings,
            clearing: _clearing,
            onUseIncluded: _useIncluded,
          ),
        ),
        const SizedBox(height: 16),
        SettingsHighlight(
          anchorId: SettingsAnchors.aiAccessPersonal,
          highlightId: widget.highlightId,
          child: _PersonalSourcePanel(
            settings: settings,
            provider: _provider,
            model: _model,
            customModelValue: _customModel,
            customModelController: _customModelController,
            apiKeyController: _apiKeyController,
            saving: _saving,
            modelFallbackEnabled: _modelFallbackEnabled,
            onFallbackChanged: (value) =>
                setState(() => _modelFallbackEnabled = value),
            collapsed: includedActive && !_personalExpanded,
            onToggleCollapsed: includedActive
                ? () => setState(() => _personalExpanded = !_personalExpanded)
                : null,
            onProviderSelected: (provider) =>
                _selectProvider(settings, provider),
            onModelSelected: (model) => setState(() => _model = model),
            onSaveKeyAndUse: () => _saveAndUse(settings, saveKey: true),
            onUseConfigured: () => _saveAndUse(settings),
            onDeleteKey: (provider) => _deleteKey(settings, provider),
            onManageOAuth: () => _openOAuth(_provider),
          ),
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
      AiAccessSource.personal => context.l10n.settingsPersonal,
      AiAccessSource.shared => context.l10n.settingsIncluded,
      AiAccessSource.none => context.l10n.settingsNoActiveSource,
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
                  effective.available
                      ? context.l10n.settingsPoweringYourAssistant
                      : context.l10n.settingsAiSource,
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
                  Text(
                    context.l10n
                        .settingsYourPersonalChoiceNeedsAttentionCantinarrDidNot,
                    style: TextStyle(
                      color: AppTheme.warning,
                      fontSize: 13,
                      height: 1.4,
                    ),
                  ),
                ] else if (!effective.available) ...[
                  const SizedBox(height: 9),
                  Text(
                    _reasonCopy(context, effective.reason),
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
  final bool modelFallbackEnabled;
  final ValueChanged<bool> onFallbackChanged;
  final bool collapsed;
  final VoidCallback? onToggleCollapsed;
  final ValueChanged<String> onProviderSelected;
  final ValueChanged<String> onModelSelected;
  final VoidCallback onSaveKeyAndUse;
  final VoidCallback onUseConfigured;
  final ValueChanged<String> onDeleteKey;
  final VoidCallback onManageOAuth;

  const _PersonalSourcePanel({
    required this.settings,
    required this.provider,
    required this.model,
    required this.customModelValue,
    required this.customModelController,
    required this.apiKeyController,
    required this.saving,
    required this.modelFallbackEnabled,
    required this.onFallbackChanged,
    required this.collapsed,
    required this.onToggleCollapsed,
    required this.onProviderSelected,
    required this.onModelSelected,
    required this.onSaveKeyAndUse,
    required this.onUseConfigured,
    required this.onDeleteKey,
    required this.onManageOAuth,
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

    final collapsible = onToggleCollapsed != null;
    final header = _SourceHeader(
      icon: Icons.person_outline_rounded,
      eyebrow: context.l10n.settingsPersonal2,
      title: context.l10n.settingsYourProvider,
      status: active
          ? context.l10n.settingsSelected
          : context.l10n.settingsOptionalOverride,
      active: active,
      trailing: collapsible
          ? Icon(
              collapsed ? Icons.expand_more_rounded : Icons.expand_less_rounded,
              color: AppTheme.textMuted,
            )
          : null,
    );

    if (collapsed) {
      return AppPanel(
        accentColor: AppTheme.signal,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: onToggleCollapsed,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              header,
              const SizedBox(height: 7),
              Text(
                context.l10n.settingsIncludedAiAlreadyPowersYourAssistantSoA,
                style: TextStyle(color: AppTheme.textSecondary, height: 1.42),
              ),
            ],
          ),
        ),
      );
    }

    return AppPanel(
      accentColor: active ? AppTheme.accent : AppTheme.signal,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (collapsible)
            GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: onToggleCollapsed,
              child: header,
            )
          else
            header,
          const SizedBox(height: 7),
          Text(
            context.l10n.settingsAPersonalProviderTakesPriorityOverIncludedAi,
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
              decoration: InputDecoration(
                labelText: context.l10n.settingsModel,
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
                  child: Text(context.l10n.settingsCustomModelId),
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
                decoration: InputDecoration(
                  labelText: context.l10n.settingsCustomModelId,
                  isDense: true,
                ),
              ),
            ],
            const SizedBox(height: 14),
            if (option.usesOAuth)
              _OAuthActions(
                brand: _oauthBrand(option),
                configured: configured,
                active: activeThisProvider,
                saving: saving,
                onManage: onManageOAuth,
                onUse: onUseConfigured,
              )
            else ...[
              TextField(
                controller: apiKeyController,
                obscureText: true,
                enableSuggestions: false,
                autocorrect: false,
                decoration: InputDecoration(
                  labelText: context.l10n
                      .settingsMessageValue1ApiKey((option.label).toString()),
                  hintText: configured
                      ? context.l10n.settingsEnterANewKeyToReplaceTheSaved
                      : context.l10n.settingsPasteAPersonalApiKey,
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
                      label: Text(configured
                          ? context.l10n.settingsReplaceKeyUse
                          : context.l10n.settingsSaveKeyUse),
                    ),
                  ),
                  if (configured) ...[
                    const SizedBox(width: 8),
                    IconButton(
                      onPressed: saving ? null : () => onDeleteKey(option.id),
                      icon: const Icon(Icons.delete_outline_rounded),
                      color: AppTheme.error,
                      tooltip: context.l10n.settingsMessageRemoveValue1Key2(
                          (option.label).toString()),
                    ),
                  ],
                ],
              ),
              if (configured && !activeThisProvider) ...[
                const SizedBox(height: 8),
                OutlinedButton(
                  onPressed: saving ? null : onUseConfigured,
                  child: Text(context.l10n.settingsMessageUseSavedValue1Key(
                      (option.label).toString())),
                ),
              ],
            ],
            ModelFallbackSetting(
              provider: option,
              enabled: modelFallbackEnabled,
              onChanged: saving ? null : onFallbackChanged,
            ),
            if (configured && activeThisProvider)
              OutlinedButton(
                onPressed: saving ? null : onUseConfigured,
                child: Text(context.l10n.settingsSavePersonalAiSettings),
              ),
            const SizedBox(height: 10),
            Text(
              context.l10n
                  .settingsNewProviderModelAndCredentialSelectionsRequireOne,
              style: TextStyle(
                color: AppTheme.textMuted,
                fontSize: 12,
                height: 1.35,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// Short brand phrase used in OAuth button labels for one provider.
String _oauthBrand(AiProviderOption option) => switch (option.id) {
      'codex' => 'OpenAI OAuth',
      'grok_oauth' => 'xAI Grok',
      _ => option.label,
    };

class _OAuthActions extends StatelessWidget {
  final String brand;
  final bool configured;
  final bool active;
  final bool saving;
  final VoidCallback onManage;
  final VoidCallback onUse;

  const _OAuthActions({
    required this.brand,
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
        label: Text(context.l10n
            .settingsMessageConnectPersonalValue1((brand).toString())),
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        OutlinedButton.icon(
          onPressed: onManage,
          icon: const Icon(Icons.manage_accounts_outlined, size: 18),
          label: Text(context.l10n
              .settingsMessageManagePersonalValue1((brand).toString())),
        ),
        if (!active) ...[
          const SizedBox(height: 8),
          ElevatedButton(
            onPressed: saving ? null : onUse,
            child: Text(context.l10n
                .settingsMessageUsePersonalValue1((brand).toString())),
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
        ? context.l10n.settingsNoProviderSelected
        : settings.providerLabel(shared.config.provider);
    final status = !shared.granted
        ? context.l10n.settingsNotIncluded
        : active
            ? context.l10n.settingsActive
            : selected && shared.configured
                ? context.l10n.settingsUnavailable
                : shared.configured
                    ? context.l10n.settingsReady
                    : context.l10n.settingsNeedsAdminSetup;

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
            eyebrow: context.l10n.settingsIncluded2,
            title: context.l10n.settingsProvidedByThisServer,
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
                ? context.l10n.settingsYourAdminHasNotIncludedSharedAiAccess
                : shared.configured
                    ? context
                        .l10n.settingsUseTheServerProviderWithoutAddingAPersonal
                    : context
                        .l10n.settingsYourAccountIsIncludedButTheServerProvider,
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
              label: Text(context.l10n.settingsUseIncludedAccess),
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
  final Widget? trailing;

  const _SourceHeader({
    required this.icon,
    required this.eyebrow,
    required this.title,
    required this.status,
    required this.active,
    this.trailing,
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
        if (trailing != null) ...[
          const SizedBox(width: 8),
          trailing!,
        ],
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
            Text(
              context.l10n.settingsCouldNotLoadAiAccessSettings,
              style: TextStyle(color: AppTheme.textSecondary),
            ),
            const SizedBox(height: 12),
            OutlinedButton(
                onPressed: onRetry, child: Text(context.l10n.settingsRetry)),
          ],
        ),
      ),
    );
  }
}

String _reasonCopy(BuildContext context, String reason) => switch (reason) {
      'shared_access_disabled' =>
        context.l10n.settingsAddAPersonalProviderOrAskYourAdmin,
      'shared_credential_missing' =>
        context.l10n.settingsIncludedAccessIsEnabledButTheServerProvider,
      'personal_credential_missing' =>
        context.l10n.settingsTheSelectedPersonalApiKeyIsMissing,
      'personal_codex_disconnected' =>
        context.l10n.settingsTheSelectedPersonalOpenaiOauthAccountNeedsTo,
      'shared_codex_disconnected' =>
        context.l10n.settingsIncludedOpenaiOauthNeedsTheServerAdminTo,
      'codex_unavailable' =>
        context.l10n.settingsOpenaiOauthIsConfiguredButItsCodexRuntime,
      'personal_grok_disconnected' =>
        context.l10n.settingsTheSelectedPersonalXaiGrokAccountNeedsTo,
      'shared_grok_disconnected' =>
        context.l10n.settingsIncludedXaiGrokNeedsTheServerAdminTo,
      'grok_unavailable' =>
        context.l10n.settingsXaiGrokOauthIsUnavailableOnThisServer,
      'storage_error' =>
        context.l10n.settingsCantinarrCouldNotReadTheSavedAiCredentials,
      _ => context.l10n.settingsChooseAPersonalProviderOrUseIncludedAccess,
    };

String _friendlyError(Object error, String fallback) {
  if (error is DioException) {
    final data = error.response?.data;
    if (data is Map && data['error'] is String) {
      return data['error'] as String;
    }
  }
  final match = RegExp(r'"error":"([^"]+)"').firstMatch(error.toString());
  return match?.group(1) ?? fallback;
}
