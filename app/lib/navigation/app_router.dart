import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../features/ai_assistant/ui/ai_chat_screen.dart';
import '../features/ai_assistant/ui/ai_access_screen.dart';
import '../features/ai_assistant/ui/codex_connection_screen.dart';
import '../features/ai_assistant/data/codex_oauth_service.dart';
import '../features/auth/logic/auth_provider.dart';
import '../features/auth/ui/auth_screen.dart';
import '../features/auth/ui/passkey_create_screen.dart';
import '../features/auth/ui/passkey_management_screen.dart';
import '../features/auth/ui/set_password_screen.dart';
import '../features/chaptarr/data/chaptarr_models.dart';
import '../features/chaptarr/ui/chaptarr_history_screen.dart';
import '../features/chaptarr/ui/chaptarr_home_screen.dart';
import '../features/chaptarr/ui/chaptarr_module_shell.dart';
import '../features/chaptarr/ui/chaptarr_queue_screen.dart';
import '../features/chaptarr/ui/chaptarr_wanted_screen.dart';
import '../features/config_changes/ui/config_change_detail_screen.dart';
import '../features/config_changes/ui/config_change_history_screen.dart';
import '../features/dashboard/ui/dashboard_books_tab.dart';
import '../features/dashboard/ui/dashboard_movies_tab.dart';
import '../features/dashboard/ui/dashboard_releases_tab.dart';
import '../features/dashboard/ui/dashboard_shell.dart';
import '../features/dashboard/ui/dashboard_tv_tab.dart';
import '../features/dashboard/ui/requester_book_detail_screen.dart';
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
import '../core/widgets/app_ambient_background.dart';

final _rootNavigatorKey = GlobalKey<NavigatorState>();

