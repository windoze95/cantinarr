import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

const _sab = ServiceInstance(
  id: 'sabnzbd-a',
  serviceType: 'sabnzbd',
  name: 'SABnzbd',
);
const _qbit = ServiceInstance(
  id: 'qbittorrent-a',
  serviceType: 'qbittorrent',
  name: 'qBittorrent',
  isDefault: true,
);

AuthState _stateWith(List<ServiceInstance> instances) => AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        instances: instances,
      ),
      user: const UserProfile(id: 1, username: 'admin', role: 'admin'),
    );

Future<ProviderContainer> _containerWith(
    List<ServiceInstance> instances) async {
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _FakeAuthNotifier(_stateWith(instances))),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  return container;
}

void main() {
  test('several download clients default to the aggregate All view', () async {
    final container = await _containerWith(const [_sab, _qbit]);
    final state = container.read(instanceProvider);

    expect(state.activeDownloadInstanceId, allDownloadInstancesId);
    expect(state.allDownloadsActive, isTrue);
    // No single instance is active in the aggregate view.
    expect(state.activeDownloadInstance, isNull);
  });

  test('a lone download client stays its own default', () async {
    final container = await _containerWith(const [_qbit]);
    final state = container.read(instanceProvider);

    expect(state.activeDownloadInstanceId, 'qbittorrent-a');
    expect(state.allDownloadsActive, isFalse);
    expect(state.activeDownloadInstance?.id, 'qbittorrent-a');
  });

  test('selecting a client and re-selecting All round-trips', () async {
    final container = await _containerWith(const [_sab, _qbit]);
    final notifier = container.read(instanceProvider.notifier);

    notifier.setActiveDownloadInstance('sabnzbd-a');
    var state = container.read(instanceProvider);
    expect(state.allDownloadsActive, isFalse);
    expect(state.activeDownloadInstance?.id, 'sabnzbd-a');

    notifier.setActiveDownloadInstance(allDownloadInstancesId);
    state = container.read(instanceProvider);
    expect(state.allDownloadsActive, isTrue);
    expect(state.activeDownloadInstance, isNull);
  });

  test('the All sentinel with a lone client falls back to that client', () {
    const state = InstanceState(
      downloadInstances: [_qbit],
      activeDownloadInstanceId: allDownloadInstancesId,
    );

    expect(state.allDownloadsActive, isFalse);
    expect(state.activeDownloadInstance?.id, 'qbittorrent-a');
  });
}
