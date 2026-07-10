import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import 'core/network/websocket_client.dart';
import 'core/providers/realtime_provider.dart';
import 'core/storage/preferences.dart';
import 'core/theme/app_theme.dart';
import 'core/widgets/app_ambient_background.dart';
import 'features/auth/logic/auth_provider.dart';
import 'features/issues/logic/issues_provider.dart';
import 'features/notifications/push_service.dart';
import 'features/request/logic/pending_approvals_provider.dart';
import 'features/settings/logic/update_status_provider.dart';
import 'navigation/app_router.dart';

class CantinarrApp extends ConsumerStatefulWidget {
  const CantinarrApp({super.key});

  @override
  ConsumerState<CantinarrApp> createState() => _CantinarrAppState();
}

class _CantinarrAppState extends ConsumerState<CantinarrApp>
    with WidgetsBindingObserver {
  late final AppLinks _appLinks;
  StreamSubscription<Uri>? _linkSubscription;
  final _scaffoldMessengerKey = GlobalKey<ScaffoldMessengerState>();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _appLinks = AppLinks();
    _initDeepLinks();
    // Reading the push service wires its native tap handler (for warm taps);
    // once the first frame is up (router exists) route any cold-start tap.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(pushServiceProvider).handleInitialNotification();
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Returning to the foreground is a good moment to retry a reconnecting
    // session immediately instead of waiting for the periodic retry.
    if (state == AppLifecycleState.resumed) {
      ref.read(authProvider.notifier).reconnectNow();
      // Re-sync the approvals badges in case the queue changed (or another
      // admin acted) while we were backgrounded. No-op for non-admins.
      ref.read(pendingApprovalsProvider.notifier).refresh();
      // Same for the open-issues badge.
      ref.read(openIssuesProvider.notifier).refresh();
      // And re-check for a newer server release (no-op for non-admins).
      ref.read(updateStatusProvider.notifier).refresh();
    }
  }

  Future<void> _initDeepLinks() async {
    // Handle initial link (app opened via link)
    try {
      final initialLink = await _appLinks.getInitialLink();
      if (initialLink != null) {
        _handleLink(initialLink);
      }
    } catch (_) {}

    // Handle links while app is running
    _linkSubscription = _appLinks.uriLinkStream.listen(_handleLink);
  }

  void _handleLink(Uri uri) {
    if (uri.scheme != 'cantinarr') return;
    if (uri.host == 'connect') {
      ref.read(authProvider.notifier).connectWithLink(uri.toString());
      return;
    }
    if (uri.host == 'passkeys') {
      _openPasskeyCreate(uri);
    }
  }

  Future<void> _openPasskeyCreate(Uri uri) async {
    final auth = await ref.read(authProvider.future);
    if (!mounted) return;
    final targetServer = uri.queryParameters['server'];
    final currentServer = auth.connection?.serverUrl;
    final matchesServer = targetServer == null ||
        currentServer == null ||
        _sameServer(targetServer, currentServer);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      if (auth.isAuthenticated && matchesServer) {
        context.go('/settings/passkeys/new');
      } else {
        context.go('/login');
      }
    });
  }

  bool _sameServer(String left, String right) =>
      _normalizeServer(left) == _normalizeServer(right);

  String _normalizeServer(String value) {
    var normalized = value.trim();
    if (!normalized.startsWith('http://') &&
        !normalized.startsWith('https://')) {
      normalized = 'https://$normalized';
    }
    while (normalized.endsWith('/')) {
      normalized = normalized.substring(0, normalized.length - 1);
    }
    final parsed = Uri.tryParse(normalized);
    if (parsed == null || parsed.host.isEmpty) {
      return normalized.toLowerCase();
    }
    return parsed
        .replace(
          scheme: parsed.scheme.toLowerCase(),
          host: parsed.host.toLowerCase(),
        )
        .toString();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _linkSubscription?.cancel();
    super.dispose();
  }

  /// Shows an in-app toast for an approval decision pushed over the socket.
  void _showDecisionSnack(WsEvent event) {
    final messenger = _scaffoldMessengerKey.currentState;
    if (messenger == null) return;
    final data = event.data;
    final approved = data['decision'] == 'approved';
    final rawTitle = (data['title'] as String?)?.trim();
    final label =
        rawTitle == null || rawTitle.isEmpty ? 'Your request' : rawTitle;
    final reason = (data['reason'] as String?)?.trim();
    final text = approved
        ? 'Approved: $label'
        : (reason == null || reason.isEmpty
            ? 'Denied: $label'
            : 'Denied: $label — $reason');
    messenger
      ..clearSnackBars()
      ..showSnackBar(SnackBar(
        behavior: SnackBarBehavior.floating,
        backgroundColor: approved ? AppTheme.available : AppTheme.error,
        content: Text(text, style: const TextStyle(color: AppTheme.background)),
      ));
  }

  /// Shows an admin notice (with a "Settings" action) when the remediation
  /// circuit breaker disables auto-dispatch. The event text is server-authored
  /// (a fixed template + structured counts); no untrusted model text is shown.
  void _showAutodispatchDisabledSnack() {
    final messenger = _scaffoldMessengerKey.currentState;
    if (messenger == null) return;
    messenger
      ..clearSnackBars()
      ..showSnackBar(SnackBar(
        behavior: SnackBarBehavior.floating,
        backgroundColor: AppTheme.error,
        duration: const Duration(seconds: 8),
        content: const Text(
          'Auto-fix paused: too many failed attempts. Re-enable it in AI '
          'remediation settings.',
          style: TextStyle(color: AppTheme.background),
        ),
        action: SnackBarAction(
          label: 'Settings',
          textColor: AppTheme.background,
          onPressed: () =>
              ref.read(appRouterProvider).push('/settings/ai-remediation'),
        ),
      ));
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);

    // Surface approval decisions pushed over the socket as a toast (unless the
    // user muted them). Registered before any early return so the listen stays
    // unconditional across rebuilds.
    ref.listen(requestDecisionEventsProvider, (_, next) {
      final event = next.valueOrNull;
      if (event == null) return;
      if (!ref.read(requestNotificationsEnabledProvider)) return;
      _showDecisionSnack(event);
    });

    // Surface the auto-dispatch circuit-breaker notice to admins.
    ref.listen(autodispatchDisabledProvider, (_, next) {
      if (next.valueOrNull == null) return;
      final isAdmin =
          ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
      if (!isAdmin) return;
      _showAutodispatchDisabledSnack();
    });

    // Show blank screen while restoring session to prevent login flash
    if (authState.isLoading) {
      return MaterialApp(
        title: 'Cantinarr',
        theme: AppTheme.dark,
        debugShowCheckedModeBanner: false,
        builder: (context, child) => AppAmbientBackground(
          child: child ?? const SizedBox.shrink(),
        ),
        home: const Scaffold(),
      );
    }

    final router = ref.watch(appRouterProvider);
    return MaterialApp.router(
      title: 'Cantinarr',
      theme: AppTheme.dark,
      debugShowCheckedModeBanner: false,
      scaffoldMessengerKey: _scaffoldMessengerKey,
      routerConfig: router,
      builder: (context, child) => AppAmbientBackground(
        child: _UpdateBanner(
          child: _ReconnectingBanner(child: child ?? const SizedBox.shrink()),
        ),
      ),
    );
  }
}

