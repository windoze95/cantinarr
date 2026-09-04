import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/app_module.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/instance_dropdown.dart';
import '../../../core/widgets/module_scaffold.dart';

/// Monitoring module shell: Activity | History | Stats.
/// Pages render as a bottom nav on mobile and sidebar items on desktop.
/// Shows an instance dropdown in the header when 2+ watch-history
/// (Tautulli or Tracearr) instances exist.
class MonitoringModuleShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const MonitoringModuleShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceState = ref.watch(instanceProvider);

    return ModuleScaffold(
      appBar: instanceState.watchHistoryInstances.length > 1
          ? AppBar(
              title: InstanceDropdown(
                instances: instanceState.watchHistoryInstances,
                activeInstanceId: instanceState.activeWatchHistoryInstanceId,
                onChanged: (id) => ref
                    .read(instanceProvider.notifier)
                    .setActiveWatchHistoryInstance(id),
              ),
              backgroundColor: AppTheme.background,
              elevation: 0,
            )
          : null,
      pages: modulePagesFor(ModuleType.monitoring),
      currentIndex: currentIndex,
      onTabChanged: onTabChanged,
      child: child,
    );
  }
}
