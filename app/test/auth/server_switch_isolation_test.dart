import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/device/device_identity.dart';
import 'package:cantinarr/core/models/app_module.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/providers/module_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/notifications/push_service.dart';
import 'package:cantinarr/features/request/logic/pending_approvals_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _serverA = 'https://server-a.example.com';
const _serverB = 'https://server-b.example.com';

/// Cross-server cached-state isolation: when the auth flow connects to a
/// DIFFERENT server, nothing user-visible from the old server may bleed
/// through. The app's isolation mechanism is that every per-server provider
/// keys off [authProvider] (directly, or via [backendClientProvider]'s
/// serverUrl select) — these tests pin that contract end to end through the
/// real [AuthNotifier], and pin that secure storage (single-slot, not keyed
/// per server) is fully overwritten by the switch so a later cold-start
/// restore cannot resurrect old-server state.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('connecting to a different server drops all old-server state',
      () async {
    final storage = _MemoryStorage();
    final container = ProviderContainer(overrides: [
      authServiceProvider.overrideWithValue(_FakeAuthService()),
      storageServiceProvider.overrideWithValue(storage),
      deviceIdentityProvider
          .overrideWithValue(_FakeDeviceIdentityService(storage)),
      pushServiceProvider.overrideWith(_NoopPushService.new),
      // Keep the realtime stream inert; the socket lifecycle itself is
      // asserted via provider identity below.
      realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    ]);
    addTearDown(container.dispose);

    // Keep the per-server providers alive across the switch, like the
    // widgets that watch them at runtime.
    for (final listen in [
      () => container.listen(instanceProvider, (_, __) {}),
      () => container.listen(moduleProvider, (_, __) {}),
      () => container.listen(backendClientProvider, (_, __) {}),
      () => container.listen(webSocketClientProvider, (_, __) {}),
    ]) {
      addTearDown(listen().close);
    }

    expect((await container.read(authProvider.future)).isAuthenticated, isFalse,
        reason: 'clean install: nothing to restore');

    // ── Connect to server A and take non-default, user-visible state. ──
    final notifier = container.read(authProvider.notifier);
    await notifier.connectWithToken(_serverA, 'token-a');

    final stateA = container.read(authProvider).valueOrNull!;
    expect(stateA.isAuthenticated, isTrue);
    expect(stateA.connection!.serverUrl, _serverA);
    expect(stateA.user!.username, 'admin-a');

    var instances = container.read(instanceProvider);
    expect(instances.radarrInstances.map((i) => i.id),
        ['a-radarr-main', 'a-radarr-4k']);
    expect(instances.sonarrInstances.map((i) => i.id), ['a-sonarr-main']);
    container.read(instanceProvider.notifier)
        .setActiveRadarrInstance('a-radarr-4k');
    expect(container.read(instanceProvider).activeRadarrInstance!.id,
        'a-radarr-4k');

    expect(_labels(container.read(moduleProvider).modules),
        ['Discover', 'Radarr', 'Sonarr']);
    container.read(moduleProvider.notifier)
        .setActiveModule(ModuleType.radarr);
    expect(container.read(moduleProvider).activeModuleType, ModuleType.radarr);

    expect(container.read(backendClientProvider).options.baseUrl, _serverA);
    final socketA = container.read(webSocketClientProvider);

    // ── Switch: the auth flow connects to server B. ──
    await notifier.connectWithToken(_serverB, 'token-b');

    final stateB = container.read(authProvider).valueOrNull!;
    expect(stateB.connection!.serverUrl, _serverB);
    expect(stateB.user!.username, 'admin-b');

    // Instance config is server B's; the server-A selection is gone.
    instances = container.read(instanceProvider);
    expect(instances.radarrInstances.map((i) => i.id), ['b-radarr-main']);
    expect(instances.sonarrInstances, isEmpty);
    expect(instances.activeRadarrInstance!.id, 'b-radarr-main',
        reason: "server A's '4K' selection must not survive the switch");

    // Module state rebuilds from server B and lands back on Discover.
    final modules = container.read(moduleProvider);
    expect(_labels(modules.modules), ['Discover', 'Radarr', 'AI Assistant']);
    expect(modules.activeModuleType, ModuleType.dashboard,
        reason: 'the active module must reset, not stay on a server-A screen');

    // The HTTP client and the websocket client are rebuilt for server B.
    expect(container.read(backendClientProvider).options.baseUrl, _serverB);
    expect(identical(container.read(webSocketClientProvider), socketA), isFalse,
        reason: 'a server switch must tear down the old socket client');

    // Storage holds ONLY server B: tokens, and the cold-start session
    // snapshot a later launch would restore optimistically from.
    expect(storage.values[StorageKeys.serverUrl], _serverB);
    expect(storage.values[StorageKeys.jwt], 'b-access');
    expect(storage.values[StorageKeys.refreshToken], 'b-refresh');
    expect(storage.values[StorageKeys.refreshTokenBackup], 'b-refresh');
    expect(storage.values[StorageKeys.deviceId], 'b-device');
    final snapshot = jsonDecode(storage.values[StorageKeys.sessionConnection]!)
        as Map<String, dynamic>;
    expect(snapshot['server_name'], 'Server B');
    expect(
      (snapshot['instances'] as List<dynamic>)
          .map((i) => (i as Map<String, dynamic>)['id'])
          .toList(),
      ['b-radarr-main'],
      reason: 'no server-A instance may linger in the restore snapshot',
    );
    final user = jsonDecode(storage.values[StorageKeys.sessionUser]!)
        as Map<String, dynamic>;
    expect(user['username'], 'admin-b');
  });

  test(
      'a stale in-flight approvals fetch from the old server cannot '
      "overwrite the new server's requests badge", () async {
    final auth = _MutableAuthNotifier(_adminStateFor(_serverA));
    final adapter = _PerHostApprovalsAdapter();
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => auth),
      // Mirrors the real backendClientProvider contract: a new Dio keyed on
      // the connection's serverUrl, so the fetch target follows the switch.
      backendClientProvider.overrideWith((ref) {
        final serverUrl = ref.watch(
          authProvider.select((s) => s.valueOrNull?.connection?.serverUrl),
        );
        return Dio(BaseOptions(baseUrl: serverUrl ?? 'http://localhost'))
          ..httpClientAdapter = adapter;
      }),
      realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      pushServiceProvider.overrideWith(_NoopPushService.new),
    ]);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    final subscription = container.listen<int>(
      pendingApprovalsProvider,
      (_, __) {},
      fireImmediately: true,
    );
    addTearDown(subscription.close);

    // Server A's approvals fetch is in flight (slow server) …
    await _waitFor(() => adapter.serverACalls == 1);

    // … when the user switches to server B, whose queue holds ONE request.
    auth.setAuth(_adminStateFor(_serverB));
    await _waitFor(() => container.read(pendingApprovalsLoadedProvider));
    expect(container.read(pendingApprovalsProvider), 1);

    // Server A finally answers with its three pending requests. The stale
    // response must be discarded, not shown as server B's queue.
    adapter.completeServerA([
      {'id': 11, 'title': 'Stale A'},
      {'id': 12, 'title': 'Stale B'},
      {'id': 13, 'title': 'Stale C'},
    ]);
    await pumpEventQueue();

    expect(container.read(pendingApprovalsProvider), 1,
        reason: "server A's queue depth must never appear on server B");
    expect(container.read(pendingApprovalsLoadedProvider), isTrue);
  });
}

