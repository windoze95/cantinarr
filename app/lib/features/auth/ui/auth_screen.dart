import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../data/passkey_service.dart';
import '../data/server_status.dart';
import '../logic/auth_provider.dart';

/// Unified auth screen: checks server status, shows setup wizard or login.
class AuthScreen extends ConsumerStatefulWidget {
  const AuthScreen({super.key});

  @override
  ConsumerState<AuthScreen> createState() => _AuthScreenState();
}

class _AuthScreenState extends ConsumerState<AuthScreen> {
  final _serverUrlController = TextEditingController();
  final _linkController = TextEditingController();

  _AuthView _view = _AuthView.serverUrl;
  ServerStatus? _serverStatus;
  bool _isCheckingServer = false;
  String? _serverError;

  @override
  void initState() {
    super.initState();
    // On web, auto-detect the server URL from the current page origin
    if (kIsWeb) {
      _checkCurrentOrigin();
    }
  }

  void _checkCurrentOrigin() {
    // On web builds served from the Cantinarr server, auto-detect
    // We use a post-frame callback so the widget is fully built
    WidgetsBinding.instance.addPostFrameCallback((_) {
      // The server URL is the origin where the web app was loaded from
      final origin = Uri.base.origin;
      if (origin.isNotEmpty && origin != 'null') {
        _serverUrlController.text = origin;
        _checkServer();
      }
    });
  }

  @override
  void dispose() {
    _serverUrlController.dispose();
    _linkController.dispose();
    super.dispose();
  }

  Future<void> _checkServer() async {
    final serverUrl = _serverUrlController.text.trim();
    if (serverUrl.isEmpty) return;

    setState(() {
      _isCheckingServer = true;
      _serverError = null;
    });

    try {
      final status = await ref.read(authProvider.notifier).checkServer(serverUrl);
      setState(() {
        _serverStatus = status;
        _isCheckingServer = false;
        _view = status.needsSetup ? _AuthView.setup : _AuthView.login;
      });
    } catch (e) {
      setState(() {
        _isCheckingServer = false;
        _serverError = _parseConnectionError(e);
      });
    }
  }

  String _parseConnectionError(Object e) {
    final msg = e.toString();
    if (msg.contains('Connection refused') || msg.contains('SocketException')) {
      return 'Could not connect to server';
    }
    if (msg.contains('404')) return 'Server not found at this URL';
    return 'Could not connect to server. Check the URL.';
  }

  void _showConnectLink() {
    setState(() => _view = _AuthView.connectLink);
  }

  void _backToServerUrl() {
    setState(() {
      _view = _AuthView.serverUrl;
      _serverStatus = null;
      _serverError = null;
    });
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final auth = authState.valueOrNull;
    final showPasskeyOffer = auth?.pendingPasskeyOffer == true &&
        auth?.isAuthenticated == true;

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
                Text(
                  showPasskeyOffer ? 'Secure your account' : _subtitle,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 15),
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 40),

                // Post-setup passkey offer takes priority
                if (showPasskeyOffer)
                  const _PasskeyOfferView()
                else
                  // View-specific content
                  switch (_view) {
                    _AuthView.serverUrl => _ServerUrlView(
                        controller: _serverUrlController,
                        isLoading: _isCheckingServer,
                        error: _serverError,
                        onCheck: _checkServer,
                        onConnectLink: _showConnectLink,
                      ),
                    _AuthView.setup => _SetupView(
                        serverUrl: _serverUrlController.text.trim(),
                        serverStatus: _serverStatus!,
                        onBack: _backToServerUrl,
                      ),
                    _AuthView.login => _LoginView(
                        serverUrl: _serverUrlController.text.trim(),
                        serverStatus: _serverStatus,
                        onBack: _backToServerUrl,
                        onConnectLink: _showConnectLink,
                      ),
                    _AuthView.connectLink => _ConnectLinkView(
                        controller: _linkController,
                        onBack: _backToServerUrl,
                      ),
                  },
              ],
            ),
          ),
        ),
      ),
    );
  }

  String get _subtitle => switch (_view) {
        _AuthView.serverUrl => 'Enter your server address to get started',
        _AuthView.setup => 'Create your admin account',
        _AuthView.login => 'Sign in to your server',
        _AuthView.connectLink => 'Paste your connection link',
      };
}

enum _AuthView { serverUrl, setup, login, connectLink }

// ─── Passkey Offer View (post-setup) ─────────────────────

class _PasskeyOfferView extends ConsumerStatefulWidget {
  const _PasskeyOfferView();

