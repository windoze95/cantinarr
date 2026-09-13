import 'dart:async';
import 'package:dio/dio.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/network/websocket_client.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/request_quota.dart';

final requestQuotasSupportedProvider = Provider<bool>((ref) =>
    ref.watch(authProvider.select((s) => s.valueOrNull?.connection?.requestQuotas ?? false)));
final requestQuotaRevisionProvider = StateProvider<int>((ref) => 0);
final requestQuotaServiceProvider = Provider((ref) =>
    RequestQuotaService(ref.watch(backendClientProvider)));

Duration requestQuotaRefreshDelay(RequestQuotaView view, DateTime next) {
  // Use elapsed server time despite a skewed device clock. A 30-day timer
  // exceeds browsers' signed 32-bit timeout range, so recheck at least daily.
  final delay = next.difference(view.asOf ?? DateTime.now());
  if (delay.isNegative) return const Duration(seconds: 1);
  if (delay >= const Duration(days: 1)) return const Duration(days: 1);
  return delay + const Duration(milliseconds: 100);
}

final requestQuotaViewProvider = FutureProvider.autoDispose.family<RequestQuotaView, String>((ref, path) async {
  ref.watch(requestQuotaSyncProvider);
  ref.watch(requestQuotaRevisionProvider);
  final view = await ref.watch(requestQuotaServiceProvider).read(path);
  final next = view.nextChange;
  if (next != null) {
    final timer = Timer(requestQuotaRefreshDelay(view, next), ref.invalidateSelf);
    ref.onDispose(timer.cancel);
  }
  return view;
});

final requestQuotaSyncProvider = Provider.autoDispose<void>((ref) {
  if (!ref.watch(requestQuotasSupportedProvider)) return;
  final socket = ref.watch(webSocketClientProvider.notifier);
  final dio = ref.watch(backendClientProvider);
  Timer? debounce;
  var disposed = false;
  void changed() {
    if (disposed) return;
    debounce?.cancel();
    debounce = Timer(const Duration(milliseconds: 100), () {
      if (!disposed) ref.read(requestQuotaRevisionProvider.notifier).state++;
    });
  }
  final lifecycle = _QuotaLifecycle(changed);
  WidgetsBinding.instance.addObserver(lifecycle);
  var connected = socket.isConnected;
  void connectionChanged() {
    if (socket.isConnected && !connected) changed();
    connected = socket.isConnected;
  }
  socket.addListener(connectionChanged);
  final events = socket.events.listen((e) {
    if (e.type == 'request_quota_changed') changed();
  });
  bool mutation(RequestOptions o) => o.method != 'GET' &&
      o.path != '/api/requests/preview' &&
      (o.path.startsWith('/api/requests') || o.path.contains('/request-quotas') ||
          o.path.startsWith('/api/admin/requests'));
  final interceptor = InterceptorsWrapper(
    onResponse: (r, h) { if (mutation(r.requestOptions)) changed(); h.next(r); },
    onError: (e, h) { if (mutation(e.requestOptions)) changed(); h.next(e); },
  );
  dio.interceptors.add(interceptor);
  socket.ensureConnected();
  ref.onDispose(() {
    disposed = true;
    debounce?.cancel();
    events.cancel();
    socket.removeListener(connectionChanged);
    WidgetsBinding.instance.removeObserver(lifecycle);
    dio.interceptors.remove(interceptor);
  });
});

class _QuotaLifecycle extends WidgetsBindingObserver {
  final VoidCallback refresh;
  _QuotaLifecycle(this.refresh);
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) refresh();
  }
}
