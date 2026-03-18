import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';

/// Dashboard module shell with bottom nav: Movies | TV Shows.
class DashboardShell extends StatefulWidget {
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
  State<DashboardShell> createState() => _DashboardShellState();
}

class _DashboardShellState extends State<DashboardShell> {
  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: widget.child,
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          border:
              Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
        ),
        child: BottomNavigationBar(
          currentIndex: widget.currentIndex,
          onTap: widget.onTabChanged,
          items: const [
            BottomNavigationBarItem(
              icon: Icon(Icons.movie_outlined),
              activeIcon: Icon(Icons.movie),
              label: 'Movies',
            ),
            BottomNavigationBarItem(
              icon: Icon(Icons.tv_outlined),
              activeIcon: Icon(Icons.tv),
              label: 'TV Shows',
            ),
          ],
        ),
      ),
    );
  }
}
