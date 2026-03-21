import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../features/ai_assistant/ui/ai_chat_screen.dart';
import '../features/auth/logic/auth_provider.dart';
import '../features/auth/ui/auth_screen.dart';
import '../features/auth/ui/passkey_management_screen.dart';
import '../features/dashboard/ui/dashboard_movies_tab.dart';
import '../features/dashboard/ui/dashboard_shell.dart';
import '../features/dashboard/ui/dashboard_tv_tab.dart';
import '../features/discover/data/tmdb_models.dart';
import '../features/media_detail/ui/media_detail_screen.dart';
import '../features/radarr/ui/radarr_calendar_screen.dart';
import '../features/radarr/ui/radarr_home_screen.dart';
import '../features/radarr/ui/radarr_module_shell.dart';
import '../features/radarr/ui/radarr_queue_screen.dart';
import '../features/settings/ui/credentials_screen.dart';
import '../features/settings/ui/devices_screen.dart';
import '../features/settings/ui/instance_edit_screen.dart';
import '../features/settings/ui/settings_screen.dart';
import '../features/setup_wizard/ui/plex_setup_guide.dart';
import '../features/setup_wizard/ui/setup_wizard_screen.dart';
import '../features/shell/ui/app_shell.dart';
import '../features/sonarr/ui/sonarr_calendar_screen.dart';
import '../features/sonarr/ui/sonarr_home_screen.dart';
import '../features/sonarr/ui/sonarr_module_shell.dart';
import '../features/sonarr/ui/sonarr_queue_screen.dart';

final _rootNavigatorKey = GlobalKey<NavigatorState>();

/// Central router configuration using GoRouter with module-based navigation.
/// Outer ShellRoute provides the drawer + search bar.
/// Inner StatefulShellRoutes provide per-module bottom nav.
final appRouterProvider = Provider<GoRouter>((ref) {
  final authState = ref.watch(authProvider);

  return GoRouter(
    navigatorKey: _rootNavigatorKey,
    initialLocation: '/dashboard/movies',
    redirect: (context, state) {
      final auth = authState.valueOrNull;
      final isAuthenticated = auth?.isAuthenticated ?? false;
      final isAuthRoute = state.matchedLocation == '/login';
      final pendingPasskey = auth?.pendingPasskeyOffer ?? false;

      if (!isAuthenticated && !isAuthRoute) return '/login';
      // During passkey offer, force user to /login (where the offer renders)
      if (isAuthenticated && pendingPasskey) return isAuthRoute ? null : '/login';
      if (isAuthenticated && isAuthRoute) return '/dashboard/movies';
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
            ],
          ),

          // Radarr module (Library/Queue/Calendar tabs)
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
                    path: '/radarr/calendar',
                    builder: (_, __) => const RadarrCalendarScreen(),
                  ),
                ],
              ),
            ],
          ),

          // Sonarr module (Library/Queue/Calendar tabs)
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
                    path: '/sonarr/calendar',
                    builder: (_, __) => const SonarrCalendarScreen(),
                  ),
                ],
              ),
            ],
          ),

          // AI Assistant module (single route, no bottom nav)
          GoRoute(
            path: '/assistant',
            builder: (_, __) {
              final auth = ref.read(authProvider).valueOrNull;
              final hasAi = auth?.connection?.services.ai ?? false;
              return AiChatScreen(aiAvailable: hasAi);
            },
          ),
        ],
      ),

      // Full-screen routes (outside the shell)
      GoRoute(
        path: '/detail/:type/:id',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (context, state) {
          final type = state.pathParameters['type']!;
          final id = int.parse(state.pathParameters['id']!);
          final mediaType =
              type == 'tv' ? MediaType.tv : MediaType.movie;
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
        path: '/settings/devices',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const DevicesScreen(),
      ),
      GoRoute(
        path: '/settings/passkeys',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const PasskeyManagementScreen(),
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
        builder: (_, __) => const PlexSetupGuide(),
      ),
    ],
  );
});
