import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/instance_dropdown.dart';

/// Chaptarr module shell with bottom nav: Library | Queue | History | Wanted.
/// Calendar is intentionally omitted (books have no air schedule). Shows the
/// instance dropdown in the header when 2+ instances exist.
class ChaptarrModuleShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const ChaptarrModuleShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceState = ref.watch(instanceProvider);

    return Scaffold(
      appBar: instanceState.chaptarrInstances.length > 1
          ? AppBar(
              title: InstanceDropdown(
                instances: instanceState.chaptarrInstances,
                activeInstanceId: instanceState.activeChaptarrInstanceId,
                onChanged: (id) => ref
                    .read(instanceProvider.notifier)
                    .setActiveChaptarrInstance(id),
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
              icon: Icon(Icons.menu_book_outlined),
              activeIcon: Icon(Icons.menu_book),
              label: 'Library',
            ),
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
            BottomNavigationBarItem(
              icon: Icon(Icons.warning_amber_outlined),
              activeIcon: Icon(Icons.warning_amber),
              label: 'Wanted',
            ),
          ],
        ),
      ),
    );
  }
}
