import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/downloads_activity.dart';

final downloadsAccountProvider = Provider<String>((ref) {
  final auth = ref.watch(authProvider).valueOrNull;
  return '${Uri.encodeComponent(auth?.connection?.serverUrl ?? '')}:${auth?.user?.id ?? 0}';
});

class DownloadsPreferences {
  final bool content;
  final String scope;
  const DownloadsPreferences({this.content = false, this.scope = 'all'});
}

class DownloadsPreferencesNotifier extends AutoDisposeAsyncNotifier<DownloadsPreferences> {
  @override
  Future<DownloadsPreferences> build() async {
    final account = ref.watch(downloadsAccountProvider);
    final prefs = await SharedPreferences.getInstance();
    return DownloadsPreferences(
      content: prefs.getBool('downloads:$account:content') ?? false,
      scope: prefs.getString('downloads:$account:scope') == 'mine' ? 'mine' : 'all',
    );
  }

  Future<void> select({bool? content, String? scope}) async {
    final account = ref.read(downloadsAccountProvider);
    final current = state.valueOrNull ?? const DownloadsPreferences();
    final next = DownloadsPreferences(content: content ?? current.content,
        scope: scope ?? current.scope);
    state = AsyncData(next);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool('downloads:$account:content', next.content);
    await prefs.setString('downloads:$account:scope', next.scope);
  }
}

final downloadsPreferencesProvider = AsyncNotifierProvider.autoDispose<
    DownloadsPreferencesNotifier, DownloadsPreferences>(DownloadsPreferencesNotifier.new);

final downloadsScopeProvider = Provider<String>((ref) {
  final auth = ref.watch(authProvider).valueOrNull;
  if (auth?.user?.isAdmin == true) return 'all';
  if (auth?.connection?.downloadsUserScope == 'mine') return 'mine';
  return ref.watch(downloadsPreferencesProvider).valueOrNull?.scope ?? 'all';
});

// A summary may keep its last value during ordinary polling, but never across
// a change that can alter which jobs this account is allowed to count.
final downloadsAccessKeyProvider = Provider<String>((ref) {
  final auth = ref.watch(authProvider).valueOrNull;
  final permissions = [...?auth?.user?.permissions]..sort();
  final instances = [
    ...?auth?.connection?.instances
        .map((instance) => '${instance.serviceType}:${instance.id}'),
  ]..sort();
  return jsonEncode({
    'account': ref.watch(downloadsAccountProvider),
    'scope': ref.watch(downloadsScopeProvider),
    'role': auth?.user?.role,
    'permissions': permissions,
    'child': auth?.user?.child,
    'content_limits': auth?.user?.contentLimits?.toJson(),
    'activity': auth?.connection?.downloadsActivity,
    'user_scope': auth?.connection?.downloadsUserScope,
    'instances': instances,
  });
});

// One clock serves the menu badge and the visible Content view. It lives with
// the foreground shell, including while another module is selected.
class DownloadsRefreshState {
  final int revision;
  final int clearEpoch;
  const DownloadsRefreshState({this.revision = 0, this.clearEpoch = 0});
}

class DownloadsRefreshNotifier extends AutoDisposeNotifier<DownloadsRefreshState> {
  @override
  DownloadsRefreshState build() {
    final supported = ref.watch(authProvider.select(
        (s) => s.valueOrNull?.connection?.downloadsActivity == true));
    if (!supported) return const DownloadsRefreshState();
    var foreground = WidgetsBinding.instance.lifecycleState == null ||
        WidgetsBinding.instance.lifecycleState == AppLifecycleState.resumed;
    Timer? debounce;
    final timer = Timer.periodic(const Duration(seconds: 15), (_) {
      if (foreground) refresh();
    });
    final lifecycle = _DownloadsLifecycle((resumed) {
      foreground = resumed;
      if (resumed) refresh();
    });
    WidgetsBinding.instance.addObserver(lifecycle);
    final events = ref.watch(realtimeEventsProvider).listen((event) {
      if (!foreground) return;
      if (event.type == 'config_changed') {
        // Clear user results immediately, before the config refresh completes.
        refresh(clearCached: true);
      } else if (const {'downloads_queue', 'arr_queue_changed',
          'request_status_changed', 'request_updated', 'request_decision'}.contains(event.type)) {
        debounce?.cancel();
        debounce = Timer(const Duration(milliseconds: 250), refresh);
      }
    });
    ref.onDispose(() {
      timer.cancel(); debounce?.cancel(); events.cancel();
      WidgetsBinding.instance.removeObserver(lifecycle);
    });
    return const DownloadsRefreshState();
  }

