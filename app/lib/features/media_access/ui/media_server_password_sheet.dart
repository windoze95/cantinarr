import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/media_access_service.dart';

/// How the password sheet ended: the account now exists, or the server said
/// one already did (the guide refreshes either way). Null means dismissed.
enum MediaServerPasswordSheetOutcome { created, accountExists }

/// Opens the create-account sheet for [server]. The password lives in the
/// sheet's controllers and travels once, in the create request; nothing
/// persists it.
Future<MediaServerPasswordSheetOutcome?> showMediaServerPasswordSheet(
  BuildContext context, {
  required MediaServerAccess server,
  required String username,
}) =>
    showAppSheet<MediaServerPasswordSheetOutcome>(
      context,
      builder: (_) => MediaServerPasswordSheet(
        server: server,
        username: username,
      ),
    );

/// Picks the password for the user's new account on a media server. Cantinarr
/// creates the account under the user's Cantinarr username and never keeps
/// the password, so the copy says both.
class MediaServerPasswordSheet extends ConsumerStatefulWidget {
  final MediaServerAccess server;
  final String username;

  const MediaServerPasswordSheet({
    super.key,
    required this.server,
    required this.username,
  });

  @override
  ConsumerState<MediaServerPasswordSheet> createState() =>
      _MediaServerPasswordSheetState();
}

class _MediaServerPasswordSheetState
    extends ConsumerState<MediaServerPasswordSheet> {
  static const _minLength = 8;

  final _passwordController = TextEditingController();
  final _confirmController = TextEditingController();
  bool _obscure = true;
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _passwordController.dispose();
    _confirmController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_busy) return;
    final password = _passwordController.text;
    if (password.length < _minLength) {
      setState(
          () => _error = 'Password must be at least $_minLength characters.');
      return;
    }
    if (password != _confirmController.text) {
      setState(() => _error = 'Passwords do not match.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await ref
          .read(mediaAccessServiceProvider)
          .createAccount(widget.server.instanceId, password);
      if (!mounted) return;
      Navigator.of(context).pop(MediaServerPasswordSheetOutcome.created);
    } on MediaAccessException catch (e) {
      if (!mounted) return;
      if (e.code == 'account_exists') {
        // Not an error to sit on: the guide re-reads and shows the account.
        Navigator.of(context)
            .pop(MediaServerPasswordSheetOutcome.accountExists);
        return;
      }
      setState(() {
        _busy = false;
        _error = _describe(e);
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = _genericFailure;
      });
    }
  }

  static const _genericFailure =
      "Couldn't create the account. Try again in a moment, or ask your admin.";

  /// Requester-voice copy per refusal. The server's fixed messages are
  /// safe, but the user needs to know what to do next: sign in with the
  /// account when the name is taken (it may well be theirs), the admin in
  /// every other case but a bad password.
  String _describe(MediaAccessException e) {
    final name = widget.server.name;
    if (e.isTransport) {
      return "Couldn't reach the server. Check your connection and try again.";
    }
    if (e.code == 'name_taken') {
      return 'The name ${widget.username} is already taken on $name. If that '
          "account is yours, go back and tap 'I already have an account' to "
          'sign in with its password. Otherwise ask your admin.';
    }
    switch (e.status) {
      case 400:
        // A password the server refused (too long) says so itself; every
        // other 400 is the username not being a valid account name there.
        if (e.code.isEmpty && e.message.toLowerCase().contains('password')) {
          return e.message;
        }
        return "$name doesn't accept your username as an account name. Ask "
            'your admin to link an account for you.';
      case 403:
        return 'That server is not available to you.';
      default:
        return _genericFailure;
    }
  }

  @override
  Widget build(BuildContext context) {
    final name = widget.server.name;
    final product = mediaServerTypeLabel(widget.server.serviceType);
    return PopScope(
      canPop: !_busy,
      child: AppSheet(
        padding: const EdgeInsets.fromLTRB(
          AppTheme.spaceXl,
          0,
          AppTheme.spaceXl,
          AppTheme.spaceXl,
        ),
        child: AutofillGroup(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Create your $product account',
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 20,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: AppTheme.spaceSm),
              Text(
                "You'll sign in to $name as ${widget.username} with the "
                "password you choose here. Cantinarr doesn't keep it, so "
                "pick one you'll remember.",
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 14,
                  height: 1.4,
                ),
              ),
              const SizedBox(height: AppTheme.spaceLg),
              TextField(
                controller: _passwordController,
                enabled: !_busy,
                obscureText: _obscure,
                autofocus: true,
                autocorrect: false,
                enableSuggestions: false,
                autofillHints: const [AutofillHints.newPassword],
                textInputAction: TextInputAction.next,
                decoration: InputDecoration(
                  labelText: 'Password',
                  prefixIcon: const Icon(Icons.lock_outline),
                  suffixIcon: IconButton(
                    tooltip: _obscure ? 'Show password' : 'Hide password',
                    icon: Icon(
                        _obscure ? Icons.visibility : Icons.visibility_off),
                    onPressed: () => setState(() => _obscure = !_obscure),
                  ),
                ),
              ),
              const SizedBox(height: AppTheme.spaceLg),
              TextField(
                controller: _confirmController,
                enabled: !_busy,
                obscureText: _obscure,
                autocorrect: false,
                enableSuggestions: false,
                autofillHints: const [AutofillHints.newPassword],
                textInputAction: TextInputAction.done,
                onSubmitted: (_) => _submit(),
                decoration: const InputDecoration(
                  labelText: 'Confirm password',
                  prefixIcon: Icon(Icons.lock_outline),
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: AppTheme.spaceMd),
                Text(
                  _error!,
                  style: const TextStyle(
                    color: AppTheme.error,
                    fontSize: 13,
                    height: 1.4,
                  ),
                ),
              ],
              const SizedBox(height: AppTheme.spaceXl),
              ElevatedButton(
                onPressed: _busy ? null : _submit,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppTheme.accent,
                  foregroundColor: Colors.black,
                  padding: const EdgeInsets.symmetric(vertical: 16),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(12),
                  ),
                ),
                child: _busy
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(
                            strokeWidth: 2, color: Colors.black),
                      )
                    : const Text('Create account'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
