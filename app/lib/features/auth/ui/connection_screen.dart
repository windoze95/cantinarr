import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../logic/auth_provider.dart';

/// Connection screen — paste a connect link or use admin login.
class ConnectionScreen extends ConsumerStatefulWidget {
  const ConnectionScreen({super.key});

  @override
  ConsumerState<ConnectionScreen> createState() => _ConnectionScreenState();
}

class _ConnectionScreenState extends ConsumerState<ConnectionScreen> {
  final _linkController = TextEditingController();

  @override
  void dispose() {
    _linkController.dispose();
    super.dispose();
  }

  void _connect() {
    final link = _linkController.text.trim();
    if (link.isEmpty) return;
    ref.read(authProvider.notifier).connectWithLink(link);
  }

  void _showAdminLogin() {
    showDialog(
      context: context,
      builder: (context) => const _AdminLoginDialog(),
    );
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final isLoading = authState.valueOrNull?.isLoading ?? false;
    final error = authState.valueOrNull?.error;

    return Scaffold(
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.symmetric(horizontal: 32),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                // Logo
                Container(
                  width: 72,
                  height: 72,
                  decoration: BoxDecoration(
                    color: AppTheme.accent.withValues(alpha: 0.15),
                    borderRadius: BorderRadius.circular(20),
                  ),
                  child: const Icon(Icons.movie_filter,
                      color: AppTheme.accent, size: 40),
                ),
                const SizedBox(height: 16),
                const Text(
                  'Cantinarr',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 28,
                    fontWeight: FontWeight.bold,
                  ),
                ),
                const SizedBox(height: 4),
                const Text(
                  'Paste your connection link to get started',
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 15),
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 40),

                // Link input
                TextField(
                  controller: _linkController,
                  decoration: const InputDecoration(
                    labelText: 'Connection Link',
                    hintText: 'cantinarr://connect?...',
                    prefixIcon: Icon(Icons.link),
                  ),
                  keyboardType: TextInputType.url,
                  textInputAction: TextInputAction.done,
                  autocorrect: false,
                  onSubmitted: (_) => _connect(),
                ),
                const SizedBox(height: 8),

                // Error
                if (error != null) ...[
                  const SizedBox(height: 8),
                  Text(
                    error,
                    style:
                        const TextStyle(color: AppTheme.error, fontSize: 13),
                    textAlign: TextAlign.center,
                  ),
                ],

                const SizedBox(height: 24),

                // Connect button
                SizedBox(
                  width: double.infinity,
                  height: 50,
                  child: ElevatedButton(
                    onPressed: isLoading ? null : _connect,
                    child: isLoading
                        ? const SizedBox(
                            width: 22,
                            height: 22,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              color: AppTheme.background,
                            ),
                          )
                        : const Text('Connect'),
                  ),
                ),
                const SizedBox(height: 24),

                // Admin login
                TextButton(
                  onPressed: _showAdminLogin,
                  child: const Text(
                    'Admin Login',
                    style: TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// Dialog for admin password login (server bootstrap).
class _AdminLoginDialog extends ConsumerStatefulWidget {
  const _AdminLoginDialog();

  @override
  ConsumerState<_AdminLoginDialog> createState() => _AdminLoginDialogState();
}

class _AdminLoginDialogState extends ConsumerState<_AdminLoginDialog> {
  final _serverUrlController = TextEditingController();
  final _passwordController = TextEditingController();
  bool _obscurePassword = true;
  bool _isLoading = false;
  String? _error;

  @override
  void dispose() {
    _serverUrlController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _login() async {
    final serverUrl = _serverUrlController.text.trim();
    final password = _passwordController.text;

    if (serverUrl.isEmpty || password.isEmpty) return;

    setState(() {
      _isLoading = true;
      _error = null;
    });

    try {
      await ref
          .read(authProvider.notifier)
          .login(serverUrl, 'admin', password);
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      setState(() {
        _isLoading = false;
        _error = 'Login failed. Check URL and password.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    // Listen for auth state changes to close dialog on success
    ref.listen(authProvider, (prev, next) {
      final auth = next.valueOrNull;
      if (auth?.isAuthenticated == true && mounted) {
        Navigator.of(context).pop();
      }
      if (auth?.error != null && mounted) {
        setState(() {
          _isLoading = false;
          _error = auth!.error;
        });
      }
    });

    return AlertDialog(
      title: const Text('Admin Login'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          TextField(
            controller: _serverUrlController,
            decoration: const InputDecoration(
              labelText: 'Server URL',
              hintText: 'https://cantinarr.example.com',
              prefixIcon: Icon(Icons.dns_outlined),
            ),
            keyboardType: TextInputType.url,
            textInputAction: TextInputAction.next,
            autocorrect: false,
          ),
          const SizedBox(height: 16),
          TextField(
            controller: _passwordController,
            decoration: InputDecoration(
              labelText: 'Password',
              prefixIcon: const Icon(Icons.lock_outline),
              suffixIcon: IconButton(
                icon: Icon(_obscurePassword
                    ? Icons.visibility_off
                    : Icons.visibility),
                onPressed: () =>
                    setState(() => _obscurePassword = !_obscurePassword),
              ),
            ),
            obscureText: _obscurePassword,
            textInputAction: TextInputAction.done,
            onSubmitted: (_) => _login(),
          ),
          if (_error != null) ...[
            const SizedBox(height: 12),
            Text(
              _error!,
              style: const TextStyle(color: AppTheme.error, fontSize: 13),
            ),
          ],
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        ElevatedButton(
          onPressed: _isLoading ? null : _login,
          child: _isLoading
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Login'),
        ),
      ],
    );
  }
}
