import 'dart:async';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/plex_auth_service.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/ui/auth_screen.dart';
import 'package:cantinarr/features/auth/ui/plex_continue_screen.dart';
import 'package:cantinarr/features/settings/ui/plex_auth_settings_screen.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'oidc_screens_test.dart' show ScreenAuth;
import 'oidc_service_test.dart' show MemoryStorage;
import 'plex_auth_service_test.dart' show PlexAuthFake;

class PlexScreenAuth extends ScreenAuth {
  final bool plex, only;
  PlexScreenAuth({this.plex = true, this.only = false});
  @override
  Future<({String serverUrl, ServerStatus status})> checkServer(
          String url) async =>
      (
        serverUrl: url,
        status: ServerStatus(
            needsSetup: false,
            plexAvailable: plex,
            ssoAvailable: only,
            ssoOnly: only)
      );
}

class PlexSettingsFake extends AuthService {
  List<dynamic>? confirmed;
  @override
  Future<Map<String, dynamic>> externalSignInRequest(String server, String path,
      {String method = 'GET',
      String? accessToken,
      Map<String, dynamic>? data}) async {
    if (path.endsWith('/confirm')) {
      confirmed = data!['mappings'] as List;
      return {'status': 'confirmed'};
    }
    if (path.endsWith('/candidates')) {
      return {
        'candidates': [
          {
            'user_id': 2,
            'username': 'friend',
            'plex_account_id': 42,
            'plex_username': 'Friend',
            'email': 'friend@example.test',
            'confirmed': confirmed != null,
            'servers': [
              {'name': 'Plex One'}
            ]
          },
          {
            'user_id': 3,
            'username': 'ambiguous',
            'plex_account_id': 0,
            'plex_username': '',
            'email': '',
            'confirmed': false,
            'reason': 'No unique account match',
            'servers': [
              {'name': 'Plex Two'}
            ]
          },
        ]
      };
    }
    return {'enabled': false, 'auto_create': false};
  }
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));
  testWidgets(
      'router ignores the stale attempt during a delayed native storage refresh',
      (tester) async {
    final service =
        PlexAuthService(PlexAuthFake(), MemoryStorage(), isWeb: false);
    final pending = await service.start('https://original.example');
    final refreshed = Completer<PlexPending?>();
    var reads = 0;
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => ScreenAuth(signedIn: true)),
      authServiceProvider.overrideWithValue(PlexSettingsFake()),
      plexAuthServiceProvider.overrideWithValue(service),
      plexPendingProvider.overrideWith((ref) async {
        if (++reads == 1) return pending;
        return refreshed.future;
      }),
    ]);
    addTearDown(container.dispose);
    final router = container.read(appRouterProvider);
    addTearDown(router.dispose);
    await tester.pumpWidget(UncontrolledProviderScope(
        container: container, child: MaterialApp.router(routerConfig: router)));
    await tester.pumpAndSettle();
    expect(
        router.routerDelegate.currentConfiguration.uri.path, '/plex/continue');
    await service.completed();
    container.invalidate(plexPendingProvider);
    expect(container.read(plexPendingProvider).isLoading, isTrue);
    expect(container.read(plexPendingProvider).valueOrNull, isNotNull);
    router.go('/settings/plex-auth');
    await tester.pumpAndSettle();
    expect(router.routerDelegate.currentConfiguration.uri.path,
        '/settings/plex-auth');
    expect(find.byType(PlexContinueScreen), findsNothing);
    refreshed.complete(null);
    await tester.pumpAndSettle();
    await tester.pumpWidget(const SizedBox.shrink());
  });
  for (final only in [false, true]) {
    testWidgets('Plex login button respects recovery label ($only)',
        (tester) async {
      await tester.pumpWidget(ProviderScope(overrides: [
        authProvider.overrideWith(() => PlexScreenAuth(only: only))
      ], child: const MaterialApp(home: AuthScreen())));
      await tester.pumpAndSettle();
      await tester.enterText(
          find.byType(TextField).first, 'https://media.example');
      await tester.tap(find.text('Continue'));
      await tester.pumpAndSettle();
      expect(
          find.text(only
              ? 'Continue with Plex (administrator recovery)'
              : 'Continue with Plex'),
          findsOneWidget);
      expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
    });
  }
  testWidgets(
      'review starts unselected and refuses ambiguous mapping selection',
      (tester) async {
    tester.view.physicalSize = const Size(1000, 1600);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final fake = PlexSettingsFake();
    await tester.pumpWidget(ProviderScope(overrides: [
      authProvider.overrideWith(() => ScreenAuth(signedIn: true)),
      authServiceProvider.overrideWithValue(fake)
    ], child: const MaterialApp(home: PlexAuthSettingsScreen())));
    await tester.pumpAndSettle();
    expect(
        tester
            .widgetList<SwitchListTile>(find.byType(SwitchListTile))
            .every((s) => !s.value),
        isTrue);
    await tester.ensureVisible(find.text('Load fresh Plex accounts'));
    await tester.tap(find.text('Load fresh Plex accounts'));
    await tester.pumpAndSettle();
    final tiles = tester
        .widgetList<CheckboxListTile>(find.byType(CheckboxListTile))
        .toList();
    expect(tiles.map((t) => t.value), [false, false]);
    expect(tiles.last.onChanged, isNull);
    await tester.ensureVisible(find.text('friend → Friend'));
    await tester.tap(find.text('friend → Friend'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Confirm selected (1)'));
    await tester.tap(find.text('Confirm selected (1)'));
    await tester.pumpAndSettle();
    expect(fake.confirmed, [
      {'user_id': 2, 'plex_account_id': 42}
    ]);
  });
  testWidgets(
      'restart recovers original server and purpose and browser failure keeps reopen controls',
      (tester) async {
    final service = PlexAuthService(PlexAuthFake(), MemoryStorage(),
        isWeb: false, openBrowser: (_) async => false);
    await service.start('https://original.example');
    await tester.pumpWidget(ProviderScope(
        overrides: [
          authProvider.overrideWith(() => ScreenAuth()),
          plexAuthServiceProvider.overrideWithValue(service)
        ],
        child: MaterialApp(
            home: PlexContinueScreen(
                uri: Uri.parse(
                    '/plex/continue?server=https://other.example&purpose=link')))));
    await tester.pumpAndSettle();
    expect(find.text('https://original.example'), findsOneWidget);
    expect(find.text('Signing in to Cantinarr'), findsOneWidget);
    expect(find.text('https://other.example'), findsNothing);
    await tester.tap(find.text('Reopen Plex'));
    await tester.pumpAndSettle();
    expect(find.textContaining('browser did not open'), findsOneWidget);
    expect(find.text('Check now'), findsOneWidget);
    expect(find.text('Cancel'), findsOneWidget);
    expect(await service.load(), isNotNull);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}
