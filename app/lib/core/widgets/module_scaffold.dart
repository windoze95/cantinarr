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
      backgroundColor: Colors.transparent,
      appBar: appBar,
      body: child,
      bottomNavigationBar: AppBreakpoints.isDesktop(context)
          ? null
          : SafeArea(
              top: false,
              minimum: const EdgeInsets.fromLTRB(10, 0, 10, 8),
              child: Container(
                clipBehavior: Clip.antiAlias,
                decoration: BoxDecoration(
                  color: AppTheme.surface.withValues(alpha: 0.97),
                  borderRadius: BorderRadius.circular(AppTheme.radiusXLarge),
                  border: Border.all(color: AppTheme.border),
                  boxShadow: [
                    BoxShadow(
                      color: Colors.black.withValues(alpha: 0.42),
                      blurRadius: 24,
                      offset: const Offset(0, 10),
                    ),
                    BoxShadow(
                      color: AppTheme.signal.withValues(alpha: 0.035),
                      blurRadius: 20,
                    ),
                  ],
                ),
                child: BottomNavigationBar(
                  backgroundColor: Colors.transparent,
                  currentIndex: currentIndex.clamp(0, pages.length - 1),
                  onTap: onTabChanged,
                  items: [
                    for (final page in pages)
                      BottomNavigationBarItem(
                        icon: Icon(page.icon),
                        activeIcon: Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 13,
                            vertical: 5,
                          ),
                          decoration: BoxDecoration(
                            color: AppTheme.accent.withValues(alpha: 0.13),
                            borderRadius: BorderRadius.circular(
                              AppTheme.radiusPill,
                            ),
                          ),
                          child: Icon(page.activeIcon),
                        ),
                        label: page.label,
                      ),
                  ],
                ),
              ),
            ),
    );
  }
}
