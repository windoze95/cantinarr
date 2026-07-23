import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _serverA = 'https://server-a.example.com';
const _serverB = 'https://server-b.example.com';

void main() {
  test('same-user token refresh preserves all selected media instances',
      () async {
    final auth = _MutableAuthNotifier(_authState());
    final container = _container(auth);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    _selectAlternates(container);

    auth.setAuth(_authState(
      accessToken: 'refreshed-access',
      refreshToken: 'refreshed-refresh',
      username: 'renamed-admin',
    ));
    await container.pump();

    _expectAlternateSelections(container);
  });

  test('removed selections fall back and are not resurrected', () async {
    final auth = _MutableAuthNotifier(_authState());
    final container = _container(auth);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    _selectAlternates(container);

    auth.setAuth(_authState(instances: _defaultInstances));
    await container.pump();

    _expectDefaultSelections(container);

    auth.setAuth(_authState());
    await container.pump();

    _expectDefaultSelections(container);
  });

  test('same-server user switch resets selections even when IDs overlap',
      () async {
    final auth = _MutableAuthNotifier(_authState());
    final container = _container(auth);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    _selectAlternates(container);

    auth.setAuth(_authState(userId: 2, username: 'other-user'));
    await container.pump();

    _expectDefaultSelections(container);
  });

  test('server switch resets selections even when instance IDs overlap',
      () async {
    final auth = _MutableAuthNotifier(_authState());
    final container = _container(auth);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    _selectAlternates(container);

    auth.setAuth(_authState(serverUrl: _serverB));
    await container.pump();

    _expectDefaultSelections(container);
  });

  test('logout clears selections before the same account signs in again',
      () async {
    final auth = _MutableAuthNotifier(_authState());
    final container = _container(auth);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    _selectAlternates(container);

    auth.setAuth(const AuthState());
    await container.pump();

    final loggedOut = container.read(instanceProvider);
    expect(loggedOut.radarrInstances, isEmpty);
    expect(loggedOut.sonarrInstances, isEmpty);
    expect(loggedOut.chaptarrInstances, isEmpty);

    auth.setAuth(_authState());
    await container.pump();

    _expectDefaultSelections(container);
  });
}

ProviderContainer _container(_MutableAuthNotifier auth) => ProviderContainer(
      overrides: [
        authProvider.overrideWith(() => auth),
      ],
    );

void _selectAlternates(ProviderContainer container) {
  final notifier = container.read(instanceProvider.notifier);
  notifier.setActiveRadarrInstance('radarr-alt');
  notifier.setActiveSonarrInstance('sonarr-alt');
  notifier.setActiveChaptarrInstance('chaptarr-alt');
  _expectAlternateSelections(container);
}

void _expectAlternateSelections(ProviderContainer container) {
  final instances = container.read(instanceProvider);
  expect(instances.activeRadarrInstanceId, 'radarr-alt');
  expect(instances.activeSonarrInstanceId, 'sonarr-alt');
  expect(instances.activeChaptarrInstanceId, 'chaptarr-alt');
}

void _expectDefaultSelections(ProviderContainer container) {
  final instances = container.read(instanceProvider);
  expect(instances.activeRadarrInstanceId, 'radarr-main');
  expect(instances.activeSonarrInstanceId, 'sonarr-main');
  expect(instances.activeChaptarrInstanceId, 'chaptarr-main');
}

AuthState _authState({
  String serverUrl = _serverA,
  int userId = 1,
  String username = 'admin',
  String accessToken = 'access',
  String refreshToken = 'refresh',
  List<ServiceInstance> instances = _allInstances,
}) =>
    AuthState(
      connection: BackendConnection(
        serverUrl: serverUrl,
        accessToken: accessToken,
        refreshToken: refreshToken,
        instances: instances,
      ),
      user: UserProfile(
        id: userId,
        username: username,
        role: 'admin',
      ),
    );

const _defaultInstances = [
  ServiceInstance(
    id: 'radarr-main',
    serviceType: 'radarr',
    name: 'Main Radarr',
    isDefault: true,
  ),
  ServiceInstance(
    id: 'sonarr-main',
    serviceType: 'sonarr',
    name: 'Main Sonarr',
    isDefault: true,
  ),
  ServiceInstance(
    id: 'chaptarr-main',
    serviceType: 'chaptarr',
    name: 'Main Chaptarr',
    isDefault: true,
  ),
];

const _allInstances = [
  ..._defaultInstances,
  ServiceInstance(
    id: 'radarr-alt',
    serviceType: 'radarr',
    name: 'Alternate Radarr',
  ),
  ServiceInstance(
    id: 'sonarr-alt',
    serviceType: 'sonarr',
    name: 'Alternate Sonarr',
  ),
  ServiceInstance(
    id: 'chaptarr-alt',
    serviceType: 'chaptarr',
    name: 'Alternate Chaptarr',
  ),
];

class _MutableAuthNotifier extends AuthNotifier {
  _MutableAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;

  void setAuth(AuthState value) => state = AsyncData(value);
}
