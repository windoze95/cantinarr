import 'package:cantinarr/l10n/auth_messages.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../data/passkey_service.dart';
import '../logic/auth_provider.dart';

class PasskeyCreateScreen extends ConsumerStatefulWidget {
  const PasskeyCreateScreen({super.key});

  @override
  ConsumerState<PasskeyCreateScreen> createState() =>
      _PasskeyCreateScreenState();
}

class _PasskeyCreateScreenState extends ConsumerState<PasskeyCreateScreen> {
  final _nameController = TextEditingController();
  String? _defaultPasskeyName;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_defaultPasskeyName == null) {
      _defaultPasskeyName = context.l10n.authDefaultPasskeyName;
      _nameController.text = _defaultPasskeyName!;
    }
  }

  bool _isCreating = false;
  bool? _passkeyAvailable;
  bool _devicePasskeyAvailable = false;
  bool _serverNativePasskeyReady = false;
  bool _browserFallbackAvailable = false;
  bool _showBrowserFallback = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadPasskeyAvailability();
  }

  Future<void> _loadPasskeyAvailability() async {
    final deviceAvailable = await PasskeyService.isAvailableAsync();
    var serverNativeReady = false;
    var browserFallbackAvailable = false;

    final conn = ref.read(authProvider).valueOrNull?.connection;
    if (conn != null) {
      try {
        final status =
            (await ref.read(authProvider.notifier).checkServer(conn.serverUrl))
                .status;
        serverNativeReady =
            status.supportsPasskeyPlatform(PasskeyService.platformKind());
        browserFallbackAvailable = status.webAuthnAvailable;
      } catch (_) {
        browserFallbackAvailable = false;
      }
    }

    if (!mounted) return;
    final available = deviceAvailable && serverNativeReady;
    setState(() {
      _passkeyAvailable = available;
      _devicePasskeyAvailable = deviceAvailable;
      _serverNativePasskeyReady = serverNativeReady;
      _browserFallbackAvailable = browserFallbackAvailable;
      _showBrowserFallback = !available && browserFallbackAvailable;
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
        SnackBar(content: Text(context.l10n.authPasskeyCreated)),
      );
      _nameController.text = _defaultPasskeyName!;
      context.pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isCreating = false;
        _passkeyAvailable = false;
        _error = _passkeyErrorMessage(e);
        _showBrowserFallback = _browserFallbackAvailable;
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
        _error = context.l10n.authOpenBrowserSetupError;
      });
    }
  }

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _nameController.text != _defaultPasskeyName,
        isSaving: _isCreating,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) {
    final checkingAvailability = _passkeyAvailable == null;
    final available = _passkeyAvailable ?? false;

    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.authCreatePasskey)),
      body: CenteredContent(
          child: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(20),
          children: [
            const Icon(Icons.fingerprint, size: 56, color: AppTheme.accent),
            const SizedBox(height: 24),
            TextField(
              controller: _nameController,
              enabled: available && !_isCreating && !checkingAvailability,
              decoration: InputDecoration(
                labelText: context.l10n.authName,
                hintText: context.l10n.authPasskeyNameHint,
                prefixIcon: Icon(Icons.label_outline),
              ),
              textCapitalization: TextCapitalization.words,
              autofocus: available && !checkingAvailability,
              onSubmitted: (_) => _create(),
            ),
            if (_error != null) ...[
              const SizedBox(height: 16),
              Text(localizeAuthMessage(context, _error!),
                  style: const TextStyle(color: AppTheme.error)),
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
                  ? context.l10n.authCheckingDots
                  : _isCreating
                      ? context.l10n.authCreating
                      : context.l10n.authCreatePasskey),
            ),
            if (!checkingAvailability && !available) ...[
              const SizedBox(height: 16),
              Text(
                _nativeUnavailableMessage(),
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
              if (_showBrowserFallback) ...[
                const SizedBox(height: 12),
                OutlinedButton.icon(
                  onPressed: _isCreating ? null : _openBrowser,
                  icon: const Icon(Icons.open_in_browser),
                  label: Text(context.l10n.authOpenBrowser),
                ),
              ],
            ],
          ],
        ),
      )),
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
    return context.l10n.authCreatePasskeyError;
  }

  String _nativeUnavailableMessage() {
    if (!_devicePasskeyAvailable) {
      return context.l10n.authPasskeysUnavailable;
    }
    if (!_browserFallbackAvailable) {
      return context.l10n.authPasskeysNeedHttps;
    }
    if (!_serverNativePasskeyReady) {
      return context.l10n.authNativePasskeyUnconfigured;
    }
    return context.l10n.authNativePasskeyUnavailable;
  }
}