  void refresh({bool clearCached = false}) => state = DownloadsRefreshState(
      revision: state.revision + 1,
      clearEpoch: state.clearEpoch + (clearCached ? 1 : 0));
}

final downloadsRefreshProvider = NotifierProvider.autoDispose<DownloadsRefreshNotifier,
    DownloadsRefreshState>(DownloadsRefreshNotifier.new);

class _DownloadsLifecycle extends WidgetsBindingObserver {
  final void Function(bool) changed;
  _DownloadsLifecycle(this.changed);
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) =>
      changed(state == AppLifecycleState.resumed);
}

typedef _DownloadsRequestContext = ({String accessKey, String scope, int clearEpoch});

final _downloadsRequestContextProvider = Provider.autoDispose<_DownloadsRequestContext>((ref) => (
  accessKey: ref.watch(downloadsAccessKeyProvider),
  scope: ref.watch(downloadsScopeProvider),
  clearEpoch: ref.watch(downloadsRefreshProvider.select((state) => state.clearEpoch)),
));

// Keeping requests in a family lets Riverpod retain confirmed data while the
// same account and scope poll again. Authorization and config changes move to
// a new family member, which has no previous value to expose.
final _downloadsSummaryRequestProvider = FutureProvider.autoDispose.family<
    DownloadsActivity?, _DownloadsRequestContext>((ref, context) async {
  final auth = ref.watch(authProvider).valueOrNull;
  if (auth?.connection?.downloadsActivity != true) return null;
  ref.watch(downloadsRefreshProvider.select((state) => state.revision));
  final dio = ref.watch(backendClientProvider);
  final cancel = CancelToken();
  ref.onDispose(cancel.cancel);
  await ref.watch(downloadsPreferencesProvider.future);
  final response = await dio.get('/api/downloads/summary',
      queryParameters: {'scope': context.scope}, cancelToken: cancel);
  return DownloadsActivity.fromJson(response.data as Map<String, dynamic>);
});

final downloadsSummaryProvider = Provider.autoDispose<AsyncValue<DownloadsActivity?>>((ref) {
  return ref.watch(_downloadsSummaryRequestProvider(
      ref.watch(_downloadsRequestContextProvider)));
});

final _downloadsActivityRequestProvider = FutureProvider.autoDispose.family<
    DownloadsActivity, _DownloadsRequestContext>((ref, context) async {
  final auth = ref.watch(authProvider).valueOrNull;
  ref.watch(downloadsRefreshProvider.select((state) => state.revision));
  if (auth?.connection?.downloadsActivity != true) throw StateError('Downloads unavailable');
  final dio = ref.watch(backendClientProvider);
  final cancel = CancelToken();
  ref.onDispose(cancel.cancel);
  await ref.watch(downloadsPreferencesProvider.future);
  final response = await dio.get('/api/downloads/activity',
      queryParameters: {'scope': context.scope}, cancelToken: cancel);
  return DownloadsActivity.fromJson(response.data as Map<String, dynamic>);
});

final downloadsActivityProvider = Provider.autoDispose<AsyncValue<DownloadsActivity>>((ref) {
  return ref.watch(_downloadsActivityRequestProvider(
      ref.watch(_downloadsRequestContextProvider)));
});

final downloadsActivityFutureProvider = Provider.autoDispose<Future<DownloadsActivity>>((ref) {
  return ref.watch(_downloadsActivityRequestProvider(
      ref.watch(_downloadsRequestContextProvider)).future);
});
