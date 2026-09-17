import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../data/connection_input.dart';
import '../data/passkey_service.dart';
import '../data/server_status.dart';
import '../logic/auth_provider.dart';
import '../logic/saved_servers_provider.dart';
import 'saved_server_picker.dart';
import 'server_setup_help.dart';

/// Overridable so native widget tests can exercise hosted-web selection too.
final authPageOriginProvider = Provider<String?>((ref) {
  if (!kIsWeb) return null;
  final origin = Uri.base.origin;
  return origin.isEmpty || origin == 'null' ? null : origin;
});

/// Resolve device support once for the connection and sign-in views. The server
/// must independently advertise support for this same platform.
final authPasskeyPlatformProvider =
    FutureProvider.autoDispose<String?>((ref) async {
  return await PasskeyService.isAvailableAsync()
      ? PasskeyService.platformKind()
      : null;
});

/// Unified auth screen: checks server status, shows setup wizard or login.
class AuthScreen extends ConsumerStatefulWidget {
  const AuthScreen({super.key});

  @override
  ConsumerState<AuthScreen> createState() => _AuthScreenState();
}

class _AuthScreenState extends ConsumerState<AuthScreen> {
  final _serverUrlController = TextEditingController();

  _AuthView _view = _AuthView.serverUrl;
  ServerStatus? _serverStatus;
  bool _isCheckingServer = false;
  bool _backgroundCheck = false;
  bool _showSavedServers = false;
  int _selectionEpoch = 0;
  String? _serverError;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _selectInitialServer());
  }

  Future<void> _selectInitialServer() async {
    if (!mounted) return;
    final epoch = _selectionEpoch;
    // Restore/migrate first. A remembered shortcut must never replace a
    // restored session or an explicit sign-in already in progress.
    await ref.read(authProvider.future);
    if (!mounted || epoch != _selectionEpoch) return;
    List<SavedServer> servers;
    try {
      servers = await ref.read(savedServersProvider.future);
    } catch (_) {
      servers = const [];
    }
    if (!mounted || epoch != _selectionEpoch) return;
    final auth = ref.read(authProvider).valueOrNull;
    if (auth?.isAuthenticated == true || auth?.isLoading == true) return;
    final url = servers.isNotEmpty
        ? servers.first.url
        : ref.read(authPageOriginProvider);
    if (url != null) {
      _serverUrlController.text = url;
      // Hosted web can go directly to its server's login/setup. Native apps
      // stay on the shared entry field; this read only enables a passkey shortcut.
      await _checkServer(advance: ref.read(authPageOriginProvider) != null);
    }
  }

  @override
  void dispose() {
    _serverUrlController.dispose();
    super.dispose();
  }

  Future<void> _submitConnection() async {
    final auth = ref.read(authProvider);
    if (auth.isLoading ||
        auth.valueOrNull?.isLoading == true ||
        auth.valueOrNull?.isAuthenticated == true ||
        (_isCheckingServer && !_backgroundCheck)) {
      return;
    }
    ConnectionInput input;
    try {
      input = ConnectionInput.parse(_serverUrlController.text);
    } on FormatException catch (e) {
      setState(() {
        _cancelPendingSelection();
        _backgroundCheck = false;
        _serverError = e.message;
      });
      return;
    }
    ref.read(authProvider.notifier).clearError();
    if (!input.isLink) {
      await _checkServer();
      return;
    }
    setState(() {
      _cancelPendingSelection();
      _backgroundCheck = false;
      _serverStatus = null;
      _serverError = null;
    });
    await ref.read(authProvider.notifier).connectWithLink(input.value);
  }

  Future<void> _checkServer({bool advance = true}) async {
    final serverUrl = _serverUrlController.text.trim();
    if (serverUrl.isEmpty) return;
    final epoch = ++_selectionEpoch;

    setState(() {
      _isCheckingServer = true;
      _backgroundCheck = !advance;
      _serverStatus = null;
      _serverError = null;
    });

    try {
      final result =
          await ref.read(authProvider.notifier).checkServer(serverUrl);
      if (!_canFinishCheck(epoch)) return;
      // Reflect the URL that actually answered (scheme included) back into
      // the field — the setup/login views read it from here, so they reuse
      // exactly the scheme the probe settled on.
      _serverUrlController.text = result.serverUrl;
      setState(() {
        _serverStatus = result.status;
        _isCheckingServer = false;
        if (advance) {
          _view = result.status.needsSetup ? _AuthView.setup : _AuthView.login;
        }
      });
    } catch (e) {
      if (!_canFinishCheck(epoch)) return;
      setState(() {
        _isCheckingServer = false;
        _serverError = _parseConnectionError(e);
      });
    }
  }

  bool _canFinishCheck(int epoch) =>
      mounted && epoch == _selectionEpoch && _view == _AuthView.serverUrl;

  void _cancelPendingSelection() {
    _selectionEpoch++;
    _isCheckingServer = false;
  }

  void _addressChanged(String _) {
    setState(() {
      _cancelPendingSelection();
      _backgroundCheck = false;
      _serverStatus = null;
      _serverError = null;
    });
    ref.read(authProvider.notifier).clearError();
  }

  void _selectSavedServer(SavedServer server) {
    _serverUrlController.text = server.url;
    _checkServer();
  }

  Future<void> _forgetServer(SavedServer server) async {
    setState(_cancelPendingSelection);
    final epoch = _selectionEpoch;
    final previous = ref.read(savedServersProvider).valueOrNull ?? const [];
    final history = ref.read(savedServersProvider.notifier);
    try {
      await history.forget(server.url);
      if (!mounted) return;
      if (epoch == _selectionEpoch && _serverUrlController.text == server.url) {
        _backToServerUrl();
        _serverUrlController.clear();
      }
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('Forgot ${server.label}'),
        action: SnackBarAction(
          label: 'Undo',
          onPressed: () async {
            try {
              await history.undoForget(server, previous);
            } catch (_) {
              if (mounted) _showHistoryError();
            }
          },
        ),
      ));
    } catch (_) {
      if (mounted) _showHistoryError();
    }
  }

  void _showHistoryError() {
    ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
      content: Text('Could not save server shortcuts. Please try again.'),
    ));
  }

  String _parseConnectionError(Object e) {
    final msg = e.toString();
    if (msg.contains('Connection refused') || msg.contains('SocketException')) {
      return 'Could not reach your Cantinarr server. Check its address and connection.';
    }
    if (msg.contains('404')) return 'Cantinarr was not found at this address.';
    return 'Could not reach your Cantinarr server. Check its address and connection.';
  }

  void _backToServerUrl() {
    setState(() {
      _cancelPendingSelection();
      _view = _AuthView.serverUrl;
      _backgroundCheck = false;
      _serverStatus = null;
      _serverError = null;
    });
    ref.read(authProvider.notifier).clearError();
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    ref.listen(authProvider, (_, next) {
      final auth = next.valueOrNull;
      if (auth?.isLoading == true || auth?.isAuthenticated == true) {
        setState(_cancelPendingSelection);
      }
    });
    final savedServers = ref.watch(savedServersProvider);
    final auth = authState.valueOrNull;
    final showPasskeyOffer =
        auth?.pendingPasskeyOffer == true && auth?.isAuthenticated == true;
    final passkeyPlatform = ref.watch(authPasskeyPlatformProvider).valueOrNull;
    final passkeyAvailable = passkeyPlatform != null &&
        _serverStatus?.needsSetup == false &&
        _serverStatus?.ssoOnly == false &&
        _serverStatus!.supportsPasskeyPlatform(passkeyPlatform);
    final connectionEntry = _view == _AuthView.serverUrl && !showPasskeyOffer;

    final screen = Scaffold(
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: EdgeInsets.symmetric(
                horizontal: 22, vertical: connectionEntry ? 20 : 32),
            // Login card: full-width buttons would otherwise stretch the
            // column across a desktop window.
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 460),
              child: AppPanel(
                padding: connectionEntry
                    ? const EdgeInsets.all(22)
                    : const EdgeInsets.fromLTRB(28, 30, 28, 28),
                radius: AppTheme.radiusXLarge,
                accentColor: AppTheme.signal,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    // Logo
                    Container(
                      width: connectionEntry ? 60 : 78,
                      height: connectionEntry ? 60 : 78,
                      padding: const EdgeInsets.all(3),
                      decoration: BoxDecoration(
                        gradient: const LinearGradient(
                          begin: Alignment.topLeft,
                          end: Alignment.bottomRight,
                          colors: [AppTheme.accent, AppTheme.signal],
                        ),
                        borderRadius: BorderRadius.circular(22),
                        boxShadow: [
                          BoxShadow(
                            color: AppTheme.accent.withValues(alpha: 0.17),
                            blurRadius: 24,
                          ),
                        ],
                      ),
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(19),
                        child: Image.asset(
                          'assets/logo.png',
                          fit: BoxFit.cover,
                        ),
                      ),
                    ),
                    SizedBox(height: connectionEntry ? 12 : 18),
                    const FittedBox(
                      fit: BoxFit.scaleDown,
                      child: Text(
                        'CANTINARR',
                        style: TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 27,
                          fontWeight: FontWeight.w800,
                          letterSpacing: 2.2,
                        ),
                      ),
                    ),
                    const SizedBox(height: 12),
                    Text(
                      showPasskeyOffer ? 'Secure your account' : _subtitle,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 15,
                      ),
                      textAlign: TextAlign.center,
                    ),
                    SizedBox(height: connectionEntry ? 20 : 34),

                    // Post-setup passkey offer takes priority
                    if (showPasskeyOffer)
                      const _PasskeyOfferView()
                    else
                      // View-specific content
                      switch (_view) {
                        _AuthView.serverUrl => _ConnectionView(
                            controller: _serverUrlController,
                            isAuthenticating:
                                authState.isLoading || auth?.isLoading == true,
                            isLoading:
                                (_isCheckingServer && !_backgroundCheck) ||
                                    authState.isLoading || auth?.isLoading == true,
                            error: _serverError ?? auth?.error,
                            servers: savedServers.valueOrNull ?? const [],
                            historyUnavailable: savedServers.hasError,
                            showSavedServers: _showSavedServers,
                            onToggleSavedServers: () => setState(() =>
                                _showSavedServers = !_showSavedServers),
                            isCheckingSavedServer:
                                _isCheckingServer && _backgroundCheck,
                            retrySavedServer:
                                _backgroundCheck && _serverError != null
                                    ? () => _checkServer(advance: false)
                                    : null,
                            passkeyServer: passkeyAvailable
                                ? SavedServer(
                                    url: _serverUrlController.text,
                                    name: savedServers.valueOrNull
                                        ?.where((s) =>
                                            s.url == _serverUrlController.text)
                                        .firstOrNull
                                        ?.name)
                                : null,
                            onPasskey: () => ref
                                .read(authProvider.notifier)
                                .loginWithPasskey(_serverUrlController.text),
                            onAddressChanged: _addressChanged,
                            onSelectServer: _selectSavedServer,
                            onForgetServer: _forgetServer,
                            onConnect: _submitConnection,
                          ),
                        _AuthView.setup => _SetupView(
                            serverUrl: _serverUrlController.text.trim(),
                            serverStatus: _serverStatus!,
                            onBack: _backToServerUrl,
                          ),
                        _AuthView.login => _LoginView(
                            key: ValueKey(_serverUrlController.text.trim()),
                            serverUrl: _serverUrlController.text.trim(),
                            serverName: savedServers.valueOrNull
                                ?.where((s) =>
                                    s.url == _serverUrlController.text.trim())
                                .firstOrNull
                                ?.name,
                            serverStatus: _serverStatus,
                            onBack: _backToServerUrl,
                          ),
                      },
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
    return PopScope(
      canPop: _view == _AuthView.serverUrl || showPasskeyOffer,
      onPopInvokedWithResult: (didPop, result) {
        if (!didPop && auth?.isLoading != true) _backToServerUrl();
      },
      child: screen,
    );
  }

  String get _subtitle => switch (_view) {
        _AuthView.serverUrl => 'Connect to Cantinarr',
        _AuthView.setup => 'Create your admin account',
        _AuthView.login => 'Sign in to your server',
      };
}