  @override
  ConsumerState<_PasskeyOfferView> createState() => _PasskeyOfferViewState();
}

class _PasskeyOfferViewState extends ConsumerState<_PasskeyOfferView> {
  bool _isRegistering = false;
  String? _error;

  void _skip() {
    ref.read(authProvider.notifier).dismissPasskeyOffer();
  }

  Future<void> _addPasskey() async {
    setState(() {
      _isRegistering = true;
      _error = null;
    });

    try {
      await ref.read(authProvider.notifier).registerPasskey('Passkey');
      if (mounted) {
        // Success — dismiss the offer and proceed to dashboard
        ref.read(authProvider.notifier).dismissPasskeyOffer();
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _isRegistering = false;
          _error = 'Could not register passkey. You can add one later in Settings.';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final passkeyAvailable = PasskeyService.isAvailable();

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 64,
          height: 64,
          decoration: BoxDecoration(
            color: AppTheme.accent.withValues(alpha: 0.15),
            shape: BoxShape.circle,
          ),
          child: const Icon(Icons.fingerprint,
              color: AppTheme.accent, size: 36),
        ),
        const SizedBox(height: 20),
        const Text(
          'Add a Passkey',
          style: TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 20,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 8),
        Text(
          passkeyAvailable
              ? 'Sign in faster next time with Face ID, fingerprint, or your device PIN.'
              : 'Passkeys require HTTPS. You can add one later in Settings after configuring a reverse proxy.',
          style: const TextStyle(
              color: AppTheme.textSecondary, fontSize: 14),
          textAlign: TextAlign.center,
        ),

        if (_error != null) ...[
          const SizedBox(height: 12),
          Text(
            _error!,
            style: const TextStyle(color: AppTheme.error, fontSize: 13),
            textAlign: TextAlign.center,
          ),
        ],

        const SizedBox(height: 28),

        if (passkeyAvailable)
          SizedBox(
            width: double.infinity,
            height: 50,
            child: ElevatedButton.icon(
              onPressed: _isRegistering ? null : _addPasskey,
              icon: _isRegistering
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: AppTheme.background,
                      ),
                    )
                  : const Icon(Icons.fingerprint),
              label: Text(_isRegistering ? 'Registering...' : 'Add Passkey'),
            ),
          ),

        const SizedBox(height: 12),

        SizedBox(
          width: double.infinity,
          height: 50,
          child: TextButton(
            onPressed: _isRegistering ? null : _skip,
            child: const Text(
              'Skip for now',
              style: TextStyle(
                  color: AppTheme.textSecondary, fontSize: 15),
            ),
          ),
        ),
      ],
    );
  }
}

// ─── Server URL View ─────────────────────────────────────

class _ServerUrlView extends StatelessWidget {
  final TextEditingController controller;
  final bool isLoading;
  final String? error;
  final VoidCallback onCheck;
  final VoidCallback onConnectLink;

  const _ServerUrlView({
    required this.controller,
    required this.isLoading,
    this.error,
    required this.onCheck,
    required this.onConnectLink,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        TextField(
          controller: controller,
          decoration: const InputDecoration(
            labelText: 'Server URL',
            hintText: 'https://cantinarr.example.com',
            prefixIcon: Icon(Icons.dns_outlined),
          ),
          keyboardType: TextInputType.url,
          textInputAction: TextInputAction.done,
          autocorrect: false,
          onSubmitted: (_) => onCheck(),
        ),

        if (error != null) ...[
          const SizedBox(height: 12),
          Text(
            error!,
            style: const TextStyle(color: AppTheme.error, fontSize: 13),
            textAlign: TextAlign.center,
          ),
        ],

        const SizedBox(height: 24),

        SizedBox(
          width: double.infinity,
          height: 50,
          child: ElevatedButton(
            onPressed: isLoading ? null : onCheck,
            child: isLoading
                ? const SizedBox(
                    width: 22,
                    height: 22,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: AppTheme.background,
                    ),
                  )
                : const Text('Continue'),
          ),
        ),

        const SizedBox(height: 24),

        TextButton(
          onPressed: onConnectLink,
          child: const Text(
            'Have a connection link?',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
      ],
    );
  }
}

// ─── Setup View ──────────────────────────────────────────

class _SetupView extends ConsumerStatefulWidget {
  final String serverUrl;
  final ServerStatus serverStatus;
  final VoidCallback onBack;

  const _SetupView({
    required this.serverUrl,
    required this.serverStatus,
    required this.onBack,
  });

