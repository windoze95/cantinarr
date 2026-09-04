import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../features/auth/logic/auth_provider.dart';
import '../models/backend_connection.dart';

/// Sentinel "instance id" meaning the aggregate view across every download
/// client. Real instance ids are always `<serviceType>-<uuid8>`, so this can
/// never collide with one. It is the default selection whenever two or more
/// download clients exist.
const String allDownloadInstancesId = 'all';

/// Tracks available instances and which is currently active per service type.
class InstanceState {
  final List<ServiceInstance> radarrInstances;
  final List<ServiceInstance> sonarrInstances;
  final List<ServiceInstance> chaptarrInstances;
  final List<ServiceInstance> lidarrInstances;
  final List<ServiceInstance> downloadInstances;
  final List<ServiceInstance> watchHistoryInstances;
  final String? activeRadarrInstanceId;
  final String? activeSonarrInstanceId;
  final String? activeChaptarrInstanceId;
  final String? activeLidarrInstanceId;
  final String? activeDownloadInstanceId;
  final String? activeWatchHistoryInstanceId;

  const InstanceState({
    this.radarrInstances = const [],
    this.sonarrInstances = const [],
    this.chaptarrInstances = const [],
    this.lidarrInstances = const [],
    this.downloadInstances = const [],
    this.watchHistoryInstances = const [],
    this.activeRadarrInstanceId,
    this.activeSonarrInstanceId,
    this.activeChaptarrInstanceId,
    this.activeLidarrInstanceId,
    this.activeDownloadInstanceId,
    this.activeWatchHistoryInstanceId,
  });

  InstanceState copyWith({
    List<ServiceInstance>? radarrInstances,
    List<ServiceInstance>? sonarrInstances,
    List<ServiceInstance>? chaptarrInstances,
    List<ServiceInstance>? lidarrInstances,
    List<ServiceInstance>? downloadInstances,
    List<ServiceInstance>? watchHistoryInstances,
    String? activeRadarrInstanceId,
    String? activeSonarrInstanceId,
    String? activeChaptarrInstanceId,
    String? activeLidarrInstanceId,
    String? activeDownloadInstanceId,
    String? activeWatchHistoryInstanceId,
  }) =>
      InstanceState(
        radarrInstances: radarrInstances ?? this.radarrInstances,
        sonarrInstances: sonarrInstances ?? this.sonarrInstances,
        chaptarrInstances: chaptarrInstances ?? this.chaptarrInstances,
        lidarrInstances: lidarrInstances ?? this.lidarrInstances,
        downloadInstances: downloadInstances ?? this.downloadInstances,
        watchHistoryInstances: watchHistoryInstances ?? this.watchHistoryInstances,
        activeRadarrInstanceId:
            activeRadarrInstanceId ?? this.activeRadarrInstanceId,
        activeSonarrInstanceId:
            activeSonarrInstanceId ?? this.activeSonarrInstanceId,
        activeChaptarrInstanceId:
            activeChaptarrInstanceId ?? this.activeChaptarrInstanceId,
        activeLidarrInstanceId:
            activeLidarrInstanceId ?? this.activeLidarrInstanceId,
        activeDownloadInstanceId:
            activeDownloadInstanceId ?? this.activeDownloadInstanceId,
        activeWatchHistoryInstanceId:
            activeWatchHistoryInstanceId ?? this.activeWatchHistoryInstanceId,
      );

  /// Get the active Radarr instance, falling back to default.
  ServiceInstance? get activeRadarrInstance {
    if (radarrInstances.isEmpty) return null;
    if (activeRadarrInstanceId != null) {
      final found =
          radarrInstances.where((i) => i.id == activeRadarrInstanceId).toList();
      if (found.isNotEmpty) return found.first;
    }
    return radarrInstances.firstWhere((i) => i.isDefault,
        orElse: () => radarrInstances.first);
  }

  /// Get the active Sonarr instance, falling back to default.
  ServiceInstance? get activeSonarrInstance {
    if (sonarrInstances.isEmpty) return null;
    if (activeSonarrInstanceId != null) {
      final found =
          sonarrInstances.where((i) => i.id == activeSonarrInstanceId).toList();
      if (found.isNotEmpty) return found.first;
    }
    return sonarrInstances.firstWhere((i) => i.isDefault,
        orElse: () => sonarrInstances.first);
  }

  /// Get the active Chaptarr instance, falling back to default.
  ServiceInstance? get activeChaptarrInstance {
    if (chaptarrInstances.isEmpty) return null;
    if (activeChaptarrInstanceId != null) {
      final found = chaptarrInstances
          .where((i) => i.id == activeChaptarrInstanceId)
          .toList();
      if (found.isNotEmpty) return found.first;
    }
    return chaptarrInstances.firstWhere((i) => i.isDefault,
        orElse: () => chaptarrInstances.first);
  }

