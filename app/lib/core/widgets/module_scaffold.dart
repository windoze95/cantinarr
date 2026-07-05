import 'package:flutter/material.dart';
import '../layout/adaptive.dart';
import '../models/app_module.dart';
import '../theme/app_theme.dart';

/// Scaffold shared by the module shells: page content plus adaptive page
/// navigation. Mobile/tablet gets the classic bottom nav; desktop drops it —
/// the AppShell sidebar lists the same [pages] there.
class ModuleScaffold extends StatelessWidget {
  final PreferredSizeWidget? appBar;
  final List<ModulePage> pages;
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const ModuleScaffold({
    super.key,
    this.appBar,
    required this.pages,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: appBar,
      body: child,
      bottomNavigationBar: AppBreakpoints.isDesktop(context)
          ? null
          : Container(
              decoration: const BoxDecoration(
                border:
                    Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
              ),
              child: BottomNavigationBar(
                currentIndex: currentIndex.clamp(0, pages.length - 1),
                onTap: onTabChanged,
                items: [
                  for (final page in pages)
                    BottomNavigationBarItem(
                      icon: Icon(page.icon),
                      activeIcon: Icon(page.activeIcon),
                      label: page.label,
                    ),
                ],
              ),
            ),
    );
  }
}