enum _AuthView { serverUrl, setup, login }

// ─── Passkey Offer View (post-setup) ─────────────────────

class _PasskeyOfferView extends ConsumerStatefulWidget {
  const _PasskeyOfferView();

  @override
  ConsumerState<_PasskeyOfferView> createState() => _PasskeyOfferViewState();
}

class _PasskeyOfferViewState extends ConsumerState<_PasskeyOfferView> {
  bool _isRegistering = false;
  bool? _passkeyAvailable;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadPasskeyAvailability();
  }

  Future<void> _loadPasskeyAvailability() async {
    final available = await PasskeyService.isAvailableAsync();
    if (!mounted) return;
    setState(() => _passkeyAvailable = available);
  }

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
          _error = _passkeyErrorMessage(e);
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final passkeyAvailable = _passkeyAvailable ?? false;
    final checkingPasskeys = _passkeyAvailable == null;

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
          child:
              const Icon(Icons.fingerprint, color: AppTheme.accent, size: 36),
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
          checkingPasskeys
              ? 'Checking passkey support...'
              : passkeyAvailable
                  ? 'Sign in faster next time with Face ID, fingerprint, or your device PIN.'
                  : 'Passkeys require HTTPS. You can add one later in Settings after configuring a reverse proxy.',
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 14),
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
        if (checkingPasskeys)
          const SizedBox(
            height: 50,
            child: Center(child: CircularProgressIndicator()),
          )
        else if (passkeyAvailable)
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
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 15),
            ),
          ),
        ),
      ],
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
    return 'Could not register passkey. You can add one later in Settings.';
  }
}

