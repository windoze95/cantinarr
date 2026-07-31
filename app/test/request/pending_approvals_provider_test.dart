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

Future<void> _waitFor(bool Function() condition) async {
  final deadline = DateTime.now().add(const Duration(seconds: 2));
  while (!condition() && DateTime.now().isBefore(deadline)) {
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  expect(condition(), isTrue);
}
