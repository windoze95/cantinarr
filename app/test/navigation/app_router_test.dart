import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/widgets/app_ambient_background.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/ai_assistant/ui/codex_connection_screen.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/ui/auth_screen.dart';
import 'package:cantinarr/features/auth/ui/set_password_screen.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_shell.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_movies_tab.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_tv_tab.dart';
import 'package:cantinarr/features/dashboard/ui/requester_album_detail_screen.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/features/discover/ui/browse_grid_screen.dart';
import 'package:cantinarr/features/media_access/ui/media_access_guide.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:cantinarr/features/notifications/push_service.dart';
import 'package:cantinarr/features/monitoring/ui/monitoring_module_shell.dart';
import 'package:cantinarr/features/settings/ui/instance_edit_screen.dart';
import 'package:cantinarr/features/shell/ui/app_shell.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_module_shell.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show MethodChannel, StandardMethodCodec, MethodCall;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  // Connection entry stays usable while saved-server history loads. Give
  // navigation tests local storage rather than waiting for a plugin timeout.
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('direct TV detail Back returns to TV Discover', (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/detail/tv/225634');
    await tester.pumpAndSettle();

    expect(router.routerDelegate.currentConfiguration.uri.path,
        '/detail/tv/225634');
    expect(router.canPop(), isFalse);
    await tester.tap(find.byTooltip('Back'));
    await tester.pumpAndSettle();
    expect(router.routerDelegate.currentConfiguration.uri.path,
        '/dashboard/tv');
  });

  testWidgets('direct book detail Back returns to Books Discover',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    router.go('/detail/book/29749107?source=chaptarr&title=Ahsoka');
    await tester.pumpAndSettle();

    expect(router.routerDelegate.currentConfiguration.uri.path,
        '/detail/book/29749107');
    expect(router.canPop(), isFalse);
    await tester.tap(find.byTooltip('Back'));
    await tester.pumpAndSettle();
    expect(router.routerDelegate.currentConfiguration.uri.path,
        '/dashboard/books');
  });

  testWidgets('direct album detail Back returns to Music Discover',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _musicState);

    router.go('/detail/album/mb-1234?title=Pinkerton');
    await tester.pumpAndSettle();

    expect(router.routerDelegate.currentConfiguration.uri.path,
        '/detail/album/mb-1234');
    expect(router.canPop(), isFalse);
    await tester.tap(find.byTooltip('Back'));
    await tester.pumpAndSettle();
    expect(router.routerDelegate.currentConfiguration.uri.path,
        '/dashboard/music');
  });

  for (final cold in [true, false]) {
    for (final restoringAuth in [true, false]) {
      testWidgets('TV tap cold=$cold restoringAuth=$restoringAuth retains story and library', (tester) async {
        final (:container, :router) = await _pumpRouter(tester,
            restoringAuth ? const AuthState() : _authedState);
        const channel = MethodChannel('codes.julian.cantinarr/push');
        final payload = {'type': 'new_episode', 'media_type': 'tv',
          'tmdb_id': 225634, 'instance_id': 'tv-importing'};
        final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
        messenger.setMockMethodCallHandler(channel, (call) async =>
            call.method == 'getInitialNotification' ? payload : null);
        addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
        final service = container.read(pushServiceProvider);
        if (cold) {
          await service.handleInitialNotification();
        } else {
          const codec = StandardMethodCodec();
          await messenger.handlePlatformMessage(channel.name,
              codec.encodeMethodCall(MethodCall('onNotificationTap', payload)), (_) {});
        }
        await tester.pumpAndSettle();
        if (restoringAuth) {
          expect(router.routerDelegate.currentConfiguration.uri.path, '/login');
          (container.read(authProvider.notifier) as _FakeAuthNotifier).push(_authedState);
          await tester.pumpAndSettle();
        }
        final screen = tester.widget<MediaDetailScreen>(find.byType(MediaDetailScreen));
        expect(screen.id, 225634);
        expect(screen.instanceId, 'tv-importing');
        // The fixture has no such grant. It must still land on that library,
        // visibly unavailable, rather than another library's detail state.
        expect(find.text('Library unavailable'), findsOneWidget);
      });
    }
  }
  testWidgets('hidden Movies at login lands on the first visible tab',
      (tester) async {
    final (:container, :router) = await _pumpRouter(tester, const AuthState());
    (container.read(authProvider.notifier) as _FakeAuthNotifier).push(
        _authedState.copyWith(
            connection: _authedState.connection!
                .copyWith(hiddenDiscoverTabs: ['movie'])));
    await tester.pumpAndSettle();
    expect(
        router.routerDelegate.currentConfiguration.uri.path, '/dashboard/tv');
  });

  testWidgets('settings edits block sidebar navigation but not session expiry',
      (tester) async {
    final (:router, :container) = await _pumpRouter(tester, _adminState,
        surfaceSize: const Size(1200, 900));
    router.go('/settings/password');
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, 'unsaved-password');
    router.go('/dashboard/movies');
    await tester.pumpAndSettle();
    expect(find.text('Discard unsaved changes?'), findsOneWidget);
    await tester.tap(find.text('Keep editing'));
    await tester.pumpAndSettle();
    expect(find.byType(SetPasswordScreen), findsOneWidget);
    (container.read(authProvider.notifier) as _FakeAuthNotifier)
        .push(const AuthState());
    await tester.pumpAndSettle();
    expect(router.routerDelegate.currentConfiguration.uri.path, '/login');
    expect(find.text('Discard unsaved changes?'), findsNothing);
  });

  test('router instance stays stable across auth state changes', () {
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_authedState)),
      ],
    );
    addTearDown(container.dispose);

    final first = container.read(appRouterProvider);
    expect(first, isA<GoRouter>());

    // An auth-state change (e.g. token refresh or profile reload) must NOT
    // recreate the router — recreating it resets navigation to the initial
    // route, which is what bounced the user out of nested screens.
    (container.read(authProvider.notifier) as _FakeAuthNotifier).push(
      AuthState(
        connection: _authedState.connection!.copyWith(accessToken: 'access2'),
        user: _authedState.user,
      ),
    );

    final second = container.read(appRouterProvider);
    expect(identical(first, second), isTrue,
        reason:
            'auth changes should refresh redirects, not rebuild the router');
  });

  testWidgets('non-admin instance module routes redirect to dashboard',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/radarr/library');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('authentication returns an internal deep link to its target',
      (tester) async {
    final (:container, :router) = await _pumpRouter(tester, const AuthState());

    router.go('/settings/password');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/login');

    (container.read(authProvider.notifier) as _FakeAuthNotifier)
        .push(_authedState);
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/settings/password',
    );
  });

  testWidgets(
      'desktop secondary routes retain AppShell and hide module-global search',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(
      tester,
      _authedState,
      surfaceSize: const Size(1200, 900),
    );

    // Search is global to module pages, not every authenticated screen.
    expect(find.byType(CantinarrSearchBar), findsOneWidget);

    router.go('/settings/password');
    await tester.pumpAndSettle();

    expect(find.byType(SetPasswordScreen), findsOneWidget);
    expect(find.byType(AppShell), findsOneWidget);
    expect(find.text('CANTINARR'), findsOneWidget);
    expect(find.byType(CantinarrSearchBar), findsNothing);
  });

  testWidgets('non-admin users are redirected from admin-only root routes',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    for (final path in [
      '/downloads/queue', // servers without the capability keep old navigation
      '/downloads/history',
      '/monitoring/activity',
      '/tdarr/activity',
      '/tdarr/libraries',
      // The old module path stays admin-only through its redirect.
      '/tautulli/activity',
      '/approvals',
      '/approvals/history',
      '/agent-actions',
      '/agent-runs/1',
      '/setup',
      '/settings/credentials',
      '/settings/ai-tools',
      '/settings/change-history',
      '/settings/change-history/1',
      '/settings/users',
      '/settings/request-settings',
      '/settings/discord-notifications',
      '/settings/seerr-api',
      '/settings/push-notifications/server',
      '/settings/agent-approval-rules',
      '/settings/devices',
      '/settings/apple-tvs',
      '/settings/plex',
      '/settings/instance/new',
    ]) {
      router.go(path);
      await tester.pumpAndSettle();
      expect(
        router.routeInformationProvider.value.uri.path,
        '/dashboard/movies',
        reason: '$path must remain admin-only',
      );
    }
  });

  testWidgets('supported requesters can open Content but cannot open History', (tester) async {
    final (:router, :container) = await _pumpRouter(tester, _authedState.copyWith(
        connection: _authedState.connection!.copyWith(downloadsActivity: true)));
    router.go('/downloads/queue');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/downloads/queue');
    expect(find.text('All downloads'), findsOneWidget);
    expect(find.text('Clients'), findsNothing);
    expect(find.text('History'), findsNothing);
    router.go('/downloads/history');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/dashboard/movies');
    await tester.pumpWidget(const SizedBox());
    container.dispose();
  });

  testWidgets('old Tautulli tab paths redirect to the Monitoring module',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _adminState);

    for (final tab in ['activity', 'history', 'stats']) {
      router.go('/tautulli/$tab');
      await tester.pumpAndSettle();
      expect(router.routeInformationProvider.value.uri.path,
          '/monitoring/$tab');
      expect(find.byType(MonitoringModuleShell), findsOneWidget);
    }

    // An unknown tab lands on the first one rather than an error page.
    router.go('/tautulli/bogus');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path,
        '/monitoring/activity');
  });

  testWidgets('a requester keeps /issues — it is their My Reports inbox',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/issues');
    await tester.pumpAndSettle();
    expect(
      router.routeInformationProvider.value.uri.path,
      '/issues',
      reason:
          'the issues route is the reporter inbox for non-admins, scoped server-side',
    );
  });

  testWidgets('a requester can still open a specific issue thread',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/issues/42');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/issues/42');
  });

  testWidgets('a requester can open their ChatGPT connection settings',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/settings/chatgpt');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/settings/chatgpt');
    expect(find.byType(CodexConnectionScreen), findsOneWidget);
  });

  testWidgets('a requester can open the media server access guide',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/media-servers');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/media-servers');
    expect(find.byType(MediaAccessGuide), findsOneWidget);
  });

  testWidgets('books route requires the Chaptarr grant', (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('books route remains available with the Chaptarr grant',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/dashboard/books');
  });

  testWidgets('book detail route requires the Chaptarr grant', (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('book detail route resolves with the Chaptarr grant',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    router.go('/detail/book/29749107');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/detail/book/29749107',
    );
    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
  });

  testWidgets('book detail route preserves a pinned Chaptarr instance',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    router.go('/detail/book/29749107?instance_id=books-two');
    await tester.pumpAndSettle();

    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.foreignId, '29749107');
    expect(screen.instanceId, 'books-two');
  });

  testWidgets('music route requires the Lidarr grant', (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/dashboard/music');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('music route remains available with the Lidarr grant',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _musicState);

    router.go('/dashboard/music');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/dashboard/music');
  });

  testWidgets('album detail route requires the Lidarr grant', (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.go('/detail/album/mb-1234');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('album detail route resolves and preserves a pinned instance',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _musicState);

    router.go('/detail/album/mb-1234?instance_id=music-two');
    await tester.pumpAndSettle();

    final screen = tester.widget<RequesterAlbumDetailScreen>(
      find.byType(RequesterAlbumDetailScreen),
    );
    expect(screen.foreignId, 'mb-1234');
    expect(screen.instanceId, 'music-two');
  });

  testWidgets('a blank album detail id degrades to the Music tab',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _musicState);

    router.go('/detail/album/%20');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/dashboard/music');
  });

  testWidgets('a Chaptarr grant alone opens no music surface',
      (tester) async {
    // The two grant-only modules gate independently: holding Books must not
    // leak Music, and the redirect degrades to the movies dashboard.
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    router.go('/dashboard/music');
    await tester.pumpAndSettle();

    expect(
      router.routeInformationProvider.value.uri.path,
      '/dashboard/movies',
    );
  });

  testWidgets('instance/new carries the checklist extras into the form',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _adminState);

    // The setup checklist names the service type when it sends an admin
    // here; the form must open already on it.
    router.go('/settings/instance/new', extra: {'service_type': 'sonarr'});
    await tester.pumpAndSettle();
    var screen =
        tester.widget<InstanceEditScreen>(find.byType(InstanceEditScreen));
    expect(screen.initialServiceType, 'sonarr');
    expect(screen.serviceTypePrompt, isNull);

    // Step off and back so each case arrives like a fresh navigation.
    router.go('/settings/password');
    await tester.pumpAndSettle();

    // The download-client row names a category, not a member, so it sends a
    // selection prompt instead of a type the admin then has to correct.
    router.go('/settings/instance/new',
        extra: {'service_type_prompt': 'Select a download client'});
    await tester.pumpAndSettle();
    screen =
        tester.widget<InstanceEditScreen>(find.byType(InstanceEditScreen));
    expect(screen.initialServiceType, isNull);
    expect(screen.serviceTypePrompt, 'Select a download client');

    router.go('/settings/password');
    await tester.pumpAndSettle();

    // The generic Add Instance button sends no extra at all: both fields
    // stay null and the form falls back to its old Radarr default.
    router.go('/settings/instance/new');
    await tester.pumpAndSettle();
    screen =
        tester.widget<InstanceEditScreen>(find.byType(InstanceEditScreen));
    expect(screen.initialServiceType, isNull);
    expect(screen.serviceTypePrompt, isNull);
  });

  testWidgets('a blank book detail id degrades to the Books tab',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _booksState);

    // %20 decodes to a whitespace-only foreign id — malformed for a book.
    router.go('/detail/book/%20');
    await tester.pumpAndSettle();

    expect(router.routeInformationProvider.value.uri.path, '/dashboard/books');
  });

  testWidgets('invalid TV links stay on TV and both dashboard tabs remain usable',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _adminState);
    for (final id in ['0', '-1', 'not-a-number']) {
      router.go('/dashboard/tv');
      await tester.pumpAndSettle();
      router.push('/detail/tv/$id');
      await tester.pumpAndSettle();
      expect(router.routerDelegate.currentConfiguration.uri.path, '/dashboard/tv');
      expect(find.byType(DashboardTvTab), findsOneWidget);
      expect(find.byType(DashboardMoviesTab), findsNothing);
      expect(tester.takeException(), isNull);
      for (final index in [0, 1, 0, 1]) {
        tester.widget<DashboardShell>(find.byType(DashboardShell).last)
            .onTabChanged(index);
        await tester.pumpAndSettle();
        final shell = tester.widget<DashboardShell>(find.byType(DashboardShell).last);
        expect(shell.currentIndex, index);
        expect(find.byType(DashboardMoviesTab), index == 0 ? findsOneWidget : findsNothing);
        expect(find.byType(DashboardTvTab), index == 1 ? findsOneWidget : findsNothing);
        expect(router.routerDelegate.currentConfiguration.uri.path,
            index == 0 ? '/dashboard/movies' : '/dashboard/tv');
        expect(tester.takeException(), isNull);
      }
    }
  });

  testWidgets('malformed parameter routes redirect without throwing',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _adminState);

    for (final path in [
      '/detail/movie/not-a-number',
      '/detail/movie/0',
      '/detail/podcast/12',
      '/browse/podcast/top-rated',
      '/browse/movie/bogus',
      '/browse/movie/recommendations',
      '/browse/movie/on-the-air',
    ]) {
      router.go(path);
      await tester.pumpAndSettle();
      expect(
        router.routeInformationProvider.value.uri.path,
        '/dashboard/movies',
        reason: '$path must not reach a detail or browse screen',
      );
    }

    router.go('/browse/tv/now-playing');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/dashboard/tv');

    router.go('/settings/users/not-a-number/request-settings');
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/settings/users');

    router.go('/settings/change-history/not-a-number');
    await tester.pumpAndSettle();
    expect(
      router.routeInformationProvider.value.uri.path,
      '/settings/change-history',
    );
  });

  // Scaffolds are transparent by theme, so every routed page must paint its
  // own ambient backdrop — a page without one lets the previous route show
  // through mid-transition as a double exposure.
  testWidgets('routed pages paint their own opaque ambient backdrop',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    // Module page: backdrop on the shell page AND the module shell page.
    expect(
      find.ancestor(
        of: find.byType(DashboardShell),
        matching: find.byType(AppAmbientBackground),
      ),
      findsNWidgets(2),
    );

    // Pushed secondary route: its own backdrop plus the shell page's.
    router.push('/settings/password');
    await tester.pumpAndSettle();
    expect(
      find.ancestor(
        of: find.byType(SetPasswordScreen),
        matching: find.byType(AppAmbientBackground),
      ),
      findsNWidgets(2),
    );

    router.push('/media-servers');
    await tester.pumpAndSettle();
    expect(
      find.ancestor(
        of: find.byType(MediaAccessGuide),
        matching: find.byType(AppAmbientBackground),
      ),
      findsNWidgets(2),
    );

    router.push('/browse/movie/top-rated');
    await tester.pumpAndSettle();
    expect(
      find.ancestor(
        of: find.byType(BrowseGridScreen),
        matching: find.byType(AppAmbientBackground),
      ),
      findsNWidgets(2),
    );
  });

  testWidgets('a requester can open a browse grid from a bare link',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _authedState);

    router.push('/browse/movie/discover?genres=28');
    await tester.pumpAndSettle();
    expect(find.byType(BrowseGridScreen), findsOneWidget);
    // The query string reached the grid.
    final grid = tester.widget<BrowseGridScreen>(find.byType(BrowseGridScreen));
    expect(grid.query.filters.genreIds, [28]);
  });

  testWidgets('the login page paints its own opaque ambient backdrop',
      (tester) async {
    await _pumpRouter(tester, const AuthState());

    expect(
      find.ancestor(
        of: find.byType(AuthScreen),
        matching: find.byType(AppAmbientBackground),
      ),
      findsOneWidget,
    );
  });

  testWidgets('module switches dissolve the incoming shell over the old one',
      (tester) async {
    final (:router, container: _) = await _pumpRouter(tester, _adminState);

    router.go('/sonarr/library');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 140));

    // Mid-dissolve: both module pages are mounted and the incoming page is
    // fading in (over its opaque backdrop) rather than double-exposing.
    expect(find.byType(DashboardShell), findsOneWidget);
    expect(find.byType(SonarrModuleShell), findsOneWidget);
    final fades = tester.widgetList<FadeTransition>(
      find.ancestor(
        of: find.byType(SonarrModuleShell),
        matching: find.byType(FadeTransition),
      ),
    );
    expect(
      fades.any((f) => f.opacity.value > 0 && f.opacity.value < 1),
      isTrue,
      reason: 'incoming module page should be mid-fade',
    );

    // Bounded pumps (not pumpAndSettle): the stubbed Sonarr library shows an
    // indeterminate spinner, which never settles. 140+200+100ms covers the
    // 280ms dissolve plus a frame for the outgoing route's removal.
    await tester.pump(const Duration(milliseconds: 200));
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.byType(DashboardShell), findsNothing);
    expect(find.byType(SonarrModuleShell), findsOneWidget);
  });
}

