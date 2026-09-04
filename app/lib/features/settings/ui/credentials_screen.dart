import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../ai_assistant/data/codex_oauth_service.dart';
import '../../ai_assistant/data/grok_oauth_service.dart';
import '../../ai_assistant/data/ai_settings_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/credentials_service.dart';
import '../logic/outbound_proxy_provider.dart';
import '../settings_anchors.dart';
import 'credential_section.dart';

/// Admin screen for managing API credentials (write-only).
class CredentialsScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const CredentialsScreen({super.key, this.highlightId});

  @override
  ConsumerState<CredentialsScreen> createState() => _CredentialsScreenState();
}

class _CredentialsScreenState extends ConsumerState<CredentialsScreen> {
  late final CredentialsService _service;
  CredentialsStatus? _status;
  bool _isLoading = true;
  String? _error;

  static const _customModelValue = '__custom__';

  final _anthropicController = TextEditingController();
  final _openAIController = TextEditingController();
  final _geminiController = TextEditingController();
  final _grokController = TextEditingController();
  final _customModelController = TextEditingController();
  // Admin-pinned shared openai reasoning effort. Empty string means auto.
  String _openaiReasoningEffort = '';
  // The local provider's scoped endpoint pair.
  final _localBaseUrlController = TextEditingController();
  String _localReasoningEffort = '';
  bool _localUseProxy = false;
  final _localKeyController = TextEditingController();
  String _selectedProvider = 'anthropic';
  String _selectedModel = 'claude-opus-4-8';
  bool _healthCheckEnabled = true;
  bool _isSaving = false;
  bool _isTestingAI = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = CredentialsService(
        backendDio: ref.read(backendClientProvider),
      );
      _loadStatus();
    });
  }

  Future<void> _loadStatus() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final status = await _service.getStatus();
      setState(() {
        _status = status;
        _syncAISelection(status);
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _save() async {
    final creds = <String, String>{};
    var aiChanged = false;
    if (_anthropicController.text.isNotEmpty) {
      creds['anthropic_key'] = _anthropicController.text.trim();
      aiChanged = true;
    }
    if (_openAIController.text.isNotEmpty) {
      creds['openai_key'] = _openAIController.text.trim();
      aiChanged = true;
    }
    if (_geminiController.text.isNotEmpty) {
      creds['gemini_key'] = _geminiController.text.trim();
      aiChanged = true;
    }
    if (_grokController.text.isNotEmpty) {
      creds['grok_key'] = _grokController.text.trim();
      aiChanged = true;
    }
    if (_localKeyController.text.isNotEmpty) {
      creds['local_openai_key'] = _localKeyController.text.trim();
      aiChanged = true;
    }

    final selectedModel = _selectedModel == _customModelValue
        ? _customModelController.text.trim()
        : _selectedModel;
    if (_selectedModel == _customModelValue && selectedModel.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a custom model ID')),
      );
      return;
    }
    if (_status == null ||
        _selectedProvider != _status!.ai.provider ||
        selectedModel != _status!.ai.model) {
      creds['ai_provider'] = _selectedProvider;
      creds['ai_model'] = selectedModel;
      aiChanged = true;
    }
    // Only a visible control writes: hidden state must never silently
    // rewrite the server, and older servers reject the key as unknown.
    if (_showOpenAiReasoningEffort &&
        _openaiReasoningEffort != (_status?.ai.openaiReasoningEffort ?? '')) {
      creds['openai_reasoning_effort'] = _openaiReasoningEffort;
      aiChanged = true;
    }
    if (_localProviderSelected) {
      final localBaseUrl = _localBaseUrlController.text.trim();
      if (localBaseUrl.isEmpty) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Enter the server base URL')),
        );
        return;
      }
      if (localBaseUrl != (_status?.ai.localOpenaiBaseUrl ?? '')) {
        creds['local_openai_base_url'] = localBaseUrl;
        aiChanged = true;
      }
      if (_localReasoningEffort !=
          (_status?.ai.localOpenaiReasoningEffort ?? '')) {
        creds['local_openai_reasoning_effort'] = _localReasoningEffort;
        aiChanged = true;
      }
      if (_showLocalProxyOptIn &&
          _localUseProxy != (_status?.ai.localOpenaiUseProxy ?? false)) {
        creds['local_openai_use_proxy'] = _localUseProxy.toString();
        aiChanged = true;
      }
    }
    if (_status == null ||
        _healthCheckEnabled != _status!.ai.healthCheckEnabled) {
      creds['ai_health_check_enabled'] = _healthCheckEnabled.toString();
      if (_healthCheckEnabled) aiChanged = true;
    }

    if (creds.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('No changes to save')),
      );
      return;
    }

    setState(() {
      _isSaving = true;
      _isTestingAI = aiChanged;
    });
    try {
      await _service.update(creds);
      _anthropicController.clear();
      _openAIController.clear();
      _geminiController.clear();
      _grokController.clear();
      await _loadStatus();
      // Provider selection and scoped OAuth availability are separate live
      // server facts. Refresh both so the underlying Settings screen and the
      // assistant cannot retain the pre-save provider state.
      ref.invalidate(codexConnectionStatusProvider);
      ref.invalidate(adminCodexConnectionStatusProvider);
      ref.invalidate(grokConnectionStatusProvider);
      ref.invalidate(adminGrokConnectionStatusProvider);
      ref.invalidate(aiSettingsProvider);
      ref.read(authProvider.notifier).refreshConfig();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              aiChanged ? 'AI test passed. Settings saved.' : 'Settings saved',
            ),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_friendlySaveError(e))),
        );
      }
    } finally {
      if (mounted) {
        setState(() {
          _isSaving = false;
          _isTestingAI = false;
        });
      }
    }
  }

  Future<void> _deleteCredential(String key, String label,
      {String? message}) async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Remove $label?'),
        content: Text(message ?? 'This will disable the $label integration.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            child:
                const Text('Remove', style: TextStyle(color: AppTheme.error)),
          ),
        ],
      ),
    );
    if (confirm != true) return;

    try {
      await _service.delete(key);
      await _loadStatus();
      ref.invalidate(aiSettingsProvider);
      ref.read(authProvider.notifier).refreshConfig();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('$label credential removed')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to remove: $e')),
        );
      }
    }
  }

  @override
  void dispose() {
    _anthropicController.dispose();
    _openAIController.dispose();
    _geminiController.dispose();
    _grokController.dispose();
    _customModelController.dispose();
    _localBaseUrlController.dispose();
    _localKeyController.dispose();
    super.dispose();
  }

  void _syncAISelection(CredentialsStatus status) {
    _selectedProvider = status.ai.provider;
    _healthCheckEnabled = status.ai.healthCheckEnabled;
    final provider = _providerFor(_selectedProvider, status.ai.providers);
    final hasModel =
        provider?.models.any((model) => model.id == status.ai.model) ?? false;
    if (hasModel) {
      _selectedModel = status.ai.model;
      _customModelController.clear();
    } else {
      _selectedModel = _customModelValue;
      _customModelController.text = status.ai.model;
    }
    // Tracks server state, not the dropdown: switching providers keeps the
    // stored value visible when the admin comes back to OpenAI.
    _openaiReasoningEffort = status.ai.openaiReasoningEffort;
    _localBaseUrlController.text = status.ai.localOpenaiBaseUrl;
    _localReasoningEffort = status.ai.localOpenaiReasoningEffort;
    _localUseProxy = status.ai.localOpenaiUseProxy;
  }

  /// Effort is openai-only among hosted providers, capability-flagged so
  /// older servers (which reject the key) never show the control.
  bool get _showOpenAiReasoningEffort {
    final provider =
        _providerFor(_selectedProvider, _status?.ai.providers ?? const []);
    return provider?.id == 'openai' && provider!.supportsReasoningEffort;
  }

  /// The local provider carries its own scoped endpoint pair.
  bool get _localProviderSelected {
    final provider =
        _providerFor(_selectedProvider, _status?.ai.providers ?? const []);
    return provider?.id == 'local_openai';
  }

  /// The endpoint's transport class is the admin's to declare, so the control
  /// only appears on servers that accept the key.
  bool get _showLocalProxyOptIn {
    final provider =
        _providerFor(_selectedProvider, _status?.ai.providers ?? const []);
    return provider?.id == 'local_openai' && provider!.supportsProxyOptIn;
  }

  String _friendlySaveError(Object error) {
    if (error is DioException) {
      final data = error.response?.data;
      if (data is Map && data['error'] is String) {
        return data['error'] as String;
      }
    }
    final match =
        RegExp(r'"error"\s*:\s*"([^"]+)"').firstMatch(error.toString());
    return match?.group(1) ?? 'Failed to save settings.';
  }

  AiProviderOption? _providerFor(
    String id,
    List<AiProviderOption> providers,
  ) {
    for (final provider in providers) {
      if (provider.id == id) return provider;
    }
    return providers.isNotEmpty ? providers.first : null;
  }

  void _selectProvider(String providerId) {
    final provider =
        _providerFor(providerId, _status?.ai.providers ?? const []);
    setState(() {
      _selectedProvider = provider?.id ?? providerId;
      _selectedModel = provider?.models.isNotEmpty == true
          ? provider!.models.first.id
          : _customModelValue;
      _customModelController.clear();
    });
  }

  /// Wraps [child] with the settings-search highlight for [anchorId].
  Widget _anchor(String anchorId, Widget child) => SettingsHighlight(
        anchorId: anchorId,
        highlightId: widget.highlightId,
        child: child,
      );

  /// Opens a shared OAuth connection screen and refreshes every live server
  /// fact that its outcome can change.
  Future<void> _manageSharedOAuth(String route) async {
    await context.push(route);
    if (!mounted) return;
    ref.invalidate(adminCodexConnectionStatusProvider);
    ref.invalidate(adminGrokConnectionStatusProvider);
    ref.invalidate(aiSettingsProvider);
    await _loadStatus();
    await ref.read(authProvider.notifier).refreshConfig();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Providers & Credentials')),
      body: CenteredContent(
          child: _isLoading
              ? const Center(
                  child: CircularProgressIndicator(color: AppTheme.accent))
              : _error != null
                  ? Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(_error!,
                              style: const TextStyle(color: AppTheme.error)),
                          const SizedBox(height: 12),
                          ElevatedButton(
                              onPressed: _loadStatus,
                              child: const Text('Retry')),
                        ],
                      ),
                    )
                  : ListView(
                      // Build every child while a settings-search highlight
                      // needs to find its anchor (see SettingsHighlight).
                      cacheExtent:
                          SettingsHighlight.cacheExtentFor(widget.highlightId),
                      padding: const EdgeInsets.all(16),
                      children: [
                        const Text(
                          'Server credentials are write-only. Users you include '
                          'can use the selected AI provider; personal providers '
                          'always take priority.',
                          style: TextStyle(
                              color: AppTheme.textSecondary, fontSize: 13),
                        ),
                        const SizedBox(height: 24),
                        _anchor(
                          SettingsAnchors.credentialsAiModel,
                          _AISelectionSection(
                            providers: _status?.ai.providers ?? const [],
                            selectedProvider: _selectedProvider,
                            selectedModel: _selectedModel,
                            customModelValue: _customModelValue,
                            customModelController: _customModelController,
                            isSelectedProviderConfigured: _status?.isConfigured(
                                  _providerFor(
                                        _selectedProvider,
                                        _status?.ai.providers ?? const [],
                                      )?.credentialKey ??
                                      'anthropic_key',
                                ) ??
                                false,
                            selectedProviderAuthType: _providerFor(
                                  _selectedProvider,
                                  _status?.ai.providers ?? const [],
                                )?.authType ??
                                'api_key',
                            onProviderChanged: _selectProvider,
                            onModelChanged: (value) =>
                                setState(() => _selectedModel = value),
                          ),
                        ),
                        if (_showOpenAiReasoningEffort) ...[
                          const SizedBox(height: 12),
                          _anchor(
                            SettingsAnchors.credentialsOpenAiReasoningEffort,
                            DropdownButtonFormField<String>(
                              key: const ValueKey('openai-reasoning-effort'),
                              initialValue: _openaiReasoningEffort,
                              decoration: const InputDecoration(
                                labelText: 'OpenAI reasoning effort',
                                helperText:
                                    'How much the model thinks before '
                                    'answering. Auto keeps the model\'s own '
                                    'default; None is fastest on local '
                                    'models.',
                                helperMaxLines: 3,
                                isDense: true,
                              ),
                              items: const [
                                DropdownMenuItem(
                                    value: '', child: Text('Auto')),
                                DropdownMenuItem(
                                    value: 'none', child: Text('None')),
                                DropdownMenuItem(
                                    value: 'minimal', child: Text('Minimal')),
                                DropdownMenuItem(
                                    value: 'low', child: Text('Low')),
                                DropdownMenuItem(
                                    value: 'medium', child: Text('Medium')),
                                DropdownMenuItem(
                                    value: 'high', child: Text('High')),
                              ],
                              onChanged: (value) => setState(
                                  () => _openaiReasoningEffort = value ?? ''),
                            ),
                          ),
                        ],
                        if (_localProviderSelected) ...[
                          const SizedBox(height: 12),
                          _anchor(
                            SettingsAnchors.credentialsOpenAiBaseUrl,
                            TextField(
                              key: const ValueKey('local-openai-base-url'),
                              controller: _localBaseUrlController,
                              decoration: const InputDecoration(
                                labelText: 'Server base URL',
                                hintText: 'http://llm-host:11434/v1',
                                helperText:
                                    'Required. Your OpenAI-compatible server '
                                    '(llama.cpp, vLLM, Ollama), reached from '
                                    'the Cantinarr server, not from this '
                                    'device.',
                                helperMaxLines: 3,
                                isDense: true,
                              ),
                              keyboardType: TextInputType.url,
                            ),
                          ),
                          const SizedBox(height: 12),
                          _anchor(
                            SettingsAnchors.credentialsOpenAiReasoningEffort,
                            DropdownButtonFormField<String>(
                              key: const ValueKey(
                                  'local-openai-reasoning-effort'),
                              initialValue: _localReasoningEffort,
                              decoration: const InputDecoration(
                                labelText: 'Reasoning effort',
                                helperText:
                                    'How much the model thinks before '
                                    'answering. None is fastest on local '
                                    'models.',
                                helperMaxLines: 3,
                                isDense: true,
                              ),
                              items: const [
                                DropdownMenuItem(
                                    value: '', child: Text('Auto')),
                                DropdownMenuItem(
                                    value: 'none', child: Text('None')),
                                DropdownMenuItem(
                                    value: 'minimal', child: Text('Minimal')),
                                DropdownMenuItem(
                                    value: 'low', child: Text('Low')),
                                DropdownMenuItem(
                                    value: 'medium', child: Text('Medium')),
                                DropdownMenuItem(
                                    value: 'high', child: Text('High')),
                              ],
                              onChanged: (value) => setState(
                                  () => _localReasoningEffort = value ?? ''),
                            ),
                          ),
                          if (_showLocalProxyOptIn) ...[
                            const SizedBox(height: 12),
                            _LocalProxyOptInSection(
                              enabled: _localUseProxy,
                              proxyConfigured: ref
                                      .watch(outboundProxyProvider)
                                      ?.url
                                      .isNotEmpty ??
                                  false,
                              onChanged: _isSaving
                                  ? null
                                  : (value) =>
                                      setState(() => _localUseProxy = value),
                            ),
                          ],
                        ],
                        const SizedBox(height: 14),
                        _anchor(
                          SettingsAnchors.credentialsHealthCheck,
                          _AIHealthCheckSection(
                            enabled: _healthCheckEnabled,
                            intervalHours:
                                _status?.ai.healthCheckIntervalHours ?? 24,
                            lastCheckedAt: _status?.ai.healthLastCheckedAt,
                            onChanged: _isSaving
                                ? null
                                : (value) => setState(
                                      () => _healthCheckEnabled = value,
                                    ),
                          ),
                        ),
                        const SizedBox(height: 14),
                        if (_selectedProvider == 'codex')
                          _SharedCodexPanel(
                            status: ref.watch(
                              adminCodexConnectionStatusProvider,
                            ),
                            onManage: () => _manageSharedOAuth(
                              '/settings/credentials/chatgpt',
                            ),
                          )
                        else if (_selectedProvider == 'grok_oauth')
                          _SharedGrokPanel(
                            status: ref.watch(
                              adminGrokConnectionStatusProvider,
                            ),
                            onManage: () => _manageSharedOAuth(
                              '/settings/credentials/grok',
                            ),
                          )
                        else
                          _SharedApiCostNotice(
                            provider: _providerFor(
                                  _selectedProvider,
                                  _status?.ai.providers ?? const [],
                                )?.label ??
                                _selectedProvider,
                            selfHosted: _localProviderSelected,
                          ),
                        const SizedBox(height: 24),
                        _anchor(
                          SettingsAnchors.credentialsAnthropic,
                          CredentialSection(
                            title: 'Anthropic (AI)',
                            description:
                                'Shared Claude API key for included AI usage',
                            isConfigured:
                                _status?.isConfigured('anthropic_key') ?? false,
                            controller: _anthropicController,
                            hint: 'Anthropic API key',
                            onDelete: () =>
                                _deleteCredential('anthropic_key', 'Anthropic'),
                          ),
                        ),
                        if (_status?.ai.providers.isNotEmpty ?? false) ...[
                          const SizedBox(height: 20),
                          _anchor(
                            SettingsAnchors.credentialsOpenAi,
                            CredentialSection(
                              title: 'OpenAI (AI)',
                              description:
                                  'Shared OpenAI API key for included AI usage',
                              isConfigured:
                                  _status?.isConfigured('openai_key') ?? false,
                              controller: _openAIController,
                              hint: 'OpenAI API key',
                              onDelete: () =>
                                  _deleteCredential('openai_key', 'OpenAI'),
                            ),
                          ),
                          const SizedBox(height: 20),
                          _anchor(
                            SettingsAnchors.credentialsGemini,
                            CredentialSection(
                              title: 'Google Gemini (AI)',
                              description:
                                  'Shared Gemini API key for included AI usage',
                              isConfigured:
                                  _status?.isConfigured('gemini_key') ?? false,
                              controller: _geminiController,
                              hint: 'Gemini API key',
                              onDelete: () => _deleteCredential(
                                  'gemini_key', 'Google Gemini'),
                            ),
                          ),
                          const SizedBox(height: 20),
                          _anchor(
                            SettingsAnchors.credentialsGrok,
                            CredentialSection(
                              title: 'xAI Grok (AI)',
                              description:
                                  'Shared xAI API key for included AI usage',
                              isConfigured:
                                  _status?.isConfigured('grok_key') ?? false,
                              controller: _grokController,
                              hint: 'xAI API key',
                              onDelete: () =>
                                  _deleteCredential('grok_key', 'xAI Grok'),
                            ),
                          ),
                          const SizedBox(height: 20),
                          CredentialSection(
                            title: 'Local server (AI)',
                            description:
                                'Optional key or proxy token for the local '
                                'OpenAI-compatible server; most need none',
                            isConfigured:
                                _status?.isConfigured('local_openai_key') ??
                                    false,
                            controller: _localKeyController,
                            hint: 'Optional API key',
                            onDelete: () => _deleteCredential(
                                'local_openai_key', 'Local server'),
                          ),
                        ],
                        const SizedBox(height: 32),
                        SizedBox(
                          width: double.infinity,
                          child: ElevatedButton(
                            onPressed: _isSaving ? null : _save,
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                if (_isSaving) ...[
                                  const SizedBox(
                                    width: 18,
                                    height: 18,
                                    child: CircularProgressIndicator(
                                      strokeWidth: 2,
                                    ),
                                  ),
                                  const SizedBox(width: 9),
                                ],
                                Text(
                                  _isTestingAI ? 'Testing & saving…' : 'Save',
                                ),
                              ],
                            ),
                          ),
                        ),
                      ],
                    )),
    );
  }
}

