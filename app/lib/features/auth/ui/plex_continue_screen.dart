import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../data/plex_auth_service.dart';
import '../logic/auth_provider.dart';
import '../../settings/ui/oidc_settings_screen.dart';

class PlexContinueScreen extends ConsumerStatefulWidget {
  final Uri uri;
  const PlexContinueScreen({super.key, required this.uri});
  @override
  ConsumerState<PlexContinueScreen> createState() => _PlexContinueScreenState();
}

class _PlexContinueScreenState extends ConsumerState<PlexContinueScreen> {
  PlexPending? _pending;
  String? _error;
  bool _starting = true, _checking = false, _cancelled = false;
  Timer? _timer;
  @override
  void initState() {
    super.initState();
    _begin();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _begin({bool retry = false}) async {
    setState(() {
      _starting = true;
      _error = null;
    });
    try {
      await ref.read(authProvider.future);
      if (!mounted || _cancelled) return;
      final service = ref.read(plexAuthServiceProvider);
      final previous = _pending;
      if (retry) await service.cancel();
      var pending = await service.load();
      if (!mounted || _cancelled) return;
      final resumed = pending != null;
      if (pending == null) {
        final server = previous?.server ?? widget.uri.queryParameters['server'];
        final purpose = previous?.purpose ??
            widget.uri.queryParameters['purpose'] ??
            'login';
        if (server == null || server.isEmpty) {
          throw StateError(
              'No Plex sign-in is waiting. Return to sign-in and try again.');
        }
        pending = await ref
            .read(authProvider.notifier)
            .startPlex(server, purpose: purpose);
      }
      if (!mounted || _cancelled) return;
      setState(() {
        _pending = pending;
        _starting = false;
      });
      _timer?.cancel();
      _timer = Timer.periodic(const Duration(seconds: 3), (_) => _check());
      if (!resumed) await _open();
      if (resumed) await _check();
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = oidcError(e);
          _starting = false;
        });
      }
    }
  }

  Future<void> _open() async {
    try {
      await ref.read(plexAuthServiceProvider).reopen();
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    }
  }

  Future<void> _check() async {
    if (_checking || _starting || _cancelled || !mounted) return;
    setState(() => _checking = true);
    try {
      final purpose = await ref.read(authProvider.notifier).checkPlex();
      if (!mounted || _cancelled) return;
      if (purpose != null) {
        _timer?.cancel();
        context.go(
            purpose == 'link' ? '/settings/sso-account' : '/dashboard/movies');
      }
    } catch (e) {
      if (mounted && !_cancelled) setState(() => _error = oidcError(e));
      _timer?.cancel(); // Check now retries transient failures without a loop.
    } finally {
      if (mounted) setState(() => _checking = false);
    }
  }

  Future<void> _cancel() async {
    if (!await ref.read(plexAuthServiceProvider).cancel()) return;
    _cancelled = true;
    _timer?.cancel();
    ref.invalidate(plexPendingProvider);
    if (!mounted) return;
    context.go(ref.read(authProvider).valueOrNull?.isAuthenticated == true
        ? (_pending?.purpose == 'link' ? '/settings/sso-account' : '/settings')
        : '/login');
  }

  @override
  Widget build(BuildContext context) => PopScope(
        canPop: false,
        onPopInvokedWithResult: (didPop, _) {
          if (!didPop) _cancel();
        },
        child: Scaffold(
            appBar: AppBar(
                title: const Text('Continue with Plex'),
                leading: IconButton(
                    onPressed: _cancel,
                    icon: const Icon(Icons.close),
                    tooltip: 'Cancel')),
            body: Center(
              child: SingleChildScrollView(
                  padding: const EdgeInsets.all(24),
                  child: ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 460),
                      child: Column(mainAxisSize: MainAxisSize.min, children: [
                        if (_starting) const CircularProgressIndicator(),
                        const Text(
                            'Approve Cantinarr in your Plex browser, then return here.'),
                        if (_pending != null) ...[
                          const SizedBox(height: 12),
                          Text(_pending!.server, textAlign: TextAlign.center),
                          Text(_pending!.purpose == 'link'
                              ? 'Linking your Plex sign-in'
                              : 'Signing in to Cantinarr'),
                        ],
                        if (_error != null)
                          Padding(
                              padding: const EdgeInsets.symmetric(vertical: 16),
                              child: Text(_error!,
                                  style: TextStyle(
                                      color: Theme.of(context)
                                          .colorScheme
                                          .error))),
                        const SizedBox(height: 20),
                        FilledButton(
                            onPressed: _starting ? null : _open,
                            child: const Text('Reopen Plex')),
                        OutlinedButton(
                            onPressed: _starting || _checking ? null : _check,
                            child: Text(_checking ? 'Checking…' : 'Check now')),
                        if (_error != null)
                          TextButton(
                              onPressed: _starting || _checking
                                  ? null
                                  : () => _begin(retry: true),
                              child: const Text('Retry sign-in')),
                        TextButton(
                            onPressed: _cancel, child: const Text('Cancel')),
                      ]))),
            )),
      );
}