const _authedState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(ai: true),
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

const _booksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

const _musicState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    instances: [
      ServiceInstance(
        id: 'music',
        serviceType: 'lidarr',
        name: 'Music',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

Future<({ProviderContainer container, GoRouter router})> _pumpRouter(
  WidgetTester tester,
  AuthState authState, {
  Size? surfaceSize,
}) async {
  if (surfaceSize != null) {
    tester.view.physicalSize = surfaceSize;
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
  }

  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(authState)),
      backendClientProvider.overrideWithValue(_fakeDio()),
      webSocketClientProvider.overrideWith((ref) => _QuietWebSocket()),
      pushServiceProvider.overrideWith((ref) => PushService(ref, supported: true)),
    ],
  );
  addTearDown(container.dispose);

  await container.read(authProvider.future);
  await container.pump();
  final router = container.read(appRouterProvider);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return (container: container, router: router);
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;

  void push(AuthState next) => state = AsyncData(next);

  // InstanceEditScreen (now reachable via /settings/instance/new) reads the
  // user directory on mount; the base impl needs an AuthService this harness
  // never wires up, so answer an empty directory instead.
  @override
  Future<List<UserSummary>> listUsers() async => const [];
}

Dio _fakeDio() {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter();
  return dio;
}

class _JsonAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body = switch (options.path) {
      '/api/downloads/activity' || '/api/downloads/summary' => {
          'count': 0, 'complete': true, 'scope': 'all', 'user_scope': 'all',
          'groups': [], 'jobs': [],
        },
      '/api/trakt/anticipated' => [],
      '/api/media/tv/225634' => {
          'id': 225634,
          'name': 'Nashville',
          'seasons': <dynamic>[],
        },
      _ => {
          'page': 1,
          'results': [],
          'total_pages': 0,
          'total_results': 0,
        },
    };
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _QuietWebSocket extends WebSocketClient {
  _QuietWebSocket()
      : super(getServerUrl: () => null, getAccessToken: () => null);
  @override
  void ensureConnected() {}
}