class _AISelectionSection extends StatelessWidget {
  final List<AiProviderOption> providers;
  final String selectedProvider;
  final String selectedModel;
  final String customModelValue;
  final TextEditingController customModelController;
  final bool isSelectedProviderConfigured;
  final String selectedProviderAuthType;
  final ValueChanged<String> onProviderChanged;
  final ValueChanged<String> onModelChanged;

  const _AISelectionSection({
    required this.providers,
    required this.selectedProvider,
    required this.selectedModel,
    required this.customModelValue,
    required this.customModelController,
    required this.isSelectedProviderConfigured,
    required this.selectedProviderAuthType,
    required this.onProviderChanged,
    required this.onModelChanged,
  });

  @override
  Widget build(BuildContext context) {
    if (providers.isEmpty) {
      return const Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'AI Model',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 16,
              fontWeight: FontWeight.w600,
            ),
          ),
          SizedBox(height: 4),
          Text(
            'Update the server container to configure OpenAI, Gemini, and AI model selection. This server only supports the legacy Anthropic AI key.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ],
      );
    }

    final provider = _currentProvider;
    final models = provider?.models ?? const <AiModelOption>[];
    final providerValue = provider?.id ?? selectedProvider;
    final usesOAuth = selectedProviderAuthType != 'api_key';
    final modelIds = models.map((model) => model.id).toSet();
    final modelValue =
        modelIds.contains(selectedModel) ? selectedModel : customModelValue;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            const Expanded(
              child: Text(
                'AI Model',
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: usesOAuth
                    ? AppTheme.signal.withValues(alpha: 0.15)
                    : isSelectedProviderConfigured
                        ? AppTheme.available.withValues(alpha: 0.15)
                        : AppTheme.unavailable.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                usesOAuth
                    ? 'Shared account'
                    : isSelectedProviderConfigured
                        ? 'Key set'
                        : 'Key missing',
                style: TextStyle(
                  color: usesOAuth
                      ? AppTheme.signal
                      : isSelectedProviderConfigured
                          ? AppTheme.available
                          : AppTheme.unavailable,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 4),
        Text(
          usesOAuth
              ? (providerValue == 'grok_oauth'
                  ? 'Connect one server xAI Grok account for users with included access.'
                  : 'Connect one server OpenAI OAuth account for users with included access.')
              : 'Select the server provider and model for included access.',
          style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 13,
          ),
        ),
        const SizedBox(height: 12),
        DropdownButtonFormField<String>(
          key: ValueKey('ai-provider-$providerValue'),
          initialValue: providerValue,
          isExpanded: true,
          decoration: const InputDecoration(
            labelText: 'Provider',
            isDense: true,
          ),
          items: providers
              .map((provider) => DropdownMenuItem(
                    value: provider.id,
                    child: Text(provider.label),
                  ))
              .toList(),
          onChanged: (value) {
            if (value != null) onProviderChanged(value);
          },
        ),
        const SizedBox(height: 12),
        DropdownButtonFormField<String>(
          key: ValueKey('ai-model-$providerValue-$modelValue'),
          initialValue: modelValue,
          isExpanded: true,
          decoration: const InputDecoration(
            labelText: 'Model',
            isDense: true,
          ),
          items: [
            ...models.map((model) => DropdownMenuItem(
                  value: model.id,
                  child: Text(model.label),
                )),
            DropdownMenuItem(
              value: customModelValue,
              child: const Text('Custom model ID'),
            ),
          ],
          onChanged: (value) {
            if (value != null) onModelChanged(value);
          },
        ),
        if (modelValue == customModelValue) ...[
          const SizedBox(height: 12),
          TextField(
            controller: customModelController,
            decoration: const InputDecoration(
              hintText: 'Provider model ID',
              isDense: true,
            ),
          ),
        ],
      ],
    );
  }

  AiProviderOption? get _currentProvider {
    for (final provider in providers) {
      if (provider.id == selectedProvider) return provider;
    }
    return providers.isNotEmpty ? providers.first : null;
  }
}

