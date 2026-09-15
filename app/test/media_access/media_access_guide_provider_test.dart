import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/config_sync_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_access/logic/media_access_guide_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _jellyfin =
    ServiceInstance(id: 'jf-a', serviceType: 'jellyfin', name: 'Home');
const _secondJellyfin =
    ServiceInstance(id: 'jf-b', serviceType: 'jellyfin', name: 'Other');

AuthState _account({
  int id = 2,
  String server = 'https://cantinarr.example',
  bool admin = false,
  List<ServiceInstance> instances = const [_jellyfin],
  bool plexRequestable = false,
}) =>
    AuthState(
      user: UserProfile(
          id: id, username: 'viewer', role: admin ? 'admin' : 'user'),
      connection: BackendConnection(
        serverUrl: server,
        accessToken: 'access',
        refreshToken: 'refresh',
        instances: instances,
        plexAccessRequestable: plexRequestable,
      ),
    );

class _Auth extends AuthNotifier {
  _Auth(this.initial);
  final AuthState initial;
  bool failConfig = false;
  AuthState get current => state.requireValue;
  @override
  Future<AuthState> build() async => initial;
  void publish(AuthState value) => state = AsyncData(value);
  @override
  Future<void> refreshConfig() async {
    if (failConfig) throw StateError('offline');
    final current = state.requireValue;
    publish(current.copyWith(connection: current.connection!.copyWith()));
  }
}

class _Grants implements HttpClientAdapter {
  Dio get dio => Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = this;
  Map<String, List<String>> grants = {
    'jellyfin': ['jf-a']
  };
  bool fail = false;
  final usersRead = <int>[];
  Completer<Map<String, List<String>>>? pending;
  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    expectSync(options.method, 'GET');
    expectSync(
        options.uri.path, matches(r'^/api/admin/users/\d+/instance-grants$'));
    final userId = int.parse(options.uri.pathSegments[3]);
    usersRead.add(userId);
    if (fail) throw StateError('offline');
    final data = pending == null ? grants : await pending!.future;
    return ResponseBody.fromString(jsonEncode(data), 200, headers: {
      'content-type': ['application/json']
    });
  }

  @override
  void close({bool force = false}) {}
}

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
  void connect() {
    connected = true;
    notifyListeners();
  }

  @override
  void dispose() {
    messages.close();
    super.dispose();
  }
}

