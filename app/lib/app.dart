import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import 'core/network/websocket_client.dart';
import 'core/providers/realtime_provider.dart';
import 'core/storage/preferences.dart';
import 'core/theme/app_theme.dart';
import 'core/utils/version_compat.dart';
import 'core/widgets/app_ambient_background.dart';
import 'features/auth/logic/auth_provider.dart';
import 'features/issues/logic/issues_provider.dart';
import 'features/notifications/push_service.dart';
import 'features/request/logic/pending_approvals_provider.dart';
import 'features/settings/logic/app_version_provider.dart';
import 'features/settings/logic/update_status_provider.dart';
import 'navigation/app_router.dart';

/// Source of platform deep links: the link that launched the app (if any)
/// plus the stream of links delivered while it runs. The default
/// implementation wraps [AppLinks]; tests override [deepLinkSourceProvider]
/// to inject links without the platform channel.
abstract class DeepLinkSource {
  Future<Uri?> getInitialLink();
  Stream<Uri> get uriLinkStream;
}

class _AppLinksSource implements DeepLinkSource {
  final AppLinks _appLinks = AppLinks();

  @override
  Future<Uri?> getInitialLink() => _appLinks.getInitialLink();

  @override
  Stream<Uri> get uriLinkStream => _appLinks.uriLinkStream;
}

/// App-wide [DeepLinkSource].
final deepLinkSourceProvider =
    Provider<DeepLinkSource>((_) => _AppLinksSource());

/// True when two user-entered server URLs point at the same server, ignoring
/// cosmetic differences (case, trailing slashes, an omitted scheme, a default
/// port).
@visibleForTesting
bool sameServer(String left, String right) =>
    normalizeServer(left) == normalizeServer(right);