/// Declares whether the local endpoint is an internet host. Cantinarr never
/// guesses that from the URL: a split-horizon name or a Tailscale address
/// would be read wrong, and reading it wrong is silent either way.
class _LocalProxyOptInSection extends StatelessWidget {
  final bool enabled;
  final bool proxyConfigured;
  final ValueChanged<bool>? onChanged;

  const _LocalProxyOptInSection({
    required this.enabled,
    required this.proxyConfigured,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Material(
      color: AppTheme.surfaceVariant.withValues(alpha: 0.45),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        side: const BorderSide(color: AppTheme.border),
      ),
      clipBehavior: Clip.antiAlias,
      child: SwitchListTile.adaptive(
        key: const ValueKey('local-openai-use-proxy'),
        value: enabled,
        onChanged: onChanged,
        title: const Text(
          'Route through the outbound proxy',
          style: TextStyle(
            color: AppTheme.textPrimary,
            fontWeight: FontWeight.w600,
          ),
        ),
        subtitle: Text(
          'Turn this on when the endpoint is on the internet instead of your '
          'own network. ${proxyConfigured ? 'A server on your own network is always dialed directly.' : 'No outbound proxy is set yet, so add one under Settings > Outbound Proxy first.'}',
          style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 12,
            height: 1.38,
          ),
        ),
      ),
    );
  }
}

