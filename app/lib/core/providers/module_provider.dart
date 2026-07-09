import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../features/auth/logic/auth_provider.dart';
import '../models/app_module.dart';
import '../models/backend_connection.dart';

/// Derives the list of available modules from the current auth state.
class ModuleState {
  final List<AppModule> modules;
  final ModuleType activeModuleType;
  final String? activeInstanceId;

  const ModuleState({
    this.modules = const [],
    this.activeModuleType = ModuleType.dashboard,
    this.activeInstanceId,
  });

  ModuleState copyWith({
    List<AppModule>? modules,
    ModuleType? activeModuleType,
    String? activeInstanceId,
    bool clearInstanceId = false,
  }) =>
      ModuleState(
        modules: modules ?? this.modules,
        activeModuleType: activeModuleType ?? this.activeModuleType,
        activeInstanceId: clearInstanceId
            ? null
            : (activeInstanceId ?? this.activeInstanceId),
      );

  int get activeIndex {
    for (int i = 0; i < modules.length; i++) {
      final m = modules[i];
      if (m.type == activeModuleType &&
          (m.instanceId == null || m.instanceId == activeInstanceId)) {
        return i;
      }
    }
    return 0;
  }
}

class ModuleNotifier extends Notifier<ModuleState> {
  @override
  ModuleState build() {
    final auth = ref.watch(authProvider).valueOrNull;
    final connection = auth?.connection;
    final isAdmin = auth?.user?.isAdmin ?? false;
    final modules = _buildModules(connection, isAdmin: isAdmin);

    return ModuleState(modules: modules);
  }

  List<AppModule> _buildModules(BackendConnection? connection,
      {bool isAdmin = false}) {
    final modules = <AppModule>[];

    // Discover (the browse/home surface) is always available. Kept internally
    // as ModuleType.dashboard / the /dashboard/* routes; only the user-facing
    // label reads "Discover".
    modules.add(const AppModule(
      type: ModuleType.dashboard,
      label: 'Discover',
      icon: Icons.explore,
    ));

    if (connection != null) {
      if (isAdmin && connection.radarrInstances.isNotEmpty) {
        modules.add(const AppModule(
          type: ModuleType.radarr,
          label: 'Radarr',
          icon: Icons.movie,
        ));
      }

      if (isAdmin && connection.sonarrInstances.isNotEmpty) {
        modules.add(const AppModule(
          type: ModuleType.sonarr,
          label: 'Sonarr',
          icon: Icons.tv,
        ));
      }

      if (isAdmin && connection.chaptarrInstances.isNotEmpty) {
        modules.add(const AppModule(
          type: ModuleType.chaptarr,
          label: 'Chaptarr',
          icon: Icons.menu_book,
        ));
      }

      if (isAdmin && connection.downloadInstances.isNotEmpty) {
        modules.add(const AppModule(
          type: ModuleType.downloads,
          label: 'Downloads',
          icon: Icons.download,
        ));
      }

      if (isAdmin && connection.tautulliInstances.isNotEmpty) {
        modules.add(const AppModule(
          type: ModuleType.tautulli,
          label: 'Tautulli',
          icon: Icons.monitor_heart,
        ));
      }

      // AI Assistant
      if (connection.services.ai) {
        modules.add(const AppModule(
          type: ModuleType.assistant,
          label: 'AI Assistant',
          icon: Icons.smart_toy,
        ));
      }
    }

    return modules;
  }

  void setActiveModule(ModuleType type, {String? instanceId}) {
    state = state.copyWith(
      activeModuleType: type,
      activeInstanceId: instanceId,
      clearInstanceId: instanceId == null,
    );
  }
}

final moduleProvider =
    NotifierProvider<ModuleNotifier, ModuleState>(ModuleNotifier.new);
