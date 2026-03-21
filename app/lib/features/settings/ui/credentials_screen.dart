import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
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
  Map<String, bool>? _status;
  bool _isLoading = true;
  String? _error;

  final _tmdbController = TextEditingController();
  final _anthropicController = TextEditingController();
  final _traktIdController = TextEditingController();
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
    if (_traktIdController.text.isNotEmpty) {
      creds['trakt_client_id'] = _traktIdController.text.trim();
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
            child: const Text('Remove',
                style: TextStyle(color: AppTheme.error)),
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
    _traktIdController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('API Credentials')),
      body: _isLoading
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
                          onPressed: _loadStatus, child: const Text('Retry')),
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
                    _CredentialSection(
                      title: 'TMDB',
                      description: 'Required for media discovery and search',
                      isConfigured:
                          _status?['tmdb_access_token'] ?? false,
                      controller: _tmdbController,
                      hint: 'TMDB access token',
                      onDelete: () => _deleteCredential(
                          'tmdb_access_token', 'TMDB'),
                    ),
                    const SizedBox(height: 20),
                    _CredentialSection(
                      title: 'Anthropic (AI)',
                      description: 'Enables the AI assistant',
                      isConfigured:
                          _status?['anthropic_key'] ?? false,
                      controller: _anthropicController,
                      hint: 'Anthropic API key',
                      onDelete: () => _deleteCredential(
                          'anthropic_key', 'Anthropic'),
                    ),
                    const SizedBox(height: 20),
                    _CredentialSection(
                      title: 'Trakt',
                      description: 'Enhances discovery with trending and popular lists',
                      isConfigured:
                          _status?['trakt_client_id'] ?? false,
                      controller: _traktIdController,
                      hint: 'Trakt client ID',
                      onDelete: () => _deleteCredential(
                          'trakt_client_id', 'Trakt'),
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
                ),
    );
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
              padding:
                  const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: isConfigured
                    ? AppTheme.available.withValues(alpha: 0.15)
                    : AppTheme.unavailable.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                isConfigured ? 'Configured' : 'Not set',
                style: TextStyle(
                  color: isConfigured
                      ? AppTheme.available
                      : AppTheme.unavailable,
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
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 13)),
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
