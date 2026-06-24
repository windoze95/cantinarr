import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';

/// Dashboard module shell with bottom nav: Movies | TV Shows | Releases, plus a
/// Books tab when the user has Chaptarr access (services.chaptarr). Books is the
/// LAST branch so showing/hiding it never shifts the other tabs' indices.
class DashboardShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const DashboardShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final showBooks = ref.watch(
      authProvider.select(
        (a) => a.valueOrNull?.connection?.services.chaptarr ?? false,
      ),
    );

    final items = <BottomNavigationBarItem>[
      const BottomNavigationBarItem(
        icon: Icon(Icons.movie_outlined),
        activeIcon: Icon(Icons.movie),
        label: 'Movies',
      ),
      const BottomNavigationBarItem(
        icon: Icon(Icons.tv_outlined),
        activeIcon: Icon(Icons.tv),
        label: 'TV Shows',
      ),
      const BottomNavigationBarItem(
        icon: Icon(Icons.event_outlined),
        activeIcon: Icon(Icons.event),
        label: 'Releases',
      ),
      if (showBooks)
        const BottomNavigationBarItem(
          icon: Icon(Icons.menu_book_outlined),
          activeIcon: Icon(Icons.menu_book),
          label: 'Books',
        ),
    ];

    return Scaffold(
      body: child,
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          border: Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
        ),
        child: BottomNavigationBar(
          currentIndex: currentIndex.clamp(0, items.length - 1),
          onTap: onTabChanged,
          items: items,
        ),
      ),
    );
  }
}