List<String> _labels(List<AppModule> modules) =>
    modules.map((m) => m.label).toList();

AuthState _adminStateFor(String serverUrl) => AuthState(
      connection: BackendConnection(
        serverUrl: serverUrl,
        accessToken: 'access',
        refreshToken: 'refresh',
      ),
      user: const UserProfile(id: 1, username: 'admin', role: 'admin'),
    );

Future<void> _waitFor(bool Function() condition) async {
  final deadline = DateTime.now().add(const Duration(seconds: 2));
  while (!condition() && DateTime.now().isBefore(deadline)) {
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  expect(condition(), isTrue);
}

/// Canned two-server backend: connect tokens and configs are answered per
/// server URL, so the real [AuthNotifier] can run its full connect flow
/// against both servers without a network.
class _FakeAuthService extends AuthService {
  static const _configs = {
    _serverA: ServerConfig(
      serverName: 'Server A',
      serverVersion: '1.0.0',
      services: AvailableServices(radarr: true, sonarr: true),
      instances: [
        ServiceInstance(
          id: 'a-radarr-main',
          serviceType: 'radarr',
          name: 'Main Radarr',
          isDefault: true,
        ),
        ServiceInstance(
          id: 'a-radarr-4k',
          serviceType: 'radarr',
          name: '4K Radarr',
        ),
        ServiceInstance(
          id: 'a-sonarr-main',
          serviceType: 'sonarr',
          name: 'Main Sonarr',
          isDefault: true,
        ),
      ],
    ),
    _serverB: ServerConfig(
      serverName: 'Server B',
      serverVersion: '1.1.0',
      services: AvailableServices(radarr: true, ai: true),
      instances: [
        ServiceInstance(
          id: 'b-radarr-main',
          serviceType: 'radarr',
          name: 'Radarr',
          isDefault: true,
        ),
      ],
    ),
  };

  static const _users = {
    _serverA: UserProfile(id: 1, username: 'admin-a', role: 'admin'),
    _serverB: UserProfile(id: 7, username: 'admin-b', role: 'admin'),
  };

  String _slot(String serverUrl) => serverUrl == _serverA ? 'a' : 'b';

  @override
  Future<AuthResponse> redeemConnectToken(
    String serverUrl,
    String token,
    String deviceName,
    String hardwareId,
  ) async {
    final slot = _slot(serverUrl);
    return AuthResponse(
      accessToken: '$slot-access',
      refreshToken: '$slot-refresh',
      user: _users[serverUrl]!,
      deviceId: '$slot-device',
    );
  }

  @override
  Future<ServerConfig> fetchConfig(String serverUrl, String accessToken) async {
    expect(accessToken, '${_slot(serverUrl)}-access',
        reason: "config must be fetched with the target server's own token");
    return _configs[serverUrl]!;
  }
}

/// In-memory [StorageService]; exposes [values] so tests can assert exactly
/// what a later cold start would restore from.
class _MemoryStorage implements StorageService {
  final Map<String, String> values = {};

  @override
  Future<String?> read({required String key}) async => values[key];

  @override
  Future<void> write({required String key, required String? value}) async {
    if (value == null) {
      values.remove(key);
    } else {
      values[key] = value;
    }
  }

  @override
  Future<void> delete({required String key}) async {
    values.remove(key);
  }

  @override
  Future<void> hardenAuthKeys() async {}
}

class _FakeDeviceIdentityService extends DeviceIdentityService {
  _FakeDeviceIdentityService(super.storage);

  @override
  Future<DeviceIdentity> resolve() async =>
      const DeviceIdentity(displayName: 'Test Device', hardwareId: 'hw-test');
}

/// Push is platform-channel territory; auth flows only need it to be inert.
class _NoopPushService extends PushService {
  _NoopPushService(super.ref);

  @override
  Future<void> registerForPush() async {}

  @override
  Future<void> setBadgeCount(int count) async {}
}

class _MutableAuthNotifier extends AuthNotifier {
  _MutableAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;

  void setAuth(AuthState value) => state = AsyncData(value);
}

/// Admin-approvals endpoint fake that answers per server: server A's
/// response is deferred until the test releases it; server B answers
/// immediately with a single pending request.
class _PerHostApprovalsAdapter implements HttpClientAdapter {
  final _serverAResponse = Completer<ResponseBody>();
  int serverACalls = 0;
  int serverBCalls = 0;

  void completeServerA(List<Map<String, dynamic>> requests) {
    _serverAResponse.complete(_json(requests));
  }

  static ResponseBody _json(Object body) => ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) {
    if (options.uri.host == 'server-a.example.com') {
      serverACalls++;
      return _serverAResponse.future;
    }
    serverBCalls++;
    return Future.value(_json([
      {'id': 21, 'title': 'Server B request'},
    ]));
  }

  @override
  void close({bool force = false}) {}
}
