import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/backend_connection.dart';
import '../../../core/models/app_module.dart';
import '../../../core/models/user_profile.dart';
import '../../../core/providers/instance_provider.dart';
import '../../auth/logic/auth_provider.dart';

/// Media identity, service mapping, and fixed router branch for each catalog.
const discoverCatalogs = [
  (
    mediaType: 'movie',
    serviceType: 'radarr',
    serviceName: 'Radarr',
    label: 'Movies',
    branch: 0
  ),
  (
    mediaType: 'tv',
    serviceType: 'sonarr',
    serviceName: 'Sonarr',
    label: 'TV Shows',
    branch: 1
  ),
  (
    mediaType: 'book',
    serviceType: 'chaptarr',
    serviceName: 'Chaptarr',
    label: 'Books',
    branch: 3
  ),
  (
    mediaType: 'music',
    serviceType: 'lidarr',
    serviceName: 'Lidarr',
    label: 'Music',
    branch: 4
  ),
];

const discoverVisibilityUpdateMessage =
    'Update your Cantinarr server to hide Discover tabs.';

const adminCatalogUpdateMessage =
    'Update your Cantinarr server to browse this catalog before connecting a service.';

/// Browsing permission and a usable library are separate capabilities.
class DiscoveryAccess {
  final UserProfile? user;
  final BackendConnection? connection;
  final InstanceState instances;
  const DiscoveryAccess(this.user, this.connection, this.instances);

  bool get isAdmin => user?.isAdmin ?? false;
  bool isVisible(String mediaType) =>
      !(connection?.hiddenDiscoverTabs?.contains(mediaType) ?? false) &&
      switch (mediaType) {
        'book' => isAdmin || (connection?.services.chaptarr ?? false),
        'music' => isAdmin || (connection?.services.lidarr ?? false),
        _ => true,
      };
  bool get showBooks => isVisible('book');
  bool get showMusic => isVisible('music');
  bool get showReleases => isVisible('movie') || isVisible('tv') || showMusic;

  /// One calculation for sidebar, mobile tabs, routes, and catalog warmup.
  List<int> get visibleBranches => [
        if (isVisible('movie')) 0,
        if (isVisible('tv')) 1,
        if (showReleases) 2,
        if (showBooks) 3,
        if (showMusic) 4,
      ];
  List<ModulePage> get pages {
    final all = modulePagesFor(ModuleType.dashboard,
        includeBooks: true, includeMusic: true);
    return [for (final index in visibleBranches) all[index]];
  }

  String get landingRoute => pages.firstOrNull?.route ?? '/dashboard';

  bool needsSetup(String serviceType) =>
      isAdmin &&
      (connection?.configConfirmed ?? false) &&
      !connection!.instances.any((i) => i.serviceType == serviceType);

  String? activeId(String serviceType) => switch (serviceType) {
        'radarr' => instances.activeRadarrInstance?.id,
        'sonarr' => instances.activeSonarrInstance?.id,
        'chaptarr' => instances.activeChaptarrInstance?.id,
        'lidarr' => instances.activeLidarrInstance?.id,
        _ => null,
      };

  bool hasInstance(String serviceType, String? id) =>
      id != null &&
      (connection?.instances
              .any((i) => i.id == id && i.serviceType == serviceType) ??
          false);

  bool canBrowse(String serviceType, String? id) =>
      (user?.hasPermission('media:discover') ?? false) &&
      (id == null
          ? isAdmin && (connection?.adminCatalogBrowsing ?? false)
          : hasInstance(serviceType, id));

  bool needsUpdate(String? id) =>
      isAdmin && id == null && !(connection?.adminCatalogBrowsing ?? false);

  /// Catalog/status state must never survive a change of account or access.
  String get scope {
    final ids =
        connection?.instances.map((i) => '${i.serviceType}:${i.id}').toList() ??
            <String>[];
    ids.sort();
    return '${connection?.serverUrl}|${user?.id}|${user?.role}|${user?.child}|${user?.permissions.join(',')}|${connection?.adminCatalogBrowsing}|${ids.join(',')}|${connection?.hiddenDiscoverTabs?.join(',')}';
  }
}

final discoveryAccessProvider = Provider((ref) {
  final auth = ref.watch(authProvider).valueOrNull;
  return DiscoveryAccess(
      auth?.user, auth?.connection, ref.watch(instanceProvider));
});

final catalogDiscoveryScopeProvider = Provider((ref) =>
    ref.watch(discoveryAccessProvider.select((access) => access.scope)));
