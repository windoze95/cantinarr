import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../features/auth/logic/auth_provider.dart';
import '../models/backend_connection.dart';

/// Tracks available instances and which is currently active per service type.
class InstanceState {
  final List<ServiceInstance> radarrInstances;
  final List<ServiceInstance> sonarrInstances;
  final String? activeRadarrInstanceId;
  final String? activeSonarrInstanceId;

  const InstanceState({
    this.radarrInstances = const [],
    this.sonarrInstances = const [],
    this.activeRadarrInstanceId,
    this.activeSonarrInstanceId,
  });

  InstanceState copyWith({
    List<ServiceInstance>? radarrInstances,
    List<ServiceInstance>? sonarrInstances,
    String? activeRadarrInstanceId,
    String? activeSonarrInstanceId,
  }) =>
      InstanceState(
        radarrInstances: radarrInstances ?? this.radarrInstances,
        sonarrInstances: sonarrInstances ?? this.sonarrInstances,
        activeRadarrInstanceId:
            activeRadarrInstanceId ?? this.activeRadarrInstanceId,
        activeSonarrInstanceId:
            activeSonarrInstanceId ?? this.activeSonarrInstanceId,
      );

  /// Get the active Radarr instance, falling back to default.
  ServiceInstance? get activeRadarrInstance {
    if (radarrInstances.isEmpty) return null;
    if (activeRadarrInstanceId != null) {
      final found = radarrInstances
          .where((i) => i.id == activeRadarrInstanceId)
          .toList();
      if (found.isNotEmpty) return found.first;
    }
    return radarrInstances.firstWhere((i) => i.isDefault,
        orElse: () => radarrInstances.first);
  }

  /// Get the active Sonarr instance, falling back to default.
  ServiceInstance? get activeSonarrInstance {
    if (sonarrInstances.isEmpty) return null;
    if (activeSonarrInstanceId != null) {
      final found = sonarrInstances
          .where((i) => i.id == activeSonarrInstanceId)
          .toList();
      if (found.isNotEmpty) return found.first;
    }
    return sonarrInstances.firstWhere((i) => i.isDefault,
        orElse: () => sonarrInstances.first);
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

    return InstanceState(
      radarrInstances: radarr,
      sonarrInstances: sonarr,
      activeRadarrInstanceId:
          radarr.isNotEmpty ? (radarr.firstWhere((i) => i.isDefault, orElse: () => radarr.first)).id : null,
      activeSonarrInstanceId:
          sonarr.isNotEmpty ? (sonarr.firstWhere((i) => i.isDefault, orElse: () => sonarr.first)).id : null,
    );
  }

  void setActiveRadarrInstance(String instanceId) {
    state = state.copyWith(activeRadarrInstanceId: instanceId);
  }

  void setActiveSonarrInstance(String instanceId) {
    state = state.copyWith(activeSonarrInstanceId: instanceId);
  }
}

final instanceProvider =
    NotifierProvider<InstanceNotifier, InstanceState>(InstanceNotifier.new);