class _AIHealthCheckSection extends StatelessWidget {
  final bool enabled;
  final int intervalHours;
  final DateTime? lastCheckedAt;
  final ValueChanged<bool>? onChanged;

  const _AIHealthCheckSection({
    required this.enabled,
    required this.intervalHours,
    required this.lastCheckedAt,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final lastTest = lastCheckedAt == null
        ? ''
        : '\nLast tested ${_shortDate(lastCheckedAt!)}.';
    return Material(
      color: AppTheme.surfaceVariant.withValues(alpha: 0.45),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        side: const BorderSide(color: AppTheme.border),
      ),
      clipBehavior: Clip.antiAlias,
      child: SwitchListTile.adaptive(
        key: const ValueKey('ai-health-check-toggle'),
        value: enabled,
        onChanged: onChanged,
        title: const Text(
          'Daily shared-model test',
          style: TextStyle(
            color: AppTheme.textPrimary,
            fontWeight: FontWeight.w600,
          ),
        ),
        subtitle: Text(
          'Runs one small message and response turn every $intervalHours hours. '
          'A failure opens an admin issue. Turn this off to eliminate '
          'background AI usage; provider and model saves still always run '
          'their own test.$lastTest',
          style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 12,
            height: 1.38,
          ),
        ),
      ),
    );
  }

  String _shortDate(DateTime value) {
    final local = value.toLocal();
    final minute = local.minute.toString().padLeft(2, '0');
    return '${local.month}/${local.day}/${local.year} '
        '${local.hour}:$minute';
  }
}

