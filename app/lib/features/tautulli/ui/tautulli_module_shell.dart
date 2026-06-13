import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/instance_dropdown.dart';

/// Tautulli module shell with bottom nav: Activity | History | Stats.
/// Shows instance dropdown in the header when 2+ Tautulli instances exist.
class TautulliModuleShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const TautulliModuleShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceState = ref.watch(instanceProvider);

    return Scaffold(
      appBar: instanceState.tautulliInstances.length > 1
          ? AppBar(
              title: InstanceDropdown(
                instances: instanceState.tautulliInstances,
                activeInstanceId: instanceState.activeTautulliInstanceId,
                onChanged: (id) => ref
                    .read(instanceProvider.notifier)
                    .setActiveTautulliInstance(id),
              ),
              backgroundColor: AppTheme.background,
              elevation: 0,
            )
          : null,
      body: child,
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          border: Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
        ),
        child: BottomNavigationBar(
          currentIndex: currentIndex,
          onTap: onTabChanged,
          items: const [
            BottomNavigationBarItem(
              icon: Icon(Icons.play_circle_outline),
              activeIcon: Icon(Icons.play_circle),
              label: 'Activity',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.history_outlined),
              activeIcon: Icon(Icons.history),
              label: 'History',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.insights_outlined),
              activeIcon: Icon(Icons.insights),
              label: 'Stats',
            ),
          ],
        ),
      ),
    );
  }
}
