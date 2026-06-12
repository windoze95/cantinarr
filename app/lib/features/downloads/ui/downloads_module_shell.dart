import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/instance_dropdown.dart';

/// Downloads module shell with bottom nav: Queue | History.
/// Shows instance dropdown in the header when 2+ download clients exist.
class DownloadsModuleShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const DownloadsModuleShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceState = ref.watch(instanceProvider);

    return Scaffold(
      appBar: instanceState.downloadInstances.length > 1
          ? AppBar(
              title: InstanceDropdown(
                instances: instanceState.downloadInstances,
                activeInstanceId: instanceState.activeDownloadInstanceId,
                onChanged: (id) => ref
                    .read(instanceProvider.notifier)
                    .setActiveDownloadInstance(id),
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
              icon: Icon(Icons.downloading_outlined),
              activeIcon: Icon(Icons.downloading),
              label: 'Queue',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.history_outlined),
              activeIcon: Icon(Icons.history),
              label: 'History',
            ),
          ],
        ),
      ),
    );
  }
}
