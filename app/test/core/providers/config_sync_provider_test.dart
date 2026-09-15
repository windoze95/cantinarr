import 'dart:async';

import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/config_sync_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Socket extends WebSocketClient {
  _Socket() : super(getServerUrl: () => null, getAccessToken: () => null);
  final messages = StreamController<WsEvent>.broadcast();
  bool connected = false;
  @override
  bool get isConnected => connected;
  @override
  Stream<WsEvent> get events => messages.stream;
  @override
  void ensureConnected() {}
  void setConnected(bool value) {
    connected = value;
    notifyListeners();
  }

  @override
  void dispose() {
    messages.close();
    super.dispose();
  }
}

class _Auth extends AuthNotifier {
  int reads = 0;
  bool fail = false;
  @override
  Future<AuthState> build() async => const AuthState();
  @override
  Future<void> refreshConfig() async {
    reads++;
    if (fail) throw StateError('offline');
  }
}

void main() {
  testWidgets(
      'config pings, reconnect and resume refresh; failures retry on the next trigger',
      (t) async {
    final socket = _Socket();
    final auth = _Auth();
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => auth),
      webSocketClientProvider.overrideWith((_) => socket),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    final sub = container.listen(configSyncProvider, (_, __) {});
    socket.setConnected(true);
    await t.pump();
    expect(auth.reads, 1);
    socket.messages.add(const WsEvent(type: 'config_changed', data: {}));
    await t.pump();
    expect(auth.reads, 2);
    socket.messages.add(const WsEvent(type: 'arr_queue_changed', data: {}));
    await t.pump();
    expect(auth.reads, 2);
    auth.fail = true;
    socket.setConnected(false);
    socket.setConnected(true);
    await t.pump();
    expect(auth.reads, 3);
    auth.fail = false;
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pump();
    expect(auth.reads, 4);
    expect(t.takeException(), isNull);
    sub.close();
    await t.pump(const Duration(milliseconds: 1));
    socket.messages.add(const WsEvent(type: 'config_changed', data: {}));
    await t.pump();
    expect(auth.reads, 4);
  });
}
