import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/logic/setup_status_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _admin = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

void main() {
  test('signing in again loads the checklist without opening Settings',
      () async {
    final auth = _MutableAuth(_admin.copyWith(isReconnecting: true));
    final adapter = _SetupAdapter()..fail = true;
    final container = _container(auth, adapter);
    await container.read(authProvider.future);
    container.listen(setupStatusProvider, (_, __) {}, fireImmediately: true);
    await pumpEventQueue();
    expect(adapter.calls, 1);
    expect(container.read(setupStatusProvider), isNull);

    // An expired cached session returns to login, then the same server accepts
    // new credentials. The shell's existing subscription must recover itself.
    auth.setAuth(const AuthState());
    await pumpEventQueue();
    adapter.fail = false;
    auth.setAuth(_admin);
    await pumpEventQueue();

    expect(adapter.calls, 2);
    expect(container.read(setupStatusProvider)?.remaining, 1);
  });

  test('validated restore retries a failed checklist load for the same admin',
      () async {
    final auth = _MutableAuth(_admin.copyWith(isReconnecting: true));
    final adapter = _SetupAdapter()..fail = true;
    final container = _container(auth, adapter);
    await container.read(authProvider.future);
    container.listen(setupStatusProvider, (_, __) {}, fireImmediately: true);
    await pumpEventQueue();
    expect(container.read(setupStatusProvider), isNull);

    adapter.fail = false;
    auth.setAuth(_admin);
    await pumpEventQueue();

    expect(adapter.calls, 2);
    expect(container.read(setupStatusProvider)?.remaining, 1);
  });

  test('same-admin refresh keeps the known count; logout clears it', () async {
    final auth = _MutableAuth(_admin);
    final adapter = _SetupAdapter();
    final container = _container(auth, adapter);
    await container.read(authProvider.future);
    container.listen(setupStatusProvider, (_, __) {}, fireImmediately: true);
    await pumpEventQueue();
    expect(container.read(setupStatusProvider)?.remaining, 1);

    adapter.fail = true;
    auth.setAuth(_admin.copyWith(isReconnecting: true));
    expect(container.read(setupStatusProvider)?.remaining, 1);
    await pumpEventQueue();
    expect(container.read(setupStatusProvider)?.remaining, 1);

    auth.setAuth(const AuthState());
    expect(container.read(setupStatusProvider), isNull);
    await pumpEventQueue();
    final calls = adapter.calls;
    await container.read(setupStatusProvider.notifier).refresh();
    expect(adapter.calls, calls, reason: 'signed-out sessions never fetch');
  });
}

ProviderContainer _container(_MutableAuth auth, _SetupAdapter adapter) {
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => auth),
    // Keep the real client's auth dependency: a constant Dio override hides
    // ordering problems when login replaces the server connection.
    backendClientProvider.overrideWith((ref) {
      final url = ref.watch(
        authProvider.select((s) => s.valueOrNull?.connection?.serverUrl),
      );
      final dio = Dio(BaseOptions(baseUrl: url ?? 'http://localhost'))
        ..httpClientAdapter = adapter;
      ref.onDispose(() => dio.close(force: true));
      return dio;
    }),
  ]);
  addTearDown(container.dispose);
  return container;
}

class _MutableAuth extends AuthNotifier {
  _MutableAuth(this.initial);
  final AuthState initial;

  @override
  Future<AuthState> build() async => initial;

  void setAuth(AuthState value) => state = AsyncData(value);
}

class _SetupAdapter implements HttpClientAdapter {
  int calls = 0;
  bool fail = false;

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    expect(options.path, '/api/admin/setup-status');
    calls++;
    return ResponseBody.fromString(
      jsonEncode(fail
          ? {'error': 'temporarily unavailable'}
          : {
              'items': [
                {'key': 'radarr', 'configured': false, 'optional': false},
              ],
              'configured': 0,
              'total': 1,
            }),
      fail ? 503 : 200,
      headers: {
        'content-type': ['application/json']
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
