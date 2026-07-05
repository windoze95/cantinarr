import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/credentials_service.dart';

/// Admin screen for managing API credentials (write-only).
class CredentialsScreen extends ConsumerStatefulWidget {
  const CredentialsScreen({super.key});

  @override
  ConsumerState<CredentialsScreen> createState() => _CredentialsScreenState();
}

class _CredentialsScreenState extends ConsumerState<CredentialsScreen> {
  late final CredentialsService _service;
  CredentialsStatus? _status;
  bool _isLoading = true;
  String? _error;

  static const _customModelValue = '__custom__';

  final _tmdbController = TextEditingController();
  final _anthropicController = TextEditingController();
  final _openAIController = TextEditingController();
  final _geminiController = TextEditingController();
  final _traktIdController = TextEditingController();
  final _customModelController = TextEditingController();
  String _selectedProvider = 'anthropic';
  String _selectedModel = 'claude-opus-4-8';
  bool _isSaving = false;

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
    if (_tmdbController.text.isNotEmpty) {
      creds['tmdb_access_token'] = _tmdbController.text.trim();
    }
    if (_anthropicController.text.isNotEmpty) {
      creds['anthropic_key'] = _anthropicController.text.trim();
    }
    if (_openAIController.text.isNotEmpty) {
      creds['openai_key'] = _openAIController.text.trim();
    }
    if (_geminiController.text.isNotEmpty) {
      creds['gemini_key'] = _geminiController.text.trim();
    }
    if (_traktIdController.text.isNotEmpty) {
      creds['trakt_client_id'] = _traktIdController.text.trim();
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
    }

    if (creds.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('No changes to save')),
      );
      return;
    }

    setState(() => _isSaving = true);
    try {
      await _service.update(creds);
      _tmdbController.clear();
      _anthropicController.clear();
      _openAIController.clear();
      _geminiController.clear();
      _traktIdController.clear();
      await _loadStatus();
      // Refresh config so service availability updates app-wide
      ref.read(authProvider.notifier).refreshConfig();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Credentials saved')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to save: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _isSaving = false);
    }
  }

  Future<void> _deleteCredential(String key, String label) async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Remove $label?'),
        content: Text('This will disable the $label integration.'),
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
    _tmdbController.dispose();
    _anthropicController.dispose();
    _openAIController.dispose();
    _geminiController.dispose();
    _traktIdController.dispose();
    _customModelController.dispose();
    super.dispose();
  }

  void _syncAISelection(CredentialsStatus status) {
    _selectedProvider = status.ai.provider;
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

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('API Credentials')),
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
                      padding: const EdgeInsets.all(16),
                      children: [
                        const Text(
                          'Credentials are write-only. Enter a new value to set or replace.',
                          style: TextStyle(
                              color: AppTheme.textSecondary, fontSize: 13),
                        ),
                        const SizedBox(height: 24),
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
                          onProviderChanged: _selectProvider,
                          onModelChanged: (value) =>
                              setState(() => _selectedModel = value),
                        ),
                        const SizedBox(height: 24),
                        _CredentialSection(
                          title: 'TMDB',
                          description:
                              'Required for media discovery and search',
                          isConfigured:
                              _status?.isConfigured('tmdb_access_token') ??
                                  false,
                          controller: _tmdbController,
                          hint: 'TMDB access token',
                          onDelete: () =>
                              _deleteCredential('tmdb_access_token', 'TMDB'),
                        ),
                        const SizedBox(height: 20),
                        _CredentialSection(
                          title: 'Anthropic (AI)',
                          description: 'Claude model provider',
                          isConfigured:
                              _status?.isConfigured('anthropic_key') ?? false,
                          controller: _anthropicController,
                          hint: 'Anthropic API key',
                          onDelete: () =>
                              _deleteCredential('anthropic_key', 'Anthropic'),
                        ),
                        if (_status?.ai.providers.isNotEmpty ?? false) ...[
                          const SizedBox(height: 20),
                          _CredentialSection(
                            title: 'OpenAI (AI)',
                            description: 'GPT model provider',
                            isConfigured:
                                _status?.isConfigured('openai_key') ?? false,
                            controller: _openAIController,
                            hint: 'OpenAI API key',
                            onDelete: () =>
                                _deleteCredential('openai_key', 'OpenAI'),
                          ),
                          const SizedBox(height: 20),
                          _CredentialSection(
                            title: 'Google Gemini (AI)',
                            description: 'Gemini model provider',
                            isConfigured:
                                _status?.isConfigured('gemini_key') ?? false,
                            controller: _geminiController,
                            hint: 'Gemini API key',
                            onDelete: () => _deleteCredential(
                                'gemini_key', 'Google Gemini'),
                          ),
                        ],
                        const SizedBox(height: 20),
                        _CredentialSection(
                          title: 'Trakt',
                          description:
                              'Enhances discovery with trending and popular lists',
                          isConfigured:
                              _status?.isConfigured('trakt_client_id') ?? false,
                          controller: _traktIdController,
                          hint: 'Trakt client ID',
                          onDelete: () =>
                              _deleteCredential('trakt_client_id', 'Trakt'),
                        ),
                        const SizedBox(height: 32),
                        SizedBox(
                          width: double.infinity,
                          child: ElevatedButton(
                            onPressed: _isSaving ? null : _save,
                            child: _isSaving
                                ? const SizedBox(
                                    width: 20,
                                    height: 20,
                                    child: CircularProgressIndicator(
                                        strokeWidth: 2),
                                  )
                                : const Text('Save'),
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
  final ValueChanged<String> onProviderChanged;
  final ValueChanged<String> onModelChanged;

  const _AISelectionSection({
    required this.providers,
    required this.selectedProvider,
    required this.selectedModel,
    required this.customModelValue,
    required this.customModelController,
    required this.isSelectedProviderConfigured,
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
                color: isSelectedProviderConfigured
                    ? AppTheme.available.withValues(alpha: 0.15)
                    : AppTheme.unavailable.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                isSelectedProviderConfigured ? 'Key set' : 'Key missing',
                style: TextStyle(
                  color: isSelectedProviderConfigured
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
        const Text(
          'Select which provider and model the assistant should use.',
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
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

class _CredentialSection extends StatelessWidget {
  final String title;
  final String description;
  final bool isConfigured;
  final TextEditingController controller;
  final String hint;
  final VoidCallback onDelete;

  const _CredentialSection({
    required this.title,
    required this.description,
    required this.isConfigured,
    required this.controller,
    required this.hint,
    required this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                title,
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: isConfigured
                    ? AppTheme.available.withValues(alpha: 0.15)
                    : AppTheme.unavailable.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                isConfigured ? 'Configured' : 'Not set',
                style: TextStyle(
                  color:
                      isConfigured ? AppTheme.available : AppTheme.unavailable,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            if (isConfigured) ...[
              const SizedBox(width: 8),
              GestureDetector(
                onTap: onDelete,
                child: const Icon(Icons.close,
                    size: 18, color: AppTheme.textSecondary),
              ),
            ],
          ],
        ),
        const SizedBox(height: 4),
        Text(description,
            style:
                const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        const SizedBox(height: 8),
        TextField(
          controller: controller,
          obscureText: true,
          decoration: InputDecoration(
            hintText: isConfigured ? 'Enter new value to replace' : hint,
            isDense: true,
          ),
        ),
      ],
    );
  }
}
