import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';

/// The root shell widget with bottom navigation and a drawer.
class AppShell extends StatelessWidget {
  final int currentIndex;
  final Widget child;
  final ValueChanged<int> onTabChanged;

  const AppShell({
    super.key,
    required this.currentIndex,
    required this.child,
    required this.onTabChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: child,
      bottomNavigationBar: _buildBottomNav(),
      drawer: _buildDrawer(context),
    );
  }

  Widget _buildBottomNav() {
    return Container(
      decoration: const BoxDecoration(
        border: Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
      ),
      child: BottomNavigationBar(
        currentIndex: currentIndex,
        onTap: onTabChanged,
        items: const [
          BottomNavigationBarItem(
            icon: Icon(Icons.explore_outlined),
            activeIcon: Icon(Icons.explore),
            label: 'Discover',
          ),
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
          BottomNavigationBarItem(
            icon: Icon(Icons.smart_toy_outlined),
            activeIcon: Icon(Icons.smart_toy),
            label: 'Assistant',
          ),
        ],
      ),
    );
  }

  Widget _buildDrawer(BuildContext context) {
    return Drawer(
      backgroundColor: AppTheme.surface,
      child: SafeArea(
        child: Column(
          children: [
            // Header
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    width: 48,
                    height: 48,
                    decoration: BoxDecoration(
                      color: AppTheme.accent.withValues(alpha: 0.15),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: const Icon(Icons.movie_filter,
                        color: AppTheme.accent, size: 28),
                  ),
                  const SizedBox(height: 12),
                  const Text(
                    'Cantinarr',
                    style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 24,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                  const Text(
                    'Your media companion',
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 14),
                  ),
                ],
              ),
            ),
            const Divider(color: AppTheme.border),

            // Navigation items
            _DrawerItem(
              icon: Icons.explore,
              title: 'Discover',
              selected: currentIndex == 0,
              onTap: () {
                Navigator.pop(context);
                onTabChanged(0);
              },
            ),
            _DrawerItem(
              icon: Icons.movie,
              title: 'Movies (Radarr)',
              selected: currentIndex == 1,
              onTap: () {
                Navigator.pop(context);
                onTabChanged(1);
              },
            ),
            _DrawerItem(
              icon: Icons.tv,
              title: 'TV Shows (Sonarr)',
              selected: currentIndex == 2,
              onTap: () {
                Navigator.pop(context);
                onTabChanged(2);
              },
            ),
            _DrawerItem(
              icon: Icons.smart_toy,
              title: 'AI Assistant',
              selected: currentIndex == 3,
              onTap: () {
                Navigator.pop(context);
                onTabChanged(3);
              },
            ),

            const Spacer(),
            const Divider(color: AppTheme.border),

            _DrawerItem(
              icon: Icons.play_circle_outline,
              title: 'Plex Setup Guide',
              onTap: () => Navigator.pop(context),
            ),
            _DrawerItem(
              icon: Icons.settings,
              title: 'Settings',
              onTap: () => Navigator.pop(context),
            ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }
}

class _DrawerItem extends StatelessWidget {
  final IconData icon;
  final String title;
  final bool selected;
  final VoidCallback onTap;

  const _DrawerItem({
    required this.icon,
    required this.title,
    this.selected = false,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: Icon(icon,
          color: selected ? AppTheme.accent : AppTheme.textSecondary),
      title: Text(
        title,
        style: TextStyle(
          color: selected ? AppTheme.accent : AppTheme.textPrimary,
          fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
        ),
      ),
      selected: selected,
      selectedTileColor: AppTheme.accent.withValues(alpha: 0.08),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
      onTap: onTap,
    );
  }
}
