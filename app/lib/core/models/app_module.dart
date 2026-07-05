import 'package:flutter/material.dart';

/// Types of modules available in the app.
enum ModuleType {
  dashboard,
  radarr,
  sonarr,
  chaptarr,
  downloads,
  tautulli,
  assistant
}

/// Represents a navigable module in the app (shown in drawer).
class AppModule {
  final ModuleType type;
  final String label;
  final IconData icon;
  final String? instanceId;
  final String? instanceName;

  const AppModule({
    required this.type,
    required this.label,
    required this.icon,
    this.instanceId,
    this.instanceName,
  });
}

/// One page (tab) within a module. Renders as a bottom-nav item on
/// mobile/tablet and as a sidebar sub-item on desktop.
class ModulePage {
  final String label;
  final IconData icon;
  final IconData activeIcon;
  final String route;

  const ModulePage({
    required this.label,
    required this.icon,
    required this.activeIcon,
    required this.route,
  });
}

/// Pages for [type], in the same order as the module's StatefulShellRoute
/// branches in app_router.dart (bottom-nav index == branch index).
/// [includeBooks] adds the dashboard Books tab, which exists only for users
/// with Chaptarr access (services.chaptarr); it is last so its presence never
/// shifts the other tabs' indices.
List<ModulePage> modulePagesFor(ModuleType type, {bool includeBooks = false}) {
  switch (type) {
    case ModuleType.dashboard:
      return [
        const ModulePage(
          label: 'Movies',
          icon: Icons.movie_outlined,
          activeIcon: Icons.movie,
          route: '/dashboard/movies',
        ),
        const ModulePage(
          label: 'TV Shows',
          icon: Icons.tv_outlined,
          activeIcon: Icons.tv,
          route: '/dashboard/tv',
        ),
        const ModulePage(
          label: 'Releases',
          icon: Icons.event_outlined,
          activeIcon: Icons.event,
          route: '/dashboard/releases',
        ),
        if (includeBooks)
          const ModulePage(
            label: 'Books',
            icon: Icons.menu_book_outlined,
            activeIcon: Icons.menu_book,
            route: '/dashboard/books',
          ),
      ];
    case ModuleType.radarr:
      return const [
        ModulePage(
          label: 'Library',
          icon: Icons.video_library_outlined,
          activeIcon: Icons.video_library,
          route: '/radarr/library',
        ),
        ModulePage(
          label: 'Queue',
          icon: Icons.downloading_outlined,
          activeIcon: Icons.downloading,
          route: '/radarr/queue',
        ),
        ModulePage(
          label: 'History',
          icon: Icons.history_outlined,
          activeIcon: Icons.history,
          route: '/radarr/history',
        ),
        ModulePage(
          label: 'Wanted',
          icon: Icons.report_gmailerrorred_outlined,
          activeIcon: Icons.report_gmailerrorred,
          route: '/radarr/wanted',
        ),
        ModulePage(
          label: 'Calendar',
          icon: Icons.calendar_month_outlined,
          activeIcon: Icons.calendar_month,
          route: '/radarr/calendar',
        ),
      ];
    case ModuleType.sonarr:
      return const [
        ModulePage(
          label: 'Library',
          icon: Icons.video_library_outlined,
          activeIcon: Icons.video_library,
          route: '/sonarr/library',
        ),
        ModulePage(
          label: 'Queue',
          icon: Icons.downloading_outlined,
          activeIcon: Icons.downloading,
          route: '/sonarr/queue',
        ),
        ModulePage(
          label: 'History',
          icon: Icons.history_outlined,
          activeIcon: Icons.history,
          route: '/sonarr/history',
        ),
        ModulePage(
          label: 'Wanted',
          icon: Icons.report_gmailerrorred_outlined,
          activeIcon: Icons.report_gmailerrorred,
          route: '/sonarr/wanted',
        ),
        ModulePage(
          label: 'Calendar',
          icon: Icons.calendar_month_outlined,
          activeIcon: Icons.calendar_month,
          route: '/sonarr/calendar',
        ),
      ];
    case ModuleType.chaptarr:
      return const [
        ModulePage(
          label: 'Library',
          icon: Icons.menu_book_outlined,
          activeIcon: Icons.menu_book,
          route: '/chaptarr/library',
        ),
        ModulePage(
          label: 'Queue',
          icon: Icons.downloading_outlined,
          activeIcon: Icons.downloading,
          route: '/chaptarr/queue',
        ),
        ModulePage(
          label: 'History',
          icon: Icons.history_outlined,
          activeIcon: Icons.history,
          route: '/chaptarr/history',
        ),
        ModulePage(
          label: 'Wanted',
          icon: Icons.warning_amber_outlined,
          activeIcon: Icons.warning_amber,
          route: '/chaptarr/wanted',
        ),
      ];
    case ModuleType.downloads:
      return const [
        ModulePage(
          label: 'Queue',
          icon: Icons.downloading_outlined,
          activeIcon: Icons.downloading,
          route: '/downloads/queue',
        ),
        ModulePage(
          label: 'History',
          icon: Icons.history_outlined,
          activeIcon: Icons.history,
          route: '/downloads/history',
        ),
      ];
    case ModuleType.tautulli:
      return const [
        ModulePage(
          label: 'Activity',
          icon: Icons.play_circle_outline,
          activeIcon: Icons.play_circle,
          route: '/tautulli/activity',
        ),
        ModulePage(
          label: 'History',
          icon: Icons.history_outlined,
          activeIcon: Icons.history,
          route: '/tautulli/history',
        ),
        ModulePage(
          label: 'Stats',
          icon: Icons.insights_outlined,
          activeIcon: Icons.insights,
          route: '/tautulli/stats',
        ),
      ];
    case ModuleType.assistant:
      return const [];
  }
}
