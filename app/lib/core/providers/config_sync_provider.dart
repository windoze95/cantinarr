import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../features/auth/logic/auth_provider.dart';
import '../network/websocket_client.dart';

/// Keep public configuration current across sessions, reconnects, and resume.
/// Errors retain the last successful configuration; the next trigger retries.
final configSyncProvider = Provider.autoDispose<void>((ref) {
  final client = ref.watch(webSocketClientProvider.notifier);
  var disposed = false;
  Future<void> refresh() async {
    if (disposed) return;
    try {
      await ref.read(authProvider.notifier).refreshConfig();
    } catch (_) {
      // An unavailable server must never look like an empty inventory.
    }
  }

  final lifecycle = _ConfigLifecycle(refresh);
  WidgetsBinding.instance.addObserver(lifecycle);
  var connected = client.isConnected;
  void onConnection() {
    final next = client.isConnected;
    if (next && !connected) unawaited(refresh());
    connected = next;
  }

  client.addListener(onConnection);
  final events = client.events.listen((event) {
    if (event.type == 'config_changed') unawaited(refresh());
  });
  client.ensureConnected();
  ref.onDispose(() {
    disposed = true;
    events.cancel();
    client.removeListener(onConnection);
    WidgetsBinding.instance.removeObserver(lifecycle);
  });
});

class _ConfigLifecycle extends WidgetsBindingObserver {
  final Future<void> Function() refresh;
  _ConfigLifecycle(this.refresh);

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(refresh());
  }
}