/// Lateral page for top-level surfaces: login, the app shell, and the module
/// shells. The incoming page dissolves in over whatever it replaces.
///
/// Scaffolds are transparent by theme, so every routed page must paint its own
/// opaque backdrop (AppAmbientBackground) — otherwise the previous route shows
/// through during transitions and both screens render at once as a jarring
/// double exposure. Lateral surfaces fade because they're peer navigation with
/// no back stack; pushed routes keep MaterialPage's platform slide (and iOS
/// swipe-back), made correct by the same opaque backdrop on their children.
CustomTransitionPage<void> _fadeSurfacePage({
  required LocalKey key,
  required Widget child,
}) {
  return CustomTransitionPage<void>(
    key: key,
    transitionDuration: const Duration(milliseconds: 280),
    reverseTransitionDuration: const Duration(milliseconds: 220),
    transitionsBuilder: (context, animation, secondaryAnimation, child) {
      return FadeTransition(
        opacity: CurvedAnimation(parent: animation, curve: Curves.easeOutCubic),
        child: child,
      );
    },
    child: AppAmbientBackground(child: child),
  );
}

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

  // Keep an in-memory return target while authentication (or the first-login
  // passkey offer) temporarily sends the user to /login. This deliberately
  // accepts only app-internal locations, so it cannot become an open redirect.
  String? pendingReturnTo;

  return GoRouter(
    navigatorKey: _rootNavigatorKey,
    initialLocation: '/dashboard/movies',
    refreshListenable: authRefresh,
    redirect: (context, state) {
      final auth = ref.read(authProvider).valueOrNull;
      final isAuthenticated = auth?.isAuthenticated ?? false;
      final isAuthRoute = state.matchedLocation == '/login';
      final pendingPasskey = auth?.pendingPasskeyOffer ?? false;

      if (!isAuthenticated && !isAuthRoute) {
        if (_isInternalReturnLocation(state.uri)) {
          pendingReturnTo = state.uri.toString();
        }
        return '/login';
      }
      // During passkey offer, force user to /login (where the offer renders)
      if (isAuthenticated && pendingPasskey) {
        if (!isAuthRoute && _isInternalReturnLocation(state.uri)) {
          pendingReturnTo = state.uri.toString();
        }
        return isAuthRoute ? null : '/login';
      }
      if (isAuthenticated && isAuthRoute) {
        final destination = pendingReturnTo;
        pendingReturnTo = null;
        return destination ?? '/dashboard/movies';
      }
      final isAdmin = auth?.user?.isAdmin ?? false;
      if (isAuthenticated && !isAdmin && _isAdminOnlyRoute(state.uri.path)) {
        return '/dashboard/movies';
      }
      // Requester book surfaces — the Books tab and the id-addressable book
      // detail — require the books grant and degrade the same way without it.
      final hasChaptarrGrant = auth?.connection?.services.chaptarr ?? false;
      if (isAuthenticated &&
          !hasChaptarrGrant &&
          (_isWithinRoute(state.uri.path, '/dashboard/books') ||
              _isWithinRoute(state.uri.path, '/detail/book'))) {
        return '/dashboard/movies';
      }
      return null;
    },
    routes: [
      // Auth route (outside the shell)
      GoRoute(
        path: '/login',
        parentNavigatorKey: _rootNavigatorKey,
        pageBuilder: (context, state) => _fadeSurfacePage(
          key: state.pageKey,
          child: const AuthScreen(),
        ),
      ),

      // Module shell (provides drawer/sidebar + search bar, no bottom nav)
      ShellRoute(
        pageBuilder: (context, state, child) => _fadeSurfacePage(
          key: state.pageKey,
          child: AppShell(currentPath: state.uri.path, child: child),
        ),
        routes: [
          // Dashboard module (Movies/TV tabs)
          StatefulShellRoute.indexedStack(
            pageBuilder: (context, state, navigationShell) => _fadeSurfacePage(
              key: state.pageKey,
              child: DashboardShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              ),
            ),
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
            pageBuilder: (context, state, navigationShell) => _fadeSurfacePage(
              key: state.pageKey,
              child: RadarrModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              ),
            ),
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
            pageBuilder: (context, state, navigationShell) => _fadeSurfacePage(
              key: state.pageKey,
              child: SonarrModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              ),
            ),
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
            pageBuilder: (context, state, navigationShell) => _fadeSurfacePage(
              key: state.pageKey,
              child: ChaptarrModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              ),
            ),
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
            pageBuilder: (context, state, navigationShell) => _fadeSurfacePage(
              key: state.pageKey,
              child: DownloadsModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              ),
            ),
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
            pageBuilder: (context, state, navigationShell) => _fadeSurfacePage(
              key: state.pageKey,
              child: TautulliModuleShell(
                currentIndex: navigationShell.currentIndex,
                onTabChanged: (index) => navigationShell.goBranch(index),
                child: navigationShell,
              ),
            ),
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
          // Authenticated secondary routes stay inside the same shell. On
          // desktop this preserves the command sidebar through details,
          // settings, approvals, and issue work; compact layouts still use
          // each screen's own back affordance.
          GoRoute(
            path: '/assistant',
            builder: (_, __) =>
                const AppAmbientBackground(child: AiChatScreen()),
          ),
          GoRoute(
            path: '/detail/:type/:id',
            redirect: (_, state) => _mediaDetailRedirect(state),
            builder: (context, state) =>
                AppAmbientBackground(child: _mediaDetailChild(state)),
          ),
          GoRoute(
            path: '/settings',
            builder: (_, __) =>
                const AppAmbientBackground(child: SettingsScreen()),
          ),
          GoRoute(
            path: '/settings/ai',
            builder: (_, __) =>
                const AppAmbientBackground(child: AiAccessScreen()),
          ),
          GoRoute(
            path: '/settings/chatgpt',
            builder: (_, __) =>
                const AppAmbientBackground(child: CodexConnectionScreen()),
          ),
          GoRoute(
            path: '/settings/credentials/chatgpt',
            builder: (_, __) => const AppAmbientBackground(
              child: CodexConnectionScreen(
                scope: CodexOAuthScope.adminShared,
              ),
            ),
          ),
          GoRoute(
            path: '/settings/credentials',
            builder: (_, __) =>
                const AppAmbientBackground(child: CredentialsScreen()),
          ),
          GoRoute(
            path: '/settings/ai-tools',
            builder: (_, __) =>
                const AppAmbientBackground(child: AiToolsScreen()),
          ),
          GoRoute(
            path: '/settings/change-history',
            builder: (_, __) => const AppAmbientBackground(
              child: ConfigChangeHistoryScreen(),
            ),
          ),
          GoRoute(
            path: '/settings/change-history/:id',
            redirect: (_, state) =>
                _positiveIntParameter(state, 'id') == null
                    ? '/settings/change-history'
                    : null,
            builder: (context, state) {
              final id = _positiveIntParameter(state, 'id');
              if (id == null) {
                return const AppAmbientBackground(
                  child: _InvalidRouteScreen(
                    message: 'This change-history link is invalid.',
                  ),
                );
              }
              return AppAmbientBackground(
                child: ConfigChangeDetailScreen(changeId: id),
              );
            },
          ),
          GoRoute(
            path: '/settings/users',
            builder: (_, __) =>
                const AppAmbientBackground(child: UsersScreen()),
          ),
          GoRoute(
            path: '/settings/users/:userId/request-settings',
            redirect: (_, state) =>
                _positiveIntParameter(state, 'userId') == null
                    ? '/settings/users'
                    : null,
            builder: (context, state) {
              final userId = _positiveIntParameter(state, 'userId');
              if (userId == null) {
                return const AppAmbientBackground(
                  child: _InvalidRouteScreen(
                    message: 'This user settings link is invalid.',
                  ),
                );
              }
              final username = state.extra as String? ?? '';
              return AppAmbientBackground(
                child: UserRequestSettingsScreen(
                    userId: userId, username: username),
              );
            },
          ),
          GoRoute(
            path: '/approvals',
            builder: (_, __) =>
                const AppAmbientBackground(child: PendingRequestsScreen()),
          ),
          GoRoute(
            path: '/issues',
            builder: (_, __) =>
                const AppAmbientBackground(child: IssuesListScreen()),
          ),
          GoRoute(
            path: '/issues/:id',
            builder: (context, state) {
              final id = int.tryParse(state.pathParameters['id'] ?? '') ?? 0;
              return AppAmbientBackground(
                  child: IssueThreadScreen(issueId: id));
            },
          ),
          GoRoute(
            path: '/agent-actions',
            builder: (_, __) =>
                const AppAmbientBackground(child: PendingAgentActionsScreen()),
          ),
          GoRoute(
            path: '/agent-runs/:id',
            builder: (context, state) {
              final id = int.tryParse(state.pathParameters['id'] ?? '') ?? 0;
              return AppAmbientBackground(child: AgentRunScreen(runId: id));
            },
          ),
          GoRoute(
            path: '/settings/ai-remediation',
            builder: (_, __) => const AppAmbientBackground(
                child: AiRemediationSettingsScreen()),
          ),
          GoRoute(
            path: '/settings/request-settings',
            builder: (_, __) =>
                const AppAmbientBackground(child: RequestSettingsScreen()),
          ),
          GoRoute(
            path: '/settings/devices',
            builder: (_, __) =>
                const AppAmbientBackground(child: DevicesScreen()),
          ),
          GoRoute(
            path: '/settings/plex',
            builder: (_, __) =>
                const AppAmbientBackground(child: PlexSettingsScreen()),
          ),
          GoRoute(
            path: '/settings/notifications',
            builder: (_, __) => const AppAmbientBackground(
                child: NotificationPreferencesScreen()),
          ),
          GoRoute(
            path: '/settings/passkeys',
            builder: (_, __) =>
                const AppAmbientBackground(child: PasskeyManagementScreen()),
          ),
          GoRoute(
            path: '/settings/passkeys/new',
            builder: (_, __) =>
                const AppAmbientBackground(child: PasskeyCreateScreen()),
          ),
          GoRoute(
            path: '/settings/password',
            builder: (_, __) =>
                const AppAmbientBackground(child: SetPasswordScreen()),
          ),
          GoRoute(
            path: '/settings/instance/new',
            builder: (_, __) =>
                const AppAmbientBackground(child: InstanceEditScreen()),
          ),
          GoRoute(
            path: '/settings/instance/:id',
            builder: (context, state) {
              final extra = state.extra as Map<String, dynamic>?;
              return AppAmbientBackground(
                child: InstanceEditScreen(
                  instanceId: state.pathParameters['id'],
                  initialServiceType: extra?['service_type'] as String?,
                  initialName: extra?['name'] as String?,
                  initialUrl: extra?['url'] as String?,
                  initialApiKey: extra?['api_key'] as String?,
                  initialUsername: extra?['username'] as String?,
                  initialIsDefault: extra?['is_default'] as bool? ?? false,
                ),
              );
            },
          ),
          GoRoute(
            path: '/setup',
            builder: (_, __) =>
                const AppAmbientBackground(child: SetupWizardScreen()),
          ),
          GoRoute(
            path: '/plex-guide',
            builder: (_, __) =>
                const AppAmbientBackground(child: PlexWatchGuide()),
          ),
        ],
      ),
    ],
  );
});