  /// Get the active Lidarr instance, falling back to default.
  ServiceInstance? get activeLidarrInstance {
    if (lidarrInstances.isEmpty) return null;
    if (activeLidarrInstanceId != null) {
      final found =
          lidarrInstances.where((i) => i.id == activeLidarrInstanceId).toList();
      if (found.isNotEmpty) return found.first;
    }
    return lidarrInstances.firstWhere((i) => i.isDefault,
        orElse: () => lidarrInstances.first);
  }

  /// Whether the aggregate "All" downloads view is active. Only meaningful
  /// with two or more clients; with a single client the aggregate would be
  /// identical to that client's view, so it is never offered.
  bool get allDownloadsActive =>
      downloadInstances.length > 1 &&
      activeDownloadInstanceId == allDownloadInstancesId;

  /// Get the active download client instance, falling back to default.
  /// Null when no clients exist or when the aggregate "All" view is active —
  /// there is no single active instance then.
  ServiceInstance? get activeDownloadInstance {
    if (downloadInstances.isEmpty) return null;
    if (allDownloadsActive) return null;
    if (activeDownloadInstanceId != null) {
      final found = downloadInstances
          .where((i) => i.id == activeDownloadInstanceId)
          .toList();
      if (found.isNotEmpty) return found.first;
    }
    return downloadInstances.firstWhere((i) => i.isDefault,
        orElse: () => downloadInstances.first);
  }

  /// Get the active watch-history (Tautulli or Tracearr) instance, falling
  /// back to the first default in server order.
  ServiceInstance? get activeWatchHistoryInstance {
    if (watchHistoryInstances.isEmpty) return null;
    if (activeWatchHistoryInstanceId != null) {
      final found = watchHistoryInstances
          .where((i) => i.id == activeWatchHistoryInstanceId)
          .toList();
      if (found.isNotEmpty) return found.first;
    }
    return watchHistoryInstances.firstWhere((i) => i.isDefault,
        orElse: () => watchHistoryInstances.first);
  }
}

class InstanceNotifier extends Notifier<InstanceState> {
  @override
  InstanceState build() {
    final auth = ref.watch(authProvider).valueOrNull;
    final connection = auth?.connection;
    if (connection == null) return const InstanceState();

    final radarr = connection.radarrInstances;
    final sonarr = connection.sonarrInstances;
    final chaptarr = connection.chaptarrInstances;
    final lidarr = connection.lidarrInstances;
    final downloads = connection.downloadInstances;
    final watchHistory = connection.watchHistoryInstances;

    return InstanceState(
      radarrInstances: radarr,
      sonarrInstances: sonarr,
      chaptarrInstances: chaptarr,
      lidarrInstances: lidarr,
      downloadInstances: downloads,
      watchHistoryInstances: watchHistory,
      activeRadarrInstanceId: radarr.isNotEmpty
          ? (radarr.firstWhere((i) => i.isDefault, orElse: () => radarr.first))
              .id
          : null,
      activeSonarrInstanceId: sonarr.isNotEmpty
          ? (sonarr.firstWhere((i) => i.isDefault, orElse: () => sonarr.first))
              .id
          : null,
      activeChaptarrInstanceId: chaptarr.isNotEmpty
          ? (chaptarr.firstWhere((i) => i.isDefault,
                  orElse: () => chaptarr.first))
              .id
          : null,
      activeLidarrInstanceId: lidarr.isNotEmpty
          ? (lidarr.firstWhere((i) => i.isDefault, orElse: () => lidarr.first))
              .id
          : null,
      // With several download clients the aggregate "All" view is the
      // default; a lone client is its own default (All is never offered).
      activeDownloadInstanceId: downloads.isEmpty
          ? null
          : downloads.length > 1
              ? allDownloadInstancesId
              : downloads.first.id,
      activeWatchHistoryInstanceId: watchHistory.isNotEmpty
          ? (watchHistory.firstWhere((i) => i.isDefault,
              orElse: () => watchHistory.first)).id
          : null,
    );
  }

  void setActiveRadarrInstance(String instanceId) {
    state = state.copyWith(activeRadarrInstanceId: instanceId);
  }

  void setActiveSonarrInstance(String instanceId) {
    state = state.copyWith(activeSonarrInstanceId: instanceId);
  }

  void setActiveChaptarrInstance(String instanceId) {
    state = state.copyWith(activeChaptarrInstanceId: instanceId);
  }

  void setActiveLidarrInstance(String instanceId) {
    state = state.copyWith(activeLidarrInstanceId: instanceId);
  }

  void setActiveDownloadInstance(String instanceId) {
    state = state.copyWith(activeDownloadInstanceId: instanceId);
  }

  void setActiveWatchHistoryInstance(String instanceId) {
    state = state.copyWith(activeWatchHistoryInstanceId: instanceId);
  }
}

final instanceProvider =
    NotifierProvider<InstanceNotifier, InstanceState>(InstanceNotifier.new);