// ─── Connection Entry ────────────────────────────────────

class _ConnectionView extends StatelessWidget {
  final TextEditingController controller;
  final bool isLoading;
  final bool isAuthenticating;
  final String? error;
  final List<SavedServer> servers;
  final bool historyUnavailable;
  final bool showSavedServers;
  final VoidCallback onToggleSavedServers;
  final bool isCheckingSavedServer;
  final VoidCallback? retrySavedServer;
  final SavedServer? passkeyServer;
  final VoidCallback onPasskey;
  final ValueChanged<String> onAddressChanged;
  final ValueChanged<SavedServer> onSelectServer;
  final ValueChanged<SavedServer> onForgetServer;
  final VoidCallback onConnect;

  const _ConnectionView({
    required this.controller,
    required this.isLoading,
    required this.isAuthenticating,
    this.error,
    required this.servers,
    required this.historyUnavailable,
    required this.showSavedServers,
    required this.onToggleSavedServers,
    required this.isCheckingSavedServer,
    required this.retrySavedServer,
    required this.passkeyServer,
    required this.onPasskey,
    required this.onAddressChanged,
    required this.onSelectServer,
    required this.onForgetServer,
    required this.onConnect,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          'This app connects to a Cantinarr server running on a computer or NAS. '
          'That server connects to your services, including Radarr, Sonarr, and Plex.',
          style: Theme.of(context).textTheme.bodyMedium,
        ),
        const SizedBox(height: 12),
        Text(
          'Ask your server admin for a connection link, or enter your '
          'Cantinarr server address below.',
          style: Theme.of(context).textTheme.bodyMedium,
        ),
        const SizedBox(height: 20),
        TextField(
          key: const ValueKey('connection-entry'),
          controller: controller,
          enabled: !isAuthenticating,
          decoration: const InputDecoration(
            labelText: 'Link or Cantinarr address',
            floatingLabelBehavior: FloatingLabelBehavior.always,
            hintText: 'http://192.168.1.10:8585',
            prefixIcon: Icon(Icons.link),
          ),
          keyboardType: TextInputType.url,
          textInputAction: TextInputAction.done,
          autocorrect: false,
          enableSuggestions: false,
          onChanged: onAddressChanged,
          onSubmitted: (_) {
            if (!isLoading) onConnect();
          },
        ),
        if (error != null) ...[
          const SizedBox(height: 12),
          Text(
            error!,
            style: const TextStyle(color: AppTheme.error, fontSize: 13),
            textAlign: TextAlign.center,
          ),
        ],
        const SizedBox(height: 16),
        ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 50),
          child: ElevatedButton(
            onPressed: isLoading ? null : onConnect,
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
        if (isCheckingSavedServer) ...[
          const SizedBox(height: 12),
          const Text('Checking saved server...', textAlign: TextAlign.center),
        ],
        if (retrySavedServer != null)
          TextButton(
            onPressed: isLoading ? null : retrySavedServer,
            child: const Text('Retry saved server'),
          ),
        if (passkeyServer case final server?) ...[
          const SizedBox(height: 16),
          OutlinedButton.icon(
            onPressed: isLoading ? null : onPasskey,
            icon: const Icon(Icons.fingerprint),
            label: const Text('Sign in with passkey',
                textAlign: TextAlign.center),
          ),
          const SizedBox(height: 6),
          Text('For ${server.label}', textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall),
          if (server.name != null)
            Text(server.url, textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.bodySmall),
        ],
        if (servers.isNotEmpty) ...[
          TextButton.icon(
            onPressed: isAuthenticating ? null : onToggleSavedServers,
            icon: Icon(
                showSavedServers ? Icons.expand_less : Icons.expand_more),
            label: Text(
                showSavedServers ? 'Hide saved servers' : 'Saved servers'),
          ),
          if (showSavedServers)
            SavedServerPicker(
              servers: servers,
              selectedUrl: controller.text,
              onSelect: (server) {
                if (!isAuthenticating) onSelectServer(server);
              },
              onForget: (server) {
                if (!isAuthenticating) onForgetServer(server);
              },
            ),
        ],
        if (historyUnavailable) ...[
          const SizedBox(height: 12),
          const Text('Saved servers are unavailable. Paste a connection link '
              'or enter an address to continue.', textAlign: TextAlign.center),
        ],
        const SizedBox(height: 24),
        const ServerSetupHelp(),
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
  final String? serverName;
  final ServerStatus? serverStatus;
  final VoidCallback onBack;

  const _LoginView({
    super.key,
    required this.serverUrl,
    this.serverName,
    this.serverStatus,
    required this.onBack,
  });

  @override
  ConsumerState<_LoginView> createState() => _LoginViewState();
}

class _LoginViewState extends ConsumerState<_LoginView> {
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();
  bool _obscurePassword = true;
  bool get _showPasskey {
    final platform = ref.watch(authPasskeyPlatformProvider).valueOrNull;
    return platform != null &&
        widget.serverStatus?.supportsPasskeyPlatform(platform) == true;
  }

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
        ServerBadge(
          server: SavedServer(url: widget.serverUrl, name: widget.serverName),
          onChange: isLoading ? null : widget.onBack,
        ),
        const SizedBox(height: 24),

