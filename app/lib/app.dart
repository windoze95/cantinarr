import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
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
    if (uri.scheme == 'cantinarr' && uri.host == 'connect') {
      ref.read(authProvider.notifier).connectWithLink(uri.toString());
    }
  }

  @override
  void dispose() {
    _linkSubscription?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);

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
      routerConfig: router,
    );
  }
}
