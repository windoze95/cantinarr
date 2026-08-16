import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/request/logic/pending_approvals_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('stale approval refresh cannot repopulate the queue after logout',
      () async {
    final auth = _MutableAuthNotifier();
    final adapter = _DeferredApprovalsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(() => auth),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    final subscription = container.listen<int>(
      pendingApprovalsProvider,
      (_, __) {},
      fireImmediately: true,
    );
    addTearDown(subscription.close);

    await _waitFor(() => adapter.calls == 1);
    expect(container.read(pendingApprovalsLoadedProvider), isFalse);

    auth.setAuth(const AuthState());
    await pumpEventQueue();
    expect(container.read(pendingApprovalsProvider), 0);
    expect(container.read(pendingApprovalsLoadedProvider), isFalse);

    adapter.complete([
      {'id': 1, 'title': 'Stale request'},
    ]);
    await pumpEventQueue();

    expect(container.read(pendingApprovalsProvider), 0);
    expect(container.read(pendingApprovalsLoadedProvider), isFalse);
  });

  test('an admin auth refresh keeps the loaded flag while recounting',
      () async {
    final auth = _MutableAuthNotifier();
    final adapter = _ImmediateApprovalsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(() => auth),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    final subscription = container.listen<int>(
      pendingApprovalsProvider,
      (_, __) {},
      fireImmediately: true,
    );
    addTearDown(subscription.close);

    await _waitFor(() => container.read(pendingApprovalsLoadedProvider));

    // A routine re-emission of the same admin session (token refresh, app
    // resume, client swap) re-binds the notifier. The queue's emptiness we
    // already know must stay known during the recount — resetting it here is
    // what made every conditional menu entry flash fail-open.
    auth.setAuth(const AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access-rotated',
        refreshToken: 'refresh',
      ),
      user: UserProfile(id: 1, username: 'admin', role: 'admin'),
    ));
    expect(container.read(pendingApprovalsLoadedProvider), isTrue,
        reason: 'the re-bind itself must not un-know the count');

    await pumpEventQueue();
    await _waitFor(() => adapter.calls >= 2);
    expect(container.read(pendingApprovalsLoadedProvider), isTrue);
    expect(container.read(pendingApprovalsProvider), 0);
  });
}

class _MutableAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => _adminState;

  void setAuth(AuthState value) => state = AsyncData(value);
}

class _DeferredApprovalsAdapter implements HttpClientAdapter {
  final _response = Completer<ResponseBody>();
  int calls = 0;

  void complete(List<Map<String, dynamic>> requests) {
    _response.complete(ResponseBody.fromString(
      jsonEncode(requests),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    ));
  }

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) {
    calls++;
    return _response.future;
  }

  @override
  void close({bool force = false}) {}
}

class _ImmediateApprovalsAdapter implements HttpClientAdapter {
  int calls = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    calls++;
    return ResponseBody.fromString(
      jsonEncode(const <Map<String, dynamic>>[]),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

Future<void> _waitFor(bool Function() condition) async {
  final deadline = DateTime.now().add(const Duration(seconds: 2));
  while (!condition() && DateTime.now().isBefore(deadline)) {
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  expect(condition(), isTrue);
}
