import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../features/ai_assistant/ui/ai_chat_screen.dart';
import '../features/auth/logic/auth_provider.dart';
import '../features/auth/ui/invite_screen.dart';
import '../features/auth/ui/login_screen.dart';
import '../features/discover/data/tmdb_models.dart';
import '../features/media_detail/ui/media_detail_screen.dart';
import '../features/radarr/ui/movies_tab_screen.dart';
import '../features/settings/ui/settings_screen.dart';
import '../features/setup_wizard/ui/plex_setup_guide.dart';
import '../features/setup_wizard/ui/setup_wizard_screen.dart';
import '../features/sonarr/ui/tv_shows_tab_screen.dart';
import '../features/shell/ui/app_shell.dart';

final _rootNavigatorKey = GlobalKey<NavigatorState>();

/// Central router configuration using GoRouter with ShellRoute for tabs.
/// Redirects unauthenticated users to /login.
final appRouterProvider = Provider<GoRouter>((ref) {
  final authState = ref.watch(authProvider);

  return GoRouter(
    navigatorKey: _rootNavigatorKey,
    initialLocation: '/radarr',
    redirect: (context, state) {
      final auth = authState.valueOrNull;
      final isAuthenticated = auth?.isAuthenticated ?? false;
      final isAuthRoute = state.matchedLocation == '/login' ||
          state.matchedLocation == '/invite';

      if (!isAuthenticated && !isAuthRoute) return '/login';
      if (isAuthenticated && isAuthRoute) return '/radarr';
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
          // Tab 0: Movies (discovery + Radarr library)
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/radarr',
                builder: (context, state) => const MoviesTabScreen(),
              ),
            ],
          ),
          // Tab 1: TV Shows (discovery + Sonarr library)
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/sonarr',
                builder: (context, state) => const TvShowsTabScreen(),
              ),
            ],
          ),
          // Tab 2: AI Assistant
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