bool _isInternalReturnLocation(Uri uri) {
  return !uri.hasScheme &&
      !uri.hasAuthority &&
      uri.path.startsWith('/') &&
      !uri.path.startsWith('//') &&
      uri.path != '/login';
}

bool _isWithinRoute(String path, String route) =>
    path == route || path.startsWith('$route/');

/// Every route whose UI is explicitly admin-only. Issue threads are omitted:
/// reporters must be able to follow a notification back to their own thread,
/// while the aggregate `/issues` queue remains an admin surface.
bool _isAdminOnlyRoute(String path) {
  const adminRoots = [
    '/radarr',
    '/sonarr',
    '/chaptarr',
    '/downloads',
    '/tautulli',
    '/approvals',
    '/agent-actions',
    '/agent-runs',
    '/setup',
    '/settings/credentials',
    '/settings/ai-tools',
    '/settings/change-history',
    '/settings/users',
    '/settings/ai-remediation',
    '/settings/request-settings',
    '/settings/devices',
    '/settings/plex',
    '/settings/instance',
  ];

  return path == '/issues' ||
      adminRoots.any((route) => _isWithinRoute(path, route));
}

int? _positiveIntParameter(GoRouterState state, String name) {
  final value = int.tryParse(state.pathParameters[name] ?? '');
  return value != null && value > 0 ? value : null;
}

