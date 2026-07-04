import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../features/ai_assistant/ui/ai_chat_screen.dart';
import '../features/auth/logic/auth_provider.dart';
import '../features/auth/ui/auth_screen.dart';
import '../features/auth/ui/passkey_create_screen.dart';
import '../features/auth/ui/passkey_management_screen.dart';
import '../features/auth/ui/set_password_screen.dart';
import '../features/chaptarr/ui/chaptarr_history_screen.dart';
import '../features/chaptarr/ui/chaptarr_home_screen.dart';
import '../features/chaptarr/ui/chaptarr_module_shell.dart';
import '../features/chaptarr/ui/chaptarr_queue_screen.dart';
import '../features/chaptarr/ui/chaptarr_wanted_screen.dart';
import '../features/dashboard/ui/dashboard_books_tab.dart';
import '../features/dashboard/ui/dashboard_movies_tab.dart';
import '../features/dashboard/ui/dashboard_releases_tab.dart';
import '../features/dashboard/ui/dashboard_shell.dart';
import '../features/dashboard/ui/dashboard_tv_tab.dart';
import '../features/discover/data/tmdb_models.dart';
import '../features/downloads/ui/downloads_history_screen.dart';
import '../features/downloads/ui/downloads_module_shell.dart';
import '../features/downloads/ui/downloads_queue_screen.dart';
import '../features/issues/ui/agent_run_screen.dart';
import '../features/issues/ui/ai_remediation_settings_screen.dart';
import '../features/issues/ui/issue_thread_screen.dart';
import '../features/issues/ui/issues_list_screen.dart';
import '../features/issues/ui/pending_agent_actions_screen.dart';
import '../features/media_detail/ui/media_detail_screen.dart';
import '../features/notifications/ui/notification_preferences_screen.dart';
import '../features/radarr/ui/radarr_calendar_screen.dart';
import '../features/radarr/ui/radarr_history_screen.dart';
import '../features/radarr/ui/radarr_home_screen.dart';
import '../features/radarr/ui/radarr_module_shell.dart';
import '../features/radarr/ui/radarr_queue_screen.dart';
import '../features/radarr/ui/radarr_wanted_screen.dart';
import '../features/settings/ui/ai_tools_screen.dart';
import '../features/settings/ui/credentials_screen.dart';
import '../features/settings/ui/devices_screen.dart';
import '../features/settings/ui/plex_settings_screen.dart';
import '../features/settings/ui/instance_edit_screen.dart';
import '../features/settings/ui/pending_requests_screen.dart';
import '../features/settings/ui/request_settings_screen.dart';
import '../features/settings/ui/settings_screen.dart';
import '../features/settings/ui/user_request_settings_screen.dart';
import '../features/settings/ui/users_screen.dart';
import '../features/setup_wizard/ui/plex_watch_guide.dart';
import '../features/setup_wizard/ui/setup_wizard_screen.dart';
import '../features/shell/ui/app_shell.dart';
import '../features/sonarr/ui/sonarr_calendar_screen.dart';
import '../features/sonarr/ui/sonarr_history_screen.dart';
import '../features/sonarr/ui/sonarr_home_screen.dart';
import '../features/sonarr/ui/sonarr_module_shell.dart';
import '../features/sonarr/ui/sonarr_queue_screen.dart';
import '../features/sonarr/ui/sonarr_wanted_screen.dart';
import '../features/tautulli/ui/tautulli_activity_screen.dart';
import '../features/tautulli/ui/tautulli_history_screen.dart';
import '../features/tautulli/ui/tautulli_module_shell.dart';
import '../features/tautulli/ui/tautulli_stats_screen.dart';

final _rootNavigatorKey = GlobalKey<NavigatorState>();