  @override
  ConsumerState<_SetupView> createState() => _SetupViewState();
}

class _SetupViewState extends ConsumerState<_SetupView> {
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();
  final _confirmPasswordController = TextEditingController();
  bool _obscurePassword = true;
  bool _obscureConfirm = true;

  @override
  void dispose() {
    _usernameController.dispose();
    _passwordController.dispose();
    _confirmPasswordController.dispose();
    super.dispose();
  }

  void _setup() {
    final username = _usernameController.text.trim();
    final password = _passwordController.text;
    final confirm = _confirmPasswordController.text;

    if (username.isEmpty || password.isEmpty) return;
    if (password != confirm) {
      ref.read(authProvider.notifier).clearError();
      // Show error via a snackbar since this is a client-side validation
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Passwords do not match')),
      );
      return;
    }
    if (password.length < 8) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Password must be at least 8 characters')),
      );
      return;
    }

    ref.read(authProvider.notifier).setup(widget.serverUrl, username, password);
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final isLoading = authState.valueOrNull?.isLoading ?? false;
    final error = authState.valueOrNull?.error;

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        // Server indicator
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: AppTheme.surfaceVariant,
            borderRadius: BorderRadius.circular(8),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.dns_outlined,
                  size: 16, color: AppTheme.textSecondary),
              const SizedBox(width: 8),
              Flexible(
                child: Text(
                  widget.serverUrl,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 13),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: 24),

        TextField(
          controller: _usernameController,
          decoration: const InputDecoration(
            labelText: 'Username',
            prefixIcon: Icon(Icons.person_outline),
          ),
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
              icon: Icon(
                  _obscurePassword ? Icons.visibility_off : Icons.visibility),
              onPressed: () =>
                  setState(() => _obscurePassword = !_obscurePassword),
            ),
          ),
          obscureText: _obscurePassword,
          textInputAction: TextInputAction.next,
        ),
        const SizedBox(height: 16),

        TextField(
          controller: _confirmPasswordController,
          decoration: InputDecoration(
            labelText: 'Confirm Password',
            prefixIcon: const Icon(Icons.lock_outline),
            suffixIcon: IconButton(
              icon: Icon(
                  _obscureConfirm ? Icons.visibility_off : Icons.visibility),
              onPressed: () =>
                  setState(() => _obscureConfirm = !_obscureConfirm),
            ),
          ),
          obscureText: _obscureConfirm,
          textInputAction: TextInputAction.done,
          onSubmitted: (_) => _setup(),
        ),

        if (error != null) ...[
          const SizedBox(height: 12),
          Text(
            error,
            style: const TextStyle(color: AppTheme.error, fontSize: 13),
            textAlign: TextAlign.center,
          ),
        ],

        const SizedBox(height: 24),

        SizedBox(
          width: double.infinity,
          height: 50,
          child: ElevatedButton(
            onPressed: isLoading ? null : _setup,
            child: isLoading
                ? const SizedBox(
                    width: 22,
                    height: 22,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: AppTheme.background,
                    ),
                  )
                : const Text('Create Account'),
          ),
        ),

        const SizedBox(height: 16),

        TextButton(
          onPressed: widget.onBack,
          child: const Text(
            'Back',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
      ],
    );
  }
}

// ─── Login View ──────────────────────────────────────────

class _LoginView extends ConsumerStatefulWidget {
  final String serverUrl;
  final ServerStatus? serverStatus;
  final VoidCallback onBack;
  final VoidCallback onConnectLink;

  const _LoginView({
    required this.serverUrl,
    this.serverStatus,
    required this.onBack,
    required this.onConnectLink,
  });

  @override
  ConsumerState<_LoginView> createState() => _LoginViewState();
}

class _LoginViewState extends ConsumerState<_LoginView> {
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();
  bool _obscurePassword = true;

  bool get _showPasskey =>
      (widget.serverStatus?.webAuthnAvailable ?? false) &&
      PasskeyService.isAvailable();