/// The `/detail/:type/:id` route body. Books are addressed by their (string)
/// Chaptarr foreignBookId — the identity request rows store and decision push
/// payloads carry — not a TMDB id.
Widget _mediaDetailChild(GoRouterState state) {
  final type = state.pathParameters['type']!;
  if (type == 'book') {
    final foreignId = state.pathParameters['id']?.trim() ?? '';
    if (foreignId.isEmpty) {
      return const _InvalidRouteScreen(
        message: 'This book link is invalid.',
      );
    }
    return RequesterBookDetailScreen(
      foreignId: foreignId,
      titleHint: state.uri.queryParameters['title'],
      instanceId: state.uri.queryParameters['instance_id'],
      initialBook: state.extra is ChaptarrBook
          ? state.extra! as ChaptarrBook
          : null,
    );
  }
  final id = _positiveIntParameter(state, 'id');
  if (id == null) {
    return const _InvalidRouteScreen(
      message: 'This media link is invalid.',
    );
  }
  final mediaType = type == 'tv' ? MediaType.tv : MediaType.movie;
  return MediaDetailScreen(
    id: id,
    mediaType: mediaType,
  );
}

bool _hasValidMediaDetailParameters(GoRouterState state) {
  final type = state.pathParameters['type'];
  return (type == 'movie' || type == 'tv') &&
      _positiveIntParameter(state, 'id') != null;
}

/// Route-level guard for `/detail/:type/:id`. Books use a string foreign id,
/// so the only malformed shape is a blank id — degrade to the Books tab (the
/// requester book surface). Movie/TV keep the positive-TMDB-id validation and
/// their movies-dashboard fallback.
String? _mediaDetailRedirect(GoRouterState state) {
  if (state.pathParameters['type'] == 'book') {
    final id = state.pathParameters['id']?.trim() ?? '';
    return id.isEmpty ? '/dashboard/books' : null;
  }
  return _hasValidMediaDetailParameters(state) ? null : '/dashboard/movies';
}

/// Defensive fallback for a malformed parameter if a future router version
/// invokes a builder before its route-level redirect has settled.
class _InvalidRouteScreen extends StatelessWidget {
  final String message;

  const _InvalidRouteScreen({required this.message});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(message, textAlign: TextAlign.center),
        ),
      ),
    );
  }
}
