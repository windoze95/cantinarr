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
        activeInstanceId:
            clearInstanceId ? null : (activeInstanceId ?? this.activeInstanceId),
      );

  int get activeIndex {
    for (int i = 0; i < modules.length; i++) {
      final m = modules[i];
      if (m.type == activeModuleType && m.instanceId == activeInstanceId) {
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
    final modules = _buildModules(connection);

    return ModuleState(modules: modules);
  }

  List<AppModule> _buildModules(BackendConnection? connection) {
    final modules = <AppModule>[];

    // Dashboard is always available
    modules.add(const AppModule(
      type: ModuleType.dashboard,
      label: 'Dashboard',
      icon: Icons.dashboard,
    ));

    if (connection != null) {
      // Radarr instances
      for (final inst in connection.radarrInstances) {
        modules.add(AppModule(
          type: ModuleType.radarr,
          label: inst.name,
          icon: Icons.movie,
          instanceId: inst.id,
          instanceName: inst.name,
        ));
      }

      // Sonarr instances
      for (final inst in connection.sonarrInstances) {
        modules.add(AppModule(
          type: ModuleType.sonarr,
          label: inst.name,
          icon: Icons.tv,
          instanceId: inst.id,
          instanceName: inst.name,
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
