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
    expect(_labels(modules), isNot(contains('Monitoring')));
    expect(_labels(modules), isNot(contains('Tautulli')));
    // A granted media server is a guide, never a module.
    expect(_labels(modules), isNot(contains('Jellyfin')));
    expect(_labels(modules), isNot(contains('Home Jellyfin')));
    expect(_labels(modules), isNot(contains('Emby')));
    expect(_labels(modules), isNot(contains('Den Emby')));
    expect(_labels(modules), isNot(contains('Plex')));
    expect(_labels(modules), isNot(contains('Cantina Plex')));
  });

  test('a Tracearr-only server lights Monitoring for admins only', () async {
    for (final isAdmin in [true, false]) {
      final container = ProviderContainer(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_tracearrOnlyState(isAdmin: isAdmin)),
          ),
        ],
      );
      addTearDown(container.dispose);

      await container.read(authProvider.future);
      await container.pump();

      final modules = container.read(moduleProvider).modules;
      final monitoring =
          modules.where((module) => module.type == ModuleType.monitoring);
      if (isAdmin) {
        expect(_labels(modules), contains('Monitoring'));
        expect(monitoring, hasLength(1));
      } else {
        expect(_labels(modules), isNot(contains('Monitoring')));
        expect(monitoring, isEmpty);
      }
      expect(_labels(modules), isNot(contains('Tracearr')));
    }
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
        'Monitoring',
      ]),
    );
    expect(_labels(modules), isNot(contains('Tautulli')));
    expect(
      modules.where((module) => module.type == ModuleType.radarr),
      hasLength(1),
    );
    expect(
      modules.where((module) => module.type == ModuleType.downloads),
      hasLength(1),
    );
    // Media servers have no module for admins either: the arrs stay the
    // source of library truth, and the guide is the media server's surface.
    expect(_labels(modules), isNot(contains('Jellyfin')));
    expect(_labels(modules), isNot(contains('Home Jellyfin')));
    expect(_labels(modules), isNot(contains('Emby')));
    expect(_labels(modules), isNot(contains('Den Emby')));
    expect(_labels(modules), isNot(contains('Plex')));
    expect(_labels(modules), isNot(contains('Cantina Plex')));
  });
}

List<String> _labels(List<AppModule> modules) =>
    modules.map((module) => module.label).toList();

/// A server whose only watch-history source is Tracearr: the Monitoring row
/// must light up for admins from that alone, and never for requesters.
AuthState _tracearrOnlyState({required bool isAdmin}) => AuthState(
      connection: const BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        instances: [
          ServiceInstance(
            id: 'tracearr-main',
            serviceType: 'tracearr',
            name: 'Tracearr',
            isDefault: true,
          ),
        ],
      ),
      user: UserProfile(
        id: 1,
        username: isAdmin ? 'admin' : 'viewer',
        role: isAdmin ? 'admin' : 'user',
      ),
    );

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
        ServiceInstance(
          id: 'jf-a',
          serviceType: 'jellyfin',
          name: 'Home Jellyfin',
        ),
        ServiceInstance(
          id: 'em-a',
          serviceType: 'emby',
          name: 'Den Emby',
        ),
        ServiceInstance(
          id: 'px-a',
          serviceType: 'plex',
          name: 'Cantina Plex',
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