        if (widget.serverStatus?.ssoAvailable == true) ...[
          SizedBox(
            width: double.infinity,
            child: FilledButton.icon(
              onPressed: isLoading ? null : () async {
                try {
                  await ref.read(authProvider.notifier).startSSO(widget.serverUrl);
                } catch (_) {
                  // The auth notifier records a visible, retryable error.
                }
              },
              icon: const Icon(Icons.login),
              label: Text('Continue with ${widget.serverStatus!.ssoProvider}'),
            ),
          ),
          const SizedBox(height: 16),
          if (widget.serverStatus?.ssoOnly == true) ...[
            const Text(
              'Single sign-on is required. Administrators can use local sign-in below to recover access.',
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 16),
          ],
        ],
        if (widget.serverStatus?.plexAvailable == true) ...[
          SizedBox(
              width: double.infinity,
              child: OutlinedButton.icon(
                onPressed: isLoading
                    ? null
                    : () => context.go(Uri(
                            path: '/plex/continue',
                            queryParameters: {'server': widget.serverUrl})
                        .toString()),
                icon: const Icon(Icons.play_arrow),
                label: Text(widget.serverStatus?.ssoOnly == true
                    ? 'Continue with Plex (administrator recovery)'
                    : 'Continue with Plex'),
              )),
          const SizedBox(height: 16),
        ],
        if (widget.serverStatus?.ssoError case final message?) ...[
          Text(message, textAlign: TextAlign.center),
          const SizedBox(height: 16),
        ],

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
                textStyle:
                    const TextStyle(fontWeight: FontWeight.w600, fontSize: 16),
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
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
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

        Wrap(
          alignment: WrapAlignment.center,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            TextButton(
              onPressed: isLoading ? null : widget.onBack,
              child: const Text(
                'Use a connection link',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              ),
            ),
          ],
        ),
      ],
    );
  }
}