class _SharedCodexPanel extends StatelessWidget {
  final AsyncValue<CodexConnectionStatus> status;
  final VoidCallback onManage;

  const _SharedCodexPanel({
    required this.status,
    required this.onManage,
  });

  @override
  Widget build(BuildContext context) {
    final connected = status.valueOrNull?.connected == true;
    return AppPanel(
      accentColor: AppTheme.warning,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(Icons.warning_amber_rounded, color: AppTheme.warning),
              SizedBox(width: 11),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Shared OpenAI OAuth allowance',
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    SizedBox(height: 5),
                    Text(
                      'Prompts and tool context from every enabled user use '
                      'one ChatGPT account and one Codex meter. Activity is '
                      'attributable to that account, and any subscription or '
                      'usage costs remain with it. ChatGPT accounts are '
                      'intended for one person; enable only people or devices '
                      'you control.',
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                        height: 1.42,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 14),
          OutlinedButton.icon(
            onPressed: status.isLoading ? null : onManage,
            icon: Icon(
              connected
                  ? Icons.manage_accounts_outlined
                  : Icons.open_in_browser_rounded,
              size: 18,
            ),
            label: Text(
              connected
                  ? 'Manage shared OpenAI OAuth'
                  : status.hasError
                      ? 'Retry shared OpenAI OAuth status'
                      : 'Connect shared OpenAI OAuth',
            ),
          ),
        ],
      ),
    );
  }
}