/// Canonicalizes a user-entered server URL for comparison: trims whitespace,
/// assumes https:// when no scheme is given, strips trailing slashes, and
/// lowercases the scheme and host. Never throws — unparseable input falls
/// back to a lowercased string compare.
@visibleForTesting
String normalizeServer(String value) {
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

/// Requester-facing copy for realtime approval decisions. Book decisions are
/// scoped to the concrete formats in the event so a partial result never
/// claims that the whole title was approved or denied.
@visibleForTesting
String requestDecisionSnackText(Map<String, dynamic> data) {
  final approved = data['decision'] == 'approved';
  final rawTitle = (data['title'] as String?)?.trim();
  final title = rawTitle == null || rawTitle.isEmpty
      ? 'Your request'
      : rawTitle;
  final reason = (data['reason'] as String?)?.trim();
  final bookScope = data['media_type'] == 'book'
      ? _bookDecisionScope(data, approved: approved)
      : null;
  final text = bookScope == null
      ? (approved ? 'Approved: $title' : 'Denied: $title')
      : '$bookScope ${approved ? 'approved' : 'denied'}: $title';
  return !approved && reason != null && reason.isNotEmpty
      ? '$text — $reason'
      : text;
}

String? _bookDecisionScope(
  Map<String, dynamic> data, {
  required bool approved,
}) {
  final rawFormats = data['book_formats'];
  final formats = <String>{};
  if (rawFormats is Map) {
    for (final entry in rawFormats.entries) {
      final format = entry.key.toString();
      final status = entry.value.toString();
      final belongsToDecision = approved
          ? const {
              'available',
              'downloading',
              'requested',
              'partial',
            }.contains(status)
          : status == 'denied';
      if (belongsToDecision &&
          (format == 'ebook' || format == 'audiobook')) {
        formats.add(format);
      }
    }
  }
  if (formats.isEmpty) {
    switch (data['book_format']?.toString()) {
      case 'ebook':
        formats.add('ebook');
      case 'audiobook':
        formats.add('audiobook');
      case 'both':
        formats.addAll(const ['ebook', 'audiobook']);
    }
  }
  if (formats.isEmpty) return null;
  return [
    if (formats.contains('ebook')) 'eBook',
    if (formats.contains('audiobook')) 'Audiobook',
  ].join(' + ');
}

class CantinarrApp extends ConsumerStatefulWidget {
  const CantinarrApp({super.key});

  @override
  ConsumerState<CantinarrApp> createState() => _CantinarrAppState();
}

class _CantinarrAppState extends ConsumerState<CantinarrApp>
    with WidgetsBindingObserver {
  StreamSubscription<Uri>? _linkSubscription;
  final _scaffoldMessengerKey = GlobalKey<ScaffoldMessengerState>();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
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
      // Same for the actionable + tracking issue navigation state.
      ref.read(issueQueueCountsProvider.notifier).refresh();
      // Proposals may have executed, failed, or been superseded while the app
      // was backgrounded. Reconcile the review badge on every foreground.
      ref.read(pendingAgentActionsProvider.notifier).refresh();
    }
  }

  Future<void> _initDeepLinks() async {
    final links = ref.read(deepLinkSourceProvider);
    // Handle initial link (app opened via link)
    try {
      final initialLink = await links.getInitialLink();
      if (initialLink != null) {
        _handleLink(initialLink);
      }
    } catch (_) {}

    // Handle links while app is running
    _linkSubscription = links.uriLinkStream.listen(_handleLink);
  }

  void _handleLink(Uri uri) {
    if (uri.scheme != 'cantinarr') return;
    if (uri.host == 'oidc') {
      _openOIDCReturn(uri);
      return;
    }
    if (uri.host == 'connect') {
      _handleConnectLink(uri);
      return;
    }
    if (uri.host == 'passkeys') {
      _openPasskeyCreate(uri);
    }
  }

  Future<void> _openOIDCReturn(Uri uri) async {
    await ref.read(authProvider.future);
    if (!mounted) return;
    try {
      final purpose = await ref.read(authProvider.notifier).finishSSO(uri);
      if (!mounted || purpose.isEmpty) return;
      final target = purpose == 'test' ? '/settings/oidc'
          : purpose == 'link' ? '/settings/sso-account' : '/dashboard/movies';
      ref.read(appRouterProvider).go(Uri(path: target, queryParameters: {
        if (purpose != 'login') 'verified': uri.queryParameters['flow'] ?? '',
      }).toString());
      if (purpose != 'login') {
        _scaffoldMessengerKey.currentState?.showSnackBar(SnackBar(content: Text(
          purpose == 'test' ? 'Test sign-in succeeded. No account was created or linked.' : 'Single sign-on identity linked.')));
      }
    } catch (e) {
      if (!mounted) return;
      _scaffoldMessengerKey.currentState?.showSnackBar(SnackBar(content: Text(
        ref.read(authProvider).valueOrNull?.error ?? 'Sign-in could not be completed. Please try again.')));
    }
  }

  /// A connect link while signed out connects directly. While signed in it is
  /// a request to replace this device's session, so it asks first — and the
  /// switch redeems the link before touching the current session, so a dead
  /// link never signs anyone out.
  Future<void> _handleConnectLink(Uri uri) async {
    final auth = await ref.read(authProvider.future);
    if (!mounted) return;
    if (!auth.isAuthenticated) {
      ref.read(authProvider.notifier).connectWithLink(uri.toString());
      return;
    }

    final token = uri.queryParameters['token'];
    final server = uri.queryParameters['server'];
    if (token == null || server == null) return;

    final conn = auth.connection!;
    final currentLabel = conn.serverName ?? conn.serverUrl;
    // The dialog needs a context under MaterialApp.router; this state sits
    // above it, so borrow the root navigator's (same reason _openPasskeyCreate
    // navigates through the router instance).
    final dialogContext = ref
        .read(appRouterProvider)
        .routerDelegate
        .navigatorKey
        .currentContext;
    if (dialogContext == null || !dialogContext.mounted) return;

    final confirmed = await showDialog<bool>(
      context: dialogContext,
      builder: (context) => AlertDialog(
        title: const Text('Switch Server'),
        content: Text(
          'This connect link is for $server. Connect there instead? '
          'This device will be signed out of $currentLabel.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Switch'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    final switched =
        await ref.read(authProvider.notifier).switchServer(server, token);
    if (switched == ServerSwitchResult.rejected) {
      _scaffoldMessengerKey.currentState?.showSnackBar(SnackBar(
        content: Text(
          'Could not connect with that link. It may have expired. '
          'You are still signed in to $currentLabel.',
        ),
      ));
    }
  }

  Future<void> _openPasskeyCreate(Uri uri) async {
    final auth = await ref.read(authProvider.future);
    if (!mounted) return;
    final targetServer = uri.queryParameters['server'];
    final currentServer = auth.connection?.serverUrl;
    final matchesServer = targetServer == null ||
        currentServer == null ||
        sameServer(targetServer, currentServer);
    // Navigate through the router instance (the same pattern as
    // _showAutodispatchDisabledSnack below): this state sits ABOVE
    // MaterialApp.router, so `context.go` cannot see the router — GoRouter.of
    // walks up the tree and asserts. And a post-frame deferral would wait on a
    // frame nothing schedules when the app is idle; router.go needs neither.
    final router = ref.read(appRouterProvider);
    if (auth.isAuthenticated && matchesServer) {
      router.go('/settings/passkeys/new');
    } else {
      router.go('/login');
    }
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
    final text = requestDecisionSnackText(data);
    messenger
      ..clearSnackBars()
      ..showSnackBar(SnackBar(
        behavior: SnackBarBehavior.floating,
        backgroundColor: approved ? AppTheme.available : AppTheme.error,
        content: Text(text, style: const TextStyle(color: AppTheme.background)),
      ));
  }

  /// Shows an admin notice when a standing auto-approval rule pauses itself
  /// after a failed fix. Fixed copy only; the "Review" action opens the
  /// triggering issue (the evidence) when the event carries one, else the
  /// rules screen.
  void _showAutoApprovalPausedSnack(WsEvent event) {
    final messenger = _scaffoldMessengerKey.currentState;
    if (messenger == null) return;
    final issueId = (event.data['issue_id'] as num?)?.toInt();
    final target = issueId != null && issueId > 0
        ? '/issues/$issueId'
        : '/settings/agent-approval-rules';
    messenger
      ..clearSnackBars()
      ..showSnackBar(SnackBar(
        behavior: SnackBarBehavior.floating,
        backgroundColor: AppTheme.error,
        duration: const Duration(seconds: 8),
        content: const Text(
          'An auto-approval rule paused itself after a failed fix.',
          style: TextStyle(color: AppTheme.background),
        ),
        action: SnackBarAction(
          label: 'Review',
          textColor: AppTheme.background,
          onPressed: () => ref.read(appRouterProvider).push(target),
        ),
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

    // Surface a self-paused auto-approval rule to admins: automation stood
    // down, so matching fixes are back to manual approval until re-armed.
    ref.listen(autoApprovalPausedProvider, (_, next) {
      final event = next.valueOrNull;
      if (event == null) return;
      final isAdmin =
          ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
      if (!isAdmin) return;
      _showAutoApprovalPausedSnack(event);
    });

    // Install the router on the first frame so a browser OIDC return survives
    // session restoration. A temporary MaterialApp with a home Navigator
    // consumes the initial URL and replaces it with "/" before GoRouter starts.
    final router = ref.watch(appRouterProvider);
    return MaterialApp.router(
      title: 'Cantinarr',
      theme: AppTheme.dark,
      debugShowCheckedModeBanner: false,
      scaffoldMessengerKey: _scaffoldMessengerKey,
      routerConfig: router,
      builder: (context, child) => AppAmbientBackground(
        // Keep the same blank restore screen without replacing the router.
        child: authState.isLoading
            ? const Scaffold()
            : _UpdateBanner(
                child: _ReconnectingBanner(
                    child: child ?? const SizedBox.shrink()),
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

/// The persistent banner slot at the top of the app, showing at most one
/// version-skew notice in priority order: this app is older than the server's
/// floor (everyone — the viewer can fix that one themselves), then the server
/// is older than this app's floor (admins). Both are warn-only by design — a
/// hard block would be a deliberate future escalation — and each is
/// dismissible per exact version pair, so a dismissal frees the slot and
/// resurfaces only when the versions change.
///
/// Release news is deliberately *not* one of them: the app-wide "a newer
/// Cantinarr is available" bar is off. The server still computes the
/// comparison and `/api/admin/update-status` still answers it — only the
/// nag is gone, so re-enabling is a UI change, not a feature rebuild.
class _UpdateBanner extends ConsumerWidget {
  const _UpdateBanner({required this.child});

  final Widget child;

  static const _updateGuideUrl =
      'https://github.com/windoze95/cantinarr/blob/main/docs/updating.md';

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final bar = _pickBar(ref);
    if (bar == null) return child;

    return Column(
      children: [
        Material(
          color: AppTheme.surfaceVariant,
          child: SafeArea(bottom: false, child: bar),
        ),
        Expanded(child: child),
      ],
    );
  }

  /// The admin's primary update action: their configured management portal,
  /// else the update guide.
  (String, String) _updateAction(String managementUrl) =>
      managementUrl.isNotEmpty
          ? ('Update', managementUrl)
          : ('How to update', _updateGuideUrl);

  Widget? _pickBar(WidgetRef ref) {
    final isAdmin = ref.watch(
      authProvider.select((s) => s.valueOrNull?.user?.isAdmin ?? false),
    );
    final connection =
        ref.watch(authProvider.select((s) => s.valueOrNull?.connection));
    // Read only for the admin's management-portal link below.
    final status = ref.watch(updateStatusProvider);
    final appVersion = ref.watch(appVersionProvider).valueOrNull?.version;
    final serverFloor = connection?.minAppVersion;
    final appFloor = ref.watch(minServerVersionProvider);

    final skew = evaluateVersionSkew(
      isWeb: kIsWeb,
      isAdmin: isAdmin,
      appVersion: appVersion,
      serverVersion: connection?.serverVersion,
      serverMinAppVersion: serverFloor,
      minServerVersionFloor: appFloor,
    );

    switch (skew) {
      case VersionSkewWarning.appTooOld:
        final pair = '$appVersion|$serverFloor';
        if (ref.watch(dismissedAppSkewPairProvider) != pair) {
          return _UpdateBannerBar(
            icon: Icons.warning_amber_rounded,
            message: 'This app is older than your server supports — '
                'update it from the app store',
            onDismiss: () =>
                ref.read(dismissedAppSkewPairProvider.notifier).set(pair),
          );
        }
      case VersionSkewWarning.serverTooOld:
        final pair = '${connection?.serverVersion}|$appFloor';
        if (ref.watch(dismissedServerSkewPairProvider) != pair) {
          final (label, url) = _updateAction(status?.managementUrl ?? '');
          return _UpdateBannerBar(
            icon: Icons.warning_amber_rounded,
            message: 'Server ${connection?.serverVersion} is older than '
                'this app supports — update the server',
            actionLabel: label,
            actionUrl: url,
            onDismiss: () =>
                ref.read(dismissedServerSkewPairProvider.notifier).set(pair),
          );
        }
      case null:
        break;
    }

    return null;
  }
}

/// The banner slot's visual content: an icon, a short message, an optional
/// primary action, and a dismiss button.
class _UpdateBannerBar extends StatelessWidget {
  const _UpdateBannerBar({
    required this.icon,
    required this.message,
    this.actionLabel,
    this.actionUrl,
    required this.onDismiss,
  });

  final IconData icon;
  final String message;
  final String? actionLabel;
  final String? actionUrl;
  final VoidCallback onDismiss;

  void _open(String url) {
    if (url.isEmpty) return;
    final uri = Uri.tryParse(url);
    if (uri == null) return;
    launchUrl(uri, mode: LaunchMode.externalApplication);
  }

  @override
  Widget build(BuildContext context) {
    final label = actionLabel;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 4, 4, 4),
      child: Row(
        children: [
          Icon(icon, size: 18, color: AppTheme.accent),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              message,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (label != null)
            TextButton(
              onPressed: () => _open(actionUrl ?? ''),
              child: Text(label),
            ),
          IconButton(
            onPressed: onDismiss,
            // No tooltip: this bar renders above MaterialApp's Navigator, so
            // there is no Overlay for one to mount into. The semantic label
            // keeps the button readable to screen readers.
            icon: const Icon(Icons.close, size: 18, semanticLabel: 'Dismiss'),
            color: AppTheme.textSecondary,
            visualDensity: VisualDensity.compact,
          ),
        ],
      ),
    );
  }
}
