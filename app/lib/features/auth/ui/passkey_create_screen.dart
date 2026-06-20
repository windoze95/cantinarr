import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/theme/app_theme.dart';
import '../data/passkey_service.dart';
import '../logic/auth_provider.dart';

class PasskeyCreateScreen extends ConsumerStatefulWidget {
  const PasskeyCreateScreen({super.key});

  @override
  ConsumerState<PasskeyCreateScreen> createState() =>
      _PasskeyCreateScreenState();
}

class _PasskeyCreateScreenState extends ConsumerState<PasskeyCreateScreen> {
  final _nameController = TextEditingController(text: 'Passkey');
  bool _isCreating = false;
  bool? _passkeyAvailable;
  bool _showBrowserFallback = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadPasskeyAvailability();
  }

  Future<void> _loadPasskeyAvailability() async {
    final available = await PasskeyService.isAvailableAsync();
    if (!mounted) return;
    setState(() {
      _passkeyAvailable = available;
      _showBrowserFallback = !available;
    });
  }

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  Future<void> _create() async {
    final name = _nameController.text.trim();
    if (name.isEmpty || _isCreating) return;

    setState(() {
      _isCreating = true;
      _error = null;
      _showBrowserFallback = false;
    });

    try {
      await ref.read(authProvider.notifier).registerPasskey(name);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Passkey created')),
      );
      context.pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isCreating = false;
        _error = _passkeyErrorMessage(e);
        _showBrowserFallback = true;
      });
    }
  }

  Future<void> _openBrowser() async {
    setState(() {
      _isCreating = true;
      _error = null;
    });
    try {
      final link =
          await ref.read(authProvider.notifier).createPasskeySetupLink();
      await launchUrl(Uri.parse(link), mode: LaunchMode.externalApplication);
      if (!mounted) return;
      setState(() {
        _isCreating = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _isCreating = false;
        _error = 'Could not open browser setup';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final checkingAvailability = _passkeyAvailable == null;
    final available = _passkeyAvailable ?? false;

    return Scaffold(
      appBar: AppBar(title: const Text('Create Passkey')),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(20),
          children: [
            const Icon(Icons.fingerprint, size: 56, color: AppTheme.accent),
            const SizedBox(height: 24),
            TextField(
              controller: _nameController,
              enabled: available && !_isCreating && !checkingAvailability,
              decoration: const InputDecoration(
                labelText: 'Name',
                hintText: 'Phone, laptop, browser',
                prefixIcon: Icon(Icons.label_outline),
              ),
              textCapitalization: TextCapitalization.words,
              autofocus: available && !checkingAvailability,
              onSubmitted: (_) => _create(),
            ),
            if (_error != null) ...[
              const SizedBox(height: 16),
              Text(_error!, style: const TextStyle(color: AppTheme.error)),
            ],
            const SizedBox(height: 24),
            FilledButton.icon(
              onPressed: available && !_isCreating && !checkingAvailability
                  ? _create
                  : null,
              icon: _isCreating || checkingAvailability
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.fingerprint),
              label: Text(checkingAvailability
                  ? 'Checking...'
                  : _isCreating
                      ? 'Creating...'
                      : 'Create Passkey'),
            ),
            if (_showBrowserFallback) ...[
              const SizedBox(height: 16),
              Text(
                available
                    ? 'Native passkey creation is not available for this server yet.'
                    : 'Passkeys are not available in this app on this device.',
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: _isCreating ? null : _openBrowser,
                icon: const Icon(Icons.open_in_browser),
                label: const Text('Open Browser'),
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _passkeyErrorMessage(Object e) {
    final message = e.toString().replaceFirst('Exception: ', '');
    if (message.contains('passkey') ||
        message.contains('Passkey') ||
        message.contains('credential provider') ||
        message.contains('Google account')) {
      return message;
    }
    return 'Could not create passkey';
  }
}
