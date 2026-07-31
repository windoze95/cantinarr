import 'package:cantinarr/core/models/app_module.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/providers/module_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('non-admin module navigation is app-level and hides admin modules',
      () async {
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_authState(isAdmin: false)),
        ),
      ],
    );
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    await container.pump();

    final modules = container.read(moduleProvider).modules;
    expect(_labels(modules), ['Discover']);
    expect(_labels(modules), isNot(contains('Main Radarr')));
    expect(_labels(modules), isNot(contains('4K Radarr')));
    expect(_labels(modules), isNot(contains('Radarr')));
    expect(_labels(modules), isNot(contains('Sonarr')));
    expect(_labels(modules), isNot(contains('Chaptarr')));
    expect(_labels(modules), isNot(contains('Downloads')));
    expect(_labels(modules), isNot(contains('Tautulli')));
  });

  test('admin module navigation is one row per app type', () async {
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_authState(isAdmin: true)),
        ),
      ],
    );
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    await container.pump();

    final modules = container.read(moduleProvider).modules;
    expect(
      _labels(modules),
      containsAll([
        'Discover',
        'Radarr',
        'Sonarr',
        'Chaptarr',
        'Downloads',
        'Tautulli',
      ]),
    );
    expect(
      modules.where((module) => module.type == ModuleType.radarr),
      hasLength(1),
    );
    expect(
      modules.where((module) => module.type == ModuleType.downloads),
      hasLength(1),
    );
  });
}

List<String> _labels(List<AppModule> modules) =>
    modules.map((module) => module.label).toList();

AuthState _authState({required bool isAdmin}) {
  return AuthState(
    connection: const BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(
          id: 'radarr-main',
          serviceType: 'radarr',
          name: 'Main Radarr',
          isDefault: true,
        ),
        ServiceInstance(
          id: 'radarr-4k',
          serviceType: 'radarr',
          name: '4K Radarr',
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
          name: 'Books',
          isDefault: true,
        ),
        ServiceInstance(
          id: 'sab-main',
          serviceType: 'sabnzbd',
          name: 'Downloads',
        ),
        ServiceInstance(
          id: 'tautulli-main',
          serviceType: 'tautulli',
          name: 'Tautulli',
        ),
      ],
    ),
    user: UserProfile(
      id: 1,
      username: isAdmin ? 'admin' : 'viewer',
      role: isAdmin ? 'admin' : 'user',
    ),
  );
}

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}
