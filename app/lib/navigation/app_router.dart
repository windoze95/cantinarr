import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../features/ai_assistant/ui/ai_chat_screen.dart';
import '../features/auth/logic/auth_provider.dart';
import '../features/auth/ui/invite_screen.dart';
import '../features/auth/ui/login_screen.dart';
import '../features/discover/data/tmdb_models.dart';
import '../features/discover/ui/discover_screen.dart';
import '../features/media_detail/ui/media_detail_screen.dart';
import '../features/radarr/ui/radarr_home_screen.dart';
import '../features/settings/ui/settings_screen.dart';
import '../features/setup_wizard/ui/plex_setup_guide.dart';
import '../features/setup_wizard/ui/setup_wizard_screen.dart';
import '../features/sonarr/ui/sonarr_home_screen.dart';
import '../features/shell/ui/app_shell.dart';

final _rootNavigatorKey = GlobalKey<NavigatorState>();
final _shellNavigatorKey = GlobalKey<NavigatorState>();

/// Central router configuration using GoRouter with ShellRoute for tabs.
/// Redirects unauthenticated users to /login.
final appRouterProvider = Provider<GoRouter>((ref) {
  final authState = ref.watch(authProvider);

  return GoRouter(
    navigatorKey: _rootNavigatorKey,
    initialLocation: '/discover',
    redirect: (context, state) {
      final auth = authState.valueOrNull;
      final isAuthenticated = auth?.isAuthenticated ?? false;
      final isAuthRoute = state.matchedLocation == '/login' ||
          state.matchedLocation == '/invite';

      if (!isAuthenticated && !isAuthRoute) return '/login';
      if (isAuthenticated && isAuthRoute) return '/discover';
      return null;
    },
    routes: [
      // Auth routes (outside the shell)
      GoRoute(
        path: '/login',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, __) => const LoginScreen(),
      ),
      GoRoute(
        path: '/invite',
        parentNavigatorKey: _rootNavigatorKey,
        builder: (_, state) => InviteScreen(
          prefillServerUrl: state.extra as String?,
        ),
      ),

      // Shell route with bottom navigation
      StatefulShellRoute.indexedStack(
        builder: (context, state, navigationShell) {
          return AppShell(
            currentIndex: navigationShell.currentIndex,
            onTabChanged: (index) => navigationShell.goBranch(index),
            child: navigationShell,
          );
        },
        branches: [
          // Tab 0: Discover
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/discover',
                builder: (context, state) => const DiscoverScreen(),
              ),
            ],
          ),
          // Tab 1: Radarr (Movies)
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/radarr',
                builder: (context, state) {
                  final auth = ref.read(authProvider).valueOrNull;
                  final hasRadarr =
                      auth?.connection?.services.radarr ?? false;
                  if (!hasRadarr) {
                    return _PlaceholderScreen(
                      title: 'Movies',
                      message:
                          'Radarr is not configured on this server.',
                      icon: Icons.movie_outlined,
                    );
                  }
                  return const RadarrHomeScreen();
                },
              ),
            ],
          ),
          // Tab 2: Sonarr (TV)
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/sonarr',
                builder: (context, state) {
                  final auth = ref.read(authProvider).valueOrNull;
                  final hasSonarr =
                      auth?.connection?.services.sonarr ?? false;
                  if (!hasSonarr) {
                    return _PlaceholderScreen(
                      title: 'TV Shows',
                      message:
                          'Sonarr is not configured on this server.',
                      icon: Icons.tv_outlined,
                    );
                  }
                  return const SonarrHomeScreen();
                },
              ),
            ],
          ),
          // Tab 3: AI Assistant
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/assistant',
                builder: (context, state) {
                  final auth = ref.read(authProvider).valueOrNull;
                  final hasAi =
                      auth?.connection?.services.ai ?? false;
                  return AiChatScreen(aiAvailable: hasAi);
                },
              ),
            ],
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

/// Placeholder for unconfigured service tabs.
class _PlaceholderScreen extends StatelessWidget {
  final String title;
  final String message;
  final IconData icon;

  const _PlaceholderScreen({
    required this.title,
    required this.message,
    required this.icon,
  });

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(title)),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 64, color: Colors.grey),
              const SizedBox(height: 16),
              Text(message,
                  style: const TextStyle(color: Colors.grey, fontSize: 16),
                  textAlign: TextAlign.center),
            ],
          ),
        ),
      ),
    );
  }
}