/// A thin, non-interactive "Reconnecting…" bar shown at the top of the app
/// while a session is held optimistically and the server is unreachable. It
/// waits briefly before appearing so a normal (fast) reconnect never flashes
/// it, keeping launches seamless.
class _ReconnectingBanner extends ConsumerStatefulWidget {
  const _ReconnectingBanner({required this.child});

  final Widget child;

  @override
  ConsumerState<_ReconnectingBanner> createState() =>
      _ReconnectingBannerState();
}

class _ReconnectingBannerState extends ConsumerState<_ReconnectingBanner> {
  Timer? _delay;
  bool _visible = false;
  bool? _lastReconnecting;

  @override
  void dispose() {
    _delay?.cancel();
    super.dispose();
  }

  void _onChanged(bool reconnecting) {
    if (reconnecting) {
      // Defer showing so quick reconnects don't flash the bar.
      _delay ??= Timer(const Duration(milliseconds: 1200), () {
        if (mounted) setState(() => _visible = true);
      });
    } else {
      _delay?.cancel();
      _delay = null;
      if (_visible && mounted) setState(() => _visible = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final reconnecting = ref.watch(
      authProvider.select((s) => s.valueOrNull?.isReconnecting ?? false),
    );
    // React to transitions after the frame so we never call setState mid-build.
    if (reconnecting != _lastReconnecting) {
      _lastReconnecting = reconnecting;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _onChanged(reconnecting);
      });
    }

    return Stack(
      textDirection: TextDirection.ltr,
      fit: StackFit.expand,
      children: [
        widget.child,
        if (_visible)
          const Positioned(
            top: 0,
            left: 0,
            right: 0,
            child: IgnorePointer(
              child: SafeArea(
                bottom: false,
                child: _ReconnectingBar(),
              ),
            ),
          ),
      ],
    );
  }
}