Future<ProviderContainer> _container(_Auth auth, _Grants grants) async {
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => auth),
    backendClientProvider.overrideWithValue(grants.dio),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  container.listen(mediaAccessGuideNavigationVisibleProvider, (_, __) {});
  await pumpEventQueue();
  return container;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('eligible by default; hide and show persist across app restarts',
      () async {
    final grants = _Grants();
    final first = await _container(_Auth(_account()), grants);
    expect(first.read(mediaAccessGuideNavigationVisibleProvider), isTrue);
    final saved =
        first.read(mediaAccessGuideHiddenProvider.notifier).setHidden(true);
    expect(first.read(mediaAccessGuideNavigationVisibleProvider), isFalse,
        reason: 'the shortcut changes before persistence completes');
    await saved;
    final prefs = await SharedPreferences.getInstance();
    final data = jsonDecode(prefs.getString(prefs.getKeys().single)!);
    expect(data['instance_ids'], ['jf-a']);
    final second = await _container(_Auth(_account()), grants);
    expect(second.read(mediaAccessGuideHiddenProvider), isTrue);
    await second.read(mediaAccessGuideHiddenProvider.notifier).setHidden(false);
    final third = await _container(_Auth(_account()), grants);
    expect(third.read(mediaAccessGuideNavigationVisibleProvider), isTrue);
    expect(grants.usersRead, isEmpty,
        reason: 'requesters use config, not the admin endpoint');
  });

  test('a grant added while the app was closed clears the saved dismissal',
      () async {
    final first = await _container(_Auth(_account()), _Grants());
    await first.read(mediaAccessGuideHiddenProvider.notifier).setHidden(true);
    final reopened = await _container(
        _Auth(_account(instances: [_jellyfin, _secondJellyfin])), _Grants());
    expect(reopened.read(mediaAccessGuideNavigationVisibleProvider), isTrue);
  });

  test('server and user identities stay isolated across switching and logout',
      () async {
    final auth = _Auth(_account());
    final container = await _container(auth, _Grants());
    await container
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    for (final other in [
      _account(id: 3),
      _account(server: 'https://other.example'),
      _account(server: 'https://cantinarr.example/another-base-path'),
      const AuthState(),
    ]) {
      auth.publish(other);
      await pumpEventQueue();
      expect(container.read(mediaAccessGuideHiddenProvider), isFalse);
    }
    auth.publish(_account(server: 'https://cantinarr.example/'));
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    auth.publish(_account().copyWith(
        user: const UserProfile(id: 2, username: 'renamed', role: 'user')));
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
  });

  test(
      'new instance of the same service resets and persists until hidden again',
      () async {
    final auth = _Auth(_account());
    final container = await _container(auth, _Grants());
    await container
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    auth.publish(_account(instances: [_jellyfin, _secondJellyfin]));
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideNavigationVisibleProvider), isTrue);
    final restarted = await _container(_Auth(auth.current), _Grants());
    expect(restarted.read(mediaAccessGuideHiddenProvider), isFalse);
    await restarted
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    final hiddenAgain = await _container(_Auth(auth.current), _Grants());
    expect(hiddenAgain.read(mediaAccessGuideHiddenProvider), isTrue);
  });

  test(
      'renames, removal, regrant of an acknowledged ID, and other services do not reset',
      () async {
    final auth = _Auth(_account());
    final container = await _container(auth, _Grants());
    await container
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    for (final instances in <List<ServiceInstance>>[
      [
        const ServiceInstance(
            id: 'jf-a', serviceType: 'jellyfin', name: 'Renamed')
      ],
      [],
      [_jellyfin],
      [
        _jellyfin,
        const ServiceInstance(
            id: 'books', serviceType: 'chaptarr', name: 'Books')
      ],
    ]) {
      auth.publish(_account(instances: instances));
      await pumpEventQueue();
      expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    }
  });

  test(
      'requestable Plex is eligible, and its first grant restores the shortcut',
      () async {
    final auth = _Auth(_account(instances: [], plexRequestable: true));
    final container = await _container(auth, _Grants());
    expect(container.read(mediaAccessGuideNavigationVisibleProvider), isTrue);
    await container
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    auth.publish(_account(instances: const [
      ServiceInstance(id: 'plex', serviceType: 'plex', name: 'Plex'),
    ], plexRequestable: true));
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideNavigationVisibleProvider), isTrue);
    auth.publish(_account(instances: []));
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideNavigationVisibleProvider), isFalse);
  });

  test(
      'admins use only their own media grants and retain them through failures',
      () async {
    final auth =
        _Auth(_account(admin: true, instances: [_jellyfin, _secondJellyfin]));
    final grants = _Grants();
    final container = await _container(auth, grants);
    expect(grants.usersRead, [2]);
    await container
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    grants.grants = {
      'jellyfin': ['jf-a'],
      'chaptarr': ['books']
    };
    await auth.refreshConfig();
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    grants.fail = true;
    await auth.refreshConfig();
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    grants.fail = false;
    await auth.refreshConfig();
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    grants.grants = {
      'jellyfin': ['jf-a', 'jf-b']
    };
    await auth.refreshConfig();
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideNavigationVisibleProvider), isTrue);
  });

  test('an admin can hide before grants load; unknown does not mean empty',
      () async {
    final grants = _Grants()..fail = true;
    final container = await _container(_Auth(_account(admin: true)), grants);
    final preference = container.read(mediaAccessGuideHiddenProvider.notifier);
    await preference.setHidden(true);
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    grants.fail = false;
    await preference.refreshGrants();
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    grants.grants = {
      'jellyfin': ['jf-a', 'jf-b']
    };
    await preference.refreshGrants();
    expect(container.read(mediaAccessGuideHiddenProvider), isFalse);
  });

  test(
      'a late grant response cannot alter another user or their saved preference',
      () async {
    final auth = _Auth(_account(admin: true));
    final grants = _Grants();
    final container = await _container(auth, grants);
    await container
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    final pending = Completer<Map<String, List<String>>>();
    grants.pending = pending;
    final oldRefresh =
        container.read(mediaAccessGuideHiddenProvider.notifier).refreshGrants();
    auth.publish(_account(id: 3));
    await pumpEventQueue();
    await container
        .read(mediaAccessGuideHiddenProvider.notifier)
        .setHidden(true);
    pending.complete({
      'jellyfin': ['jf-a', 'jf-b']
    });
    await oldRefresh;
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    grants.pending = null;
    auth.publish(_account(admin: true));
    await pumpEventQueue();
    expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
  });

  test(
      'a late preference load cannot overwrite a toggle; fast writes keep order',
      () async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(
        'test', jsonEncode({'hidden': false, 'instance_ids': null}));
    final pending = Completer<SharedPreferences>();
    final preference = MediaAccessGuidePreference(
      preferences: pending.future,
      storageKey: 'test',
      grantedInstanceIds: {'jf-a'},
    );
    addTearDown(preference.dispose);
    final writes = [
      preference.setHidden(true),
      preference.setHidden(false),
      preference.setHidden(true)
    ];
    expect(preference.state, isTrue);
    pending.complete(prefs);
    await Future.wait(writes);
    expect(preference.state, isTrue);
    expect(jsonDecode(prefs.getString('test')!)['hidden'], isTrue);
  });

  testWidgets('config events, reconnect and resume refresh the admin grant set',
      (tester) async {
    final auth = _Auth(_account(admin: true));
    final grants = _Grants();
    final socket = _Socket();
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => auth),
      backendClientProvider.overrideWithValue(grants.dio),
      webSocketClientProvider.overrideWith((_) => socket),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    container.listen(mediaAccessGuideHiddenProvider, (_, __) {});
    container.listen(configSyncProvider, (_, __) {});
    await tester.pump(const Duration(milliseconds: 100));
    final preference = container.read(mediaAccessGuideHiddenProvider.notifier);
    await preference.setHidden(true);
    for (final trigger in <void Function()>[
      () =>
          socket.messages.add(const WsEvent(type: 'config_changed', data: {})),
      socket.connect,
      () {
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        tester.binding
            .handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      },
    ]) {
      final before = grants.usersRead.length;
      trigger();
      await tester.pump(const Duration(milliseconds: 100));
      expect(grants.usersRead.length, greaterThan(before));
      expect(container.read(mediaAccessGuideHiddenProvider), isTrue);
    }
    grants.grants = {
      'jellyfin': ['jf-a', 'jf-b']
    };
    socket.messages.add(const WsEvent(type: 'config_changed', data: {}));
    await tester.pump(const Duration(milliseconds: 100));
    expect(container.read(mediaAccessGuideHiddenProvider), isFalse);
  });
}