class _SharedGrokPanel extends StatelessWidget {
  final AsyncValue<GrokConnectionStatus> status;
  final VoidCallback onManage;

  const _SharedGrokPanel({
    required this.status,
    required this.onManage,
  });

  @override
  Widget build(BuildContext context) {
    final connected = status.valueOrNull?.connected == true;
    return AppPanel(
      accentColor: AppTheme.warning,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(Icons.warning_amber_rounded, color: AppTheme.warning),
              SizedBox(width: 11),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Shared xAI Grok allowance',
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    SizedBox(height: 5),
                    Text(
                      'Prompts and tool context from every enabled user use '
                      'one xAI account and its Grok subscription allowance. '
                      'Activity is attributable to that account, and any '
                      'subscription or usage costs remain with it. xAI '
                      'accounts are intended for one person; enable only '
                      'people or devices you control.',
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                        height: 1.42,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 14),
          OutlinedButton.icon(
            onPressed: status.isLoading ? null : onManage,
            icon: Icon(
              connected
                  ? Icons.manage_accounts_outlined
                  : Icons.open_in_browser_rounded,
              size: 18,
            ),
            label: Text(
              connected
                  ? 'Manage shared xAI Grok'
                  : status.hasError
                      ? 'Retry shared xAI Grok status'
                      : 'Connect shared xAI Grok',
            ),
          ),
        ],
      ),
    );
  }
}

class _SharedApiCostNotice extends StatelessWidget {
  final String provider;

  /// A configured openai base URL means included requests go to the admin's
  /// own endpoint: claiming paid-quota charges there would be false.
  final bool selfHosted;

  const _SharedApiCostNotice({required this.provider, this.selfHosted = false});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(13),
      decoration: BoxDecoration(
        color: AppTheme.signal.withValues(alpha: 0.07),
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        border: Border.all(color: AppTheme.signal.withValues(alpha: 0.2)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.payments_outlined, color: AppTheme.signal, size: 20),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              selfHosted
                  ? 'Included requests go to the configured server '
                      'endpoint. Costs depend on that server, not on a '
                      'hosted provider.'
                  : 'Included requests use the server $provider key. Usage '
                      'counts against its paid quota and may create provider '
                      'charges.',
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