/// The bar's visual content, factored out so the whole overlay can be const.
class _ReconnectingBar extends StatelessWidget {
  const _ReconnectingBar();

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 26,
      color: AppTheme.surfaceVariant,
      alignment: Alignment.center,
      child: const Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(
                strokeWidth: 2, color: AppTheme.accent),
          ),
          SizedBox(width: 8),
          Text(
            'Reconnecting…',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
          ),
        ],
      ),
    );
  }
}

/// A persistent, admin-only banner shown at the top of the app when a newer
/// Cantinarr release is available. Its action links to the admin's configured
/// management portal (if set) or the update guide otherwise, and it is
/// dismissible per release — dismissing silences only the offered version.
class _UpdateBanner extends ConsumerWidget {
  const _UpdateBanner({required this.child});

  final Widget child;

  static const _updateGuideUrl =
      'https://github.com/windoze95/cantinarr/blob/main/docs/updating.md';

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isAdmin = ref.watch(
      authProvider.select((s) => s.valueOrNull?.user?.isAdmin ?? false),
    );
    final status = ref.watch(updateStatusProvider);
    final dismissed = ref.watch(dismissedUpdateVersionProvider);

    final update = status?.update;
    if (!isAdmin ||
        update == null ||
        !update.available ||
        update.latest.isEmpty ||
        dismissed == update.latest) {
      return child;
    }

    return Column(
      children: [
        Material(
          color: AppTheme.surfaceVariant,
          child: SafeArea(
            bottom: false,
            child: _UpdateBannerBar(
              latest: update.latest,
              releaseUrl: update.url,
              managementUrl: status?.managementUrl ?? '',
              guideUrl: _updateGuideUrl,
              onDismiss: () => ref
                  .read(dismissedUpdateVersionProvider.notifier)
                  .set(update.latest),
            ),
          ),
        ),
        Expanded(child: child),
      ],
    );
  }
}

/// The update banner's visual content: a short message plus a "Notes" link, the
/// primary update action, and a dismiss button.
class _UpdateBannerBar extends StatelessWidget {
  const _UpdateBannerBar({
    required this.latest,
    required this.releaseUrl,
    required this.managementUrl,
    required this.guideUrl,
    required this.onDismiss,
  });

  final String latest;
  final String releaseUrl;
  final String managementUrl;
  final String guideUrl;
  final VoidCallback onDismiss;

  void _open(String url) {
    if (url.isEmpty) return;
    final uri = Uri.tryParse(url);
    if (uri == null) return;
    launchUrl(uri, mode: LaunchMode.externalApplication);
  }

  @override
  Widget build(BuildContext context) {
    final hasPortal = managementUrl.isNotEmpty;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 4, 4, 4),
      child: Row(
        children: [
          const Icon(Icons.system_update, size: 18, color: AppTheme.accent),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Cantinarr $latest is available',
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (releaseUrl.isNotEmpty)
            TextButton(
              onPressed: () => _open(releaseUrl),
              child: const Text('Notes'),
            ),
          TextButton(
            onPressed: () => _open(hasPortal ? managementUrl : guideUrl),
            child: Text(hasPortal ? 'Update' : 'How to update'),
          ),
          IconButton(
            onPressed: onDismiss,
            icon: const Icon(Icons.close, size: 18),
            color: AppTheme.textSecondary,
            tooltip: 'Dismiss',
            visualDensity: VisualDensity.compact,
          ),
        ],
      ),
    );
  }
}