/// Central router configuration using GoRouter with module-based navigation.
/// Outer ShellRoute provides the drawer + search bar.
/// Inner StatefulShellRoutes provide per-module bottom nav.
final appRouterProvider = Provider<GoRouter>((ref) {
  // Re-run redirects when auth state changes WITHOUT rebuilding the router.
  // Watching authProvider here would create a brand-new GoRouter on every auth
  // change (token refresh, profile reload, etc.), which resets navigation to
  // the initial route. A refreshListenable keeps the router instance stable.
  final authRefresh = ValueNotifier<int>(0);
  ref.onDispose(authRefresh.dispose);
  ref.listen(authProvider, (_, __) => authRefresh.value++);

  return GoRouter(
    navigatorKey: _rootNavigatorKey,
    initialLocation: '/dashboard/movies',
    refreshListenable: authRefresh,
    redirect: (context, state) {
      final auth = ref.read(authProvider).valueOrNull;
      final isAuthenticated = auth?.isAuthenticated ?? false;
      final isAuthRoute = state.matchedLocation == '/login';
      final pendingPasskey = auth?.pendingPasskeyOffer ?? false;

      if (!isAuthenticated && !isAuthRoute) return '/login';
      // During passkey offer, force user to /login (where the offer renders)
      if (isAuthenticated && pendingPasskey) {
        return isAuthRoute ? null : '/login';
      }
      if (isAuthenticated && isAuthRoute) return '/dashboard/movies';
      final isAdmin = auth?.user?.isAdmin ?? false;
      if (isAuthenticated &&
          !isAdmin &&
          _isInstanceModuleRoute(state.uri.path)) {
        return '/dashboard/movies';
      }
      return null;
    },
    routes: [
      // Auth route (outside the shell)
      GoRoute(
        path: '/login',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const AuthScreen(),
      ),

      // Module shell (provides drawer + search bar, no bottom nav)
      ShellRoute(
        builder: (context, state, child) {
          return AppShell(child: child);
        },
        routes: [
          // Dashboard module (Movies/TV tabs)
          StatefulShellRoute.indexedStack(
            builder: (context, state, navigationShell) {
              return DashboardShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              );
            },
            branches: [
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/dashboard/movies',
                    builder: (_, __) => const DashboardMoviesTab(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/dashboard/tv',
                    builder: (_, __) => const DashboardTvTab(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/dashboard/releases',
                    builder: (_, __) => const DashboardReleasesTab(),
                  ),
                ],
              ),
              // Books (Chaptarr) — last branch so the Books tab can be shown or
              // hidden (per the user's chaptarr grant) without shifting the
              // Movies/TV/Releases tab indices. The DashboardShell only surfaces
              // the Books tab when services.chaptarr is true.
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/dashboard/books',
                    builder: (_, __) => const DashboardBooksTab(),
                  ),
                ],
              ),
            ],
          ),

          // Radarr module (Library/Queue/History/Wanted/Calendar tabs)
          StatefulShellRoute.indexedStack(
            builder: (context, state, navigationShell) {
              return RadarrModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              );
            },
            branches: [
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/radarr/library',
                    builder: (_, __) => const RadarrHomeScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/radarr/queue',
                    builder: (_, __) => const RadarrQueueScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/radarr/history',
                    builder: (_, __) => const RadarrHistoryScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/radarr/wanted',
                    builder: (_, __) => const RadarrWantedScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/radarr/calendar',
                    builder: (_, __) => const RadarrCalendarScreen(),
                  ),
                ],
              ),
            ],
          ),

          // Sonarr module (Library/Queue/History/Wanted/Calendar tabs)
          StatefulShellRoute.indexedStack(
            builder: (context, state, navigationShell) {
              return SonarrModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              );
            },
            branches: [
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/sonarr/library',
                    builder: (_, __) => const SonarrHomeScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/sonarr/queue',
                    builder: (_, __) => const SonarrQueueScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/sonarr/history',
                    builder: (_, __) => const SonarrHistoryScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/sonarr/wanted',
                    builder: (_, __) => const SonarrWantedScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/sonarr/calendar',
                    builder: (_, __) => const SonarrCalendarScreen(),
                  ),
                ],
              ),
            ],
          ),

          // Chaptarr module (Library/Queue/History/Wanted tabs)
          StatefulShellRoute.indexedStack(
            builder: (context, state, navigationShell) {
              return ChaptarrModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              );
            },
            branches: [
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/chaptarr/library',
                    builder: (_, __) => const ChaptarrHomeScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/chaptarr/queue',
                    builder: (_, __) => const ChaptarrQueueScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/chaptarr/history',
                    builder: (_, __) => const ChaptarrHistoryScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/chaptarr/wanted',
                    builder: (_, __) => const ChaptarrWantedScreen(),
                  ),
                ],
              ),
            ],
          ),

          // Downloads module (Queue/History tabs, admin only)
          StatefulShellRoute.indexedStack(
            builder: (context, state, navigationShell) {
              return DownloadsModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              );
            },
            branches: [
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/downloads/queue',
                    builder: (_, __) => const DownloadsQueueScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/downloads/history',
                    builder: (_, __) => const DownloadsHistoryScreen(),
                  ),
                ],
              ),
            ],
          ),

          // Tautulli module (Activity/History/Stats tabs, admin only)
          StatefulShellRoute.indexedStack(
            builder: (context, state, navigationShell) {
              return TautulliModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              );
            },
            branches: [
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/tautulli/activity',
                    builder: (_, __) => const TautulliActivityScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/tautulli/history',
                    builder: (_, __) => const TautulliHistoryScreen(),
                  ),
                ],
              ),
              StatefulShellBranch(
                routes: [
                  GoRoute(
                    path: '/tautulli/stats',
                    builder: (_, __) => const TautulliStatsScreen(),
                  ),
                ],
              ),
            ],
          ),
        ],
      ),

      // Full-screen routes (outside the shell)
      GoRoute(
        path: '/assistant',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) {
          final auth = ref.read(authProvider).valueOrNull;
          final hasAi = auth?.connection?.services.ai ?? false;
          return AiChatScreen(aiAvailable: hasAi);
        },
      ),
      GoRoute(
        path: '/detail/:type/:id',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (context, state) {
          final type = state.pathParameters['type']!;
          final id = int.parse(state.pathParameters['id']!);
          final mediaType = type == 'tv' ? MediaType.tv : MediaType.movie;
          return MediaDetailScreen(
            id: id,
            mediaType: mediaType,
          );
        },
      ),
      GoRoute(
        path: '/settings',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const SettingsScreen(),
      ),
      GoRoute(
        path: '/settings/credentials',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const CredentialsScreen(),
      ),
      GoRoute(
        path: '/settings/ai-tools',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const AiToolsScreen(),
      ),
      GoRoute(
        path: '/settings/users',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const UsersScreen(),
      ),
      GoRoute(
        path: '/settings/users/:userId/request-settings',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (context, state) {
          final userId = int.parse(state.pathParameters['userId']!);
          final username = state.extra as String? ?? '';
          return UserRequestSettingsScreen(userId: userId, username: username);
        },
      ),
      GoRoute(
        path: '/approvals',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const PendingRequestsScreen(),
      ),
      GoRoute(
        path: '/issues',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const IssuesListScreen(),
      ),
      GoRoute(
        path: '/issues/:id',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (context, state) {
          final id = int.tryParse(state.pathParameters['id'] ?? '') ?? 0;
          return IssueThreadScreen(issueId: id);
        },
      ),
      GoRoute(
        path: '/agent-actions',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const PendingAgentActionsScreen(),
      ),
      GoRoute(
        path: '/agent-runs/:id',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (context, state) {
          final id = int.tryParse(state.pathParameters['id'] ?? '') ?? 0;
          return AgentRunScreen(runId: id);
        },
      ),
      GoRoute(
        path: '/settings/ai-remediation',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const AiRemediationSettingsScreen(),
      ),
      GoRoute(
        path: '/settings/request-settings',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const RequestSettingsScreen(),
      ),
      GoRoute(
        path: '/settings/devices',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const DevicesScreen(),
      ),
      GoRoute(
        path: '/settings/plex',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const PlexSettingsScreen(),
      ),
      GoRoute(
        path: '/settings/notifications',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const NotificationPreferencesScreen(),
      ),
      GoRoute(
        path: '/settings/passkeys',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const PasskeyManagementScreen(),
      ),
      GoRoute(
        path: '/settings/passkeys/new',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const PasskeyCreateScreen(),
      ),
      GoRoute(
        path: '/settings/password',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const SetPasswordScreen(),
      ),
      GoRoute(
        path: '/settings/instance/new',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const InstanceEditScreen(),
      ),
      GoRoute(
        path: '/settings/instance/:id',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (context, state) {
          final extra = state.extra as Map<String, dynamic>?;
          return InstanceEditScreen(
            instanceId: state.pathParameters['id'],
            initialServiceType: extra?['service_type'] as String?,
            initialName: extra?['name'] as String?,
            initialUrl: extra?['url'] as String?,
            initialApiKey: extra?['api_key'] as String?,
            initialUsername: extra?['username'] as String?,
            initialIsDefault: extra?['is_default'] as bool? ?? false,
          );
        },
      ),
      GoRoute(
        path: '/setup',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const SetupWizardScreen(),
      ),
      GoRoute(
        path: '/plex-guide',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const PlexWatchGuide(),
      ),
    ],
  );
});

bool _isInstanceModuleRoute(String path) {
  return path.startsWith('/radarr/') ||
      path.startsWith('/sonarr/') ||
      path.startsWith('/chaptarr/') ||
      path.startsWith('/downloads/') ||
      path.startsWith('/tautulli/');
}
