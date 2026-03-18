import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/instance_dropdown.dart';

/// Radarr module shell with bottom nav: Library | Queue | Calendar.
/// Shows instance dropdown in the header when 2+ instances exist.
class RadarrModuleShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const RadarrModuleShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceState = ref.watch(instanceProvider);

    return Scaffold(
      appBar: instanceState.radarrInstances.length > 1
          ? AppBar(
              title: InstanceDropdown(
                instances: instanceState.radarrInstances,
                activeInstanceId: instanceState.activeRadarrInstanceId,
                onChanged: (id) => ref
                    .read(instanceProvider.notifier)
                    .setActiveRadarrInstance(id),
              ),
              backgroundColor: AppTheme.background,
              elevation: 0,
            )
          : null,
      body: child,
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          border:
              Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
        ),
        child: BottomNavigationBar(
          currentIndex: currentIndex,
          onTap: onTabChanged,
          items: const [
            BottomNavigationBarItem(
              icon: Icon(Icons.video_library_outlined),
              activeIcon: Icon(Icons.video_library),
              label: 'Library',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.downloading_outlined),
              activeIcon: Icon(Icons.downloading),
              label: 'Queue',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.calendar_month_outlined),
              activeIcon: Icon(Icons.calendar_month),
              label: 'Calendar',
            ),
          ],
        ),
      ),
    );
  }
}