  @override
  void dispose() {
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  void _login() {
    final username = _usernameController.text.trim();
    final password = _passwordController.text;
    if (username.isEmpty || password.isEmpty) return;
    ref.read(authProvider.notifier).login(widget.serverUrl, username, password);
  }

  void _loginWithPasskey() {
    ref.read(authProvider.notifier).loginWithPasskey(widget.serverUrl);
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final isLoading = authState.valueOrNull?.isLoading ?? false;
    final error = authState.valueOrNull?.error;

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        // Server indicator
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: AppTheme.surfaceVariant,
            borderRadius: BorderRadius.circular(8),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.dns_outlined,
                  size: 16, color: AppTheme.textSecondary),
              const SizedBox(width: 8),
              Flexible(
                child: Text(
                  widget.serverUrl,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 13),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: 24),

        // Passkey login button (shown when server and platform both support it)
        if (_showPasskey) ...[
          SizedBox(
            width: double.infinity,
            height: 50,
            child: OutlinedButton.icon(
              onPressed: isLoading ? null : _loginWithPasskey,
              icon: const Icon(Icons.fingerprint, size: 22),
              label: const Text('Sign in with Passkey'),
              style: OutlinedButton.styleFrom(
                side: const BorderSide(color: AppTheme.accent),
                foregroundColor: AppTheme.accent,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
                padding: const EdgeInsets.symmetric(vertical: 14),
                textStyle: const TextStyle(
                    fontWeight: FontWeight.w600, fontSize: 16),
              ),
            ),
          ),
          const SizedBox(height: 16),
          const Row(
            children: [
              Expanded(child: Divider(color: AppTheme.border)),
              Padding(
                padding: EdgeInsets.symmetric(horizontal: 12),
                child: Text('or use password',
                    style: TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13)),
              ),
              Expanded(child: Divider(color: AppTheme.border)),
            ],
          ),
          const SizedBox(height: 16),
        ],

        TextField(
          controller: _usernameController,
          decoration: const InputDecoration(
            labelText: 'Username',
            prefixIcon: Icon(Icons.person_outline),
          ),
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
              icon: Icon(
                  _obscurePassword ? Icons.visibility_off : Icons.visibility),
              onPressed: () =>
                  setState(() => _obscurePassword = !_obscurePassword),
            ),
          ),
          obscureText: _obscurePassword,
          textInputAction: TextInputAction.done,
          onSubmitted: (_) => _login(),
        ),

        if (error != null) ...[
          const SizedBox(height: 12),
          Text(
            error,
            style: const TextStyle(color: AppTheme.error, fontSize: 13),
            textAlign: TextAlign.center,
          ),
        ],

        const SizedBox(height: 24),

        SizedBox(
          width: double.infinity,
          height: 50,
          child: ElevatedButton(
            onPressed: isLoading ? null : _login,
            child: isLoading
                ? const SizedBox(
                    width: 22,
                    height: 22,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: AppTheme.background,
                    ),
                  )
                : const Text('Sign In'),
          ),
        ),

        const SizedBox(height: 16),

        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            TextButton(
              onPressed: widget.onBack,
              child: const Text(
                'Back',
                style:
                    TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              ),
            ),
            const Text('  |  ',
                style:
                    TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
            TextButton(
              onPressed: widget.onConnectLink,
              child: const Text(
                'Have a connection link?',
                style:
                    TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              ),
            ),
          ],
        ),
      ],
    );
  }
}

// ─── Connect Link View ───────────────────────────────────

class _ConnectLinkView extends ConsumerWidget {
  final TextEditingController controller;
  final VoidCallback onBack;

  const _ConnectLinkView({
    required this.controller,
    required this.onBack,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final authState = ref.watch(authProvider);
    final isLoading = authState.valueOrNull?.isLoading ?? false;
    final error = authState.valueOrNull?.error;

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        TextField(
          controller: controller,
          decoration: const InputDecoration(
            labelText: 'Connection Link',
            hintText: 'cantinarr://connect?...',
            prefixIcon: Icon(Icons.link),
          ),
          keyboardType: TextInputType.url,
          textInputAction: TextInputAction.done,
          autocorrect: false,
          onSubmitted: (_) {
            final link = controller.text.trim();
            if (link.isNotEmpty) {
              ref.read(authProvider.notifier).connectWithLink(link);
            }
          },
        ),

        if (error != null) ...[
          const SizedBox(height: 12),
          Text(
            error,
            style: const TextStyle(color: AppTheme.error, fontSize: 13),
            textAlign: TextAlign.center,
          ),
        ],

        const SizedBox(height: 24),

        SizedBox(
          width: double.infinity,
          height: 50,
          child: ElevatedButton(
            onPressed: isLoading
                ? null
                : () {
                    final link = controller.text.trim();
                    if (link.isNotEmpty) {
                      ref.read(authProvider.notifier).connectWithLink(link);
                    }
                  },
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

        const SizedBox(height: 16),

        TextButton(
          onPressed: onBack,
          child: const Text(
            'Back',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
      ],
    );
  }
}
