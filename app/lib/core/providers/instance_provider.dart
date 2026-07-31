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
  final List<ServiceInstance> downloadInstances;
  final List<ServiceInstance> tautulliInstances;
  final String? activeRadarrInstanceId;
  final String? activeSonarrInstanceId;
  final String? activeChaptarrInstanceId;
  final String? activeDownloadInstanceId;
  final String? activeTautulliInstanceId;

  const InstanceState({
    this.radarrInstances = const [],
    this.sonarrInstances = const [],
    this.chaptarrInstances = const [],
    this.downloadInstances = const [],
    this.tautulliInstances = const [],
    this.activeRadarrInstanceId,
    this.activeSonarrInstanceId,
    this.activeChaptarrInstanceId,
    this.activeDownloadInstanceId,
    this.activeTautulliInstanceId,
  });

  InstanceState copyWith({
    List<ServiceInstance>? radarrInstances,
    List<ServiceInstance>? sonarrInstances,
    List<ServiceInstance>? chaptarrInstances,
    List<ServiceInstance>? downloadInstances,
    List<ServiceInstance>? tautulliInstances,
    String? activeRadarrInstanceId,
    String? activeSonarrInstanceId,
    String? activeChaptarrInstanceId,
    String? activeDownloadInstanceId,
    String? activeTautulliInstanceId,
  }) =>
      InstanceState(
        radarrInstances: radarrInstances ?? this.radarrInstances,
        sonarrInstances: sonarrInstances ?? this.sonarrInstances,
        chaptarrInstances: chaptarrInstances ?? this.chaptarrInstances,
        downloadInstances: downloadInstances ?? this.downloadInstances,
        tautulliInstances: tautulliInstances ?? this.tautulliInstances,
        activeRadarrInstanceId:
            activeRadarrInstanceId ?? this.activeRadarrInstanceId,
        activeSonarrInstanceId:
            activeSonarrInstanceId ?? this.activeSonarrInstanceId,
        activeChaptarrInstanceId:
            activeChaptarrInstanceId ?? this.activeChaptarrInstanceId,
        activeDownloadInstanceId:
            activeDownloadInstanceId ?? this.activeDownloadInstanceId,
        activeTautulliInstanceId:
            activeTautulliInstanceId ?? this.activeTautulliInstanceId,
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

  /// Get the active Tautulli instance, falling back to default.
  ServiceInstance? get activeTautulliInstance {
    if (tautulliInstances.isEmpty) return null;
    if (activeTautulliInstanceId != null) {
      final found = tautulliInstances
          .where((i) => i.id == activeTautulliInstanceId)
          .toList();
      if (found.isNotEmpty) return found.first;
    }
    return tautulliInstances.firstWhere((i) => i.isDefault,
        orElse: () => tautulliInstances.first);
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
    final downloads = connection.downloadInstances;
    final tautulli = connection.tautulliInstances;

    return InstanceState(
      radarrInstances: radarr,
      sonarrInstances: sonarr,
      chaptarrInstances: chaptarr,
      downloadInstances: downloads,
      tautulliInstances: tautulli,
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
      // With several download clients the aggregate "All" view is the
      // default; a lone client is its own default (All is never offered).
      activeDownloadInstanceId: downloads.isEmpty
          ? null
          : downloads.length > 1
              ? allDownloadInstancesId
              : downloads.first.id,
      activeTautulliInstanceId: tautulli.isNotEmpty
          ? (tautulli.firstWhere((i) => i.isDefault,
              orElse: () => tautulli.first)).id
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

  void setActiveDownloadInstance(String instanceId) {
    state = state.copyWith(activeDownloadInstanceId: instanceId);
  }

  void setActiveTautulliInstance(String instanceId) {
    state = state.copyWith(activeTautulliInstanceId: instanceId);
  }
}

final instanceProvider =
    NotifierProvider<InstanceNotifier, InstanceState>(InstanceNotifier.new);
