import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'core/network/websocket_client.dart';
import 'core/providers/realtime_provider.dart';
import 'core/storage/preferences.dart';
import 'core/theme/app_theme.dart';
import 'features/auth/logic/auth_provider.dart';
import 'navigation/app_router.dart';

class CantinarrApp extends ConsumerStatefulWidget {
  const CantinarrApp({super.key});

  @override
  ConsumerState<CantinarrApp> createState() => _CantinarrAppState();
}

class _CantinarrAppState extends ConsumerState<CantinarrApp> {
  late final AppLinks _appLinks;
  StreamSubscription<Uri>? _linkSubscription;
  final _scaffoldMessengerKey = GlobalKey<ScaffoldMessengerState>();

  @override
  void initState() {
    super.initState();
    _appLinks = AppLinks();
    _initDeepLinks();
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
    final label = rawTitle == null || rawTitle.isEmpty ? 'Your request' : rawTitle;
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
        content: Text(text, style: const TextStyle(color: Colors.white)),
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

    // Show blank screen while restoring session to prevent login flash
    if (authState.isLoading) {
      return MaterialApp(
        title: 'Cantinarr',
        theme: AppTheme.dark,
        debugShowCheckedModeBanner: false,
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
    );
  }
}
