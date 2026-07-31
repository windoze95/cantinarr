import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../logic/auth_provider.dart';

/// Lets a signed-in user create or change their account password.
///
/// A password is the fallback sign-in method — and the way to authorize MCP
/// clients — on servers without HTTPS, where passkeys are unavailable.
class SetPasswordScreen extends ConsumerStatefulWidget {
  const SetPasswordScreen({super.key});

  @override
  ConsumerState<SetPasswordScreen> createState() => _SetPasswordScreenState();
}

class _SetPasswordScreenState extends ConsumerState<SetPasswordScreen> {
  static const _minLength = 8;

  final _passwordController = TextEditingController();
  final _confirmController = TextEditingController();
  bool _obscure = true;
  bool _isSaving = false;
  String? _error;

  @override
  void dispose() {
    _passwordController.dispose();
    _confirmController.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    if (_isSaving) return;

    final password = _passwordController.text;
    final confirm = _confirmController.text;

    if (password.length < _minLength) {
      setState(
          () => _error = 'Password must be at least $_minLength characters.');
      return;
    }
    if (password != confirm) {
      setState(() => _error = 'Passwords do not match.');
      return;
    }

    setState(() {
      _isSaving = true;
      _error = null;
    });

    try {
      await ref.read(authProvider.notifier).setPassword(password);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Password saved')),
      );
      context.pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isSaving = false;
        _error = _errorMessage(e);
      });
    }
  }

  String _errorMessage(Object e) {
    if (e is DioException) {
      final data = e.response?.data;
      if (data is Map<String, dynamic>) {
        final error = data['error'] as String?;
        if (error != null) return error;
      }
      if (e.type == DioExceptionType.connectionError ||
          e.type == DioExceptionType.connectionTimeout) {
        return 'Could not connect to server';
      }
    }
    return 'Could not save password. Please try again.';
  }

  @override
  Widget build(BuildContext context) {
    final user = ref.watch(authProvider).valueOrNull?.user;
    final isChange = user?.hasPassword == true;
    final account = user?.username ?? 'your account';

    return Scaffold(
      appBar:
          AppBar(title: Text(isChange ? 'Change Password' : 'Create Password')),
      body: CenteredContent(
          child: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(20),
          children: [
            const Icon(Icons.lock_outline, size: 56, color: AppTheme.accent),
            const SizedBox(height: 24),
            Text(
              isChange
                  ? 'Update the password for $account.'
                  : 'Add a password to $account.',
              style: const TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            const SizedBox(height: 8),
            const Text(
              'A password lets you sign in — and authorize MCP clients — on '
              'servers without HTTPS, where passkeys are unavailable.',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
            const SizedBox(height: 24),
            TextField(
              controller: _passwordController,
              enabled: !_isSaving,
              obscureText: _obscure,
              autofocus: true,
              decoration: InputDecoration(
                labelText: 'New password',
                prefixIcon: const Icon(Icons.lock_outline),
                suffixIcon: IconButton(
                  icon:
                      Icon(_obscure ? Icons.visibility : Icons.visibility_off),
                  onPressed: () => setState(() => _obscure = !_obscure),
                ),
              ),
              autofillHints: const [AutofillHints.newPassword],
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _confirmController,
              enabled: !_isSaving,
              obscureText: _obscure,
              decoration: const InputDecoration(
                labelText: 'Confirm password',
                prefixIcon: Icon(Icons.lock_outline),
              ),
              autofillHints: const [AutofillHints.newPassword],
              onSubmitted: (_) => _save(),
            ),
            if (_error != null) ...[
              const SizedBox(height: 16),
              Text(_error!, style: const TextStyle(color: AppTheme.error)),
            ],
            const SizedBox(height: 24),
            FilledButton.icon(
              onPressed: _isSaving ? null : _save,
              icon: _isSaving
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.check),
              label: Text(_isSaving ? 'Saving...' : 'Save Password'),
            ),
          ],
        ),
      )),
    );
  }
}
