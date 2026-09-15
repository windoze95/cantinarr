import 'dart:async';
import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/logic/saved_servers_provider.dart';
import 'package:cantinarr/features/auth/ui/auth_screen.dart';
import 'package:cantinarr/features/auth/ui/saved_server_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _home = SavedServer(url: 'http://home.local:8585', name: 'Home');
const _remote = SavedServer(url: 'https://remote.test/library', name: 'Remote');

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  Future<ProviderContainer> showAuth(
    WidgetTester tester,
    _ScreenAuth auth, {
    List<SavedServer> saved = const [],
    String? origin,
    Completer<SharedPreferences>? delayedPreferences,
    SavedServersNotifier? history,
    bool preferencesUnavailable = false,
  }) async {
    SharedPreferences.setMockInitialValues({
      SavedServersNotifier.storageKey:
          jsonEncode(saved.map((s) => s.toJson()).toList()),
    });
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => auth),
      authPageOriginProvider.overrideWithValue(origin),
      if (history != null) savedServersProvider.overrideWith(() => history),
      if (delayedPreferences != null)
        sharedPreferencesProvider
            .overrideWith((_) => delayedPreferences.future),
      if (preferencesUnavailable)
        sharedPreferencesProvider.overrideWith(
            (_) async => throw StateError('Preferences unavailable')),
    ]);
    addTearDown(container.dispose);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(theme: AppTheme.dark, home: const AuthScreen()),
    ));
    return container;
  }

  testWidgets('last server opens directly to its current sign-in methods',
      (tester) async {
    final auth = _ScreenAuth();
    final container = await showAuth(tester, auth, saved: [_home, _remote]);
    await tester.pumpAndSettle();
    expect(auth.checks, [_home.url]);
    expect(find.text('Continue'), findsNothing);
    expect(find.text('Home'), findsOneWidget);
    expect(find.text('Continue with Family'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
    await tester.tap(find.text('Change server'));
    await tester.pumpAndSettle();
    expect(find.text('Saved servers'), findsOneWidget);
    expect(find.text('Selected'), findsOneWidget);
    await tester.tap(find.byKey(ValueKey('saved-server-${_remote.url}')));
    await tester.pumpAndSettle();
    expect(auth.checks, [_home.url, _remote.url]);
    expect(find.text('Remote'), findsOneWidget);
    expect(find.text('Continue with Family'), findsNothing);
    expect((await container.read(savedServersProvider.future)).first.url,
        _home.url,
        reason: 'selection alone is not a successful sign-in');
  });

  testWidgets('no history keeps native entry and web origin detection',
      (tester) async {
    final native = _ScreenAuth();
    await showAuth(tester, native);
    await tester.pumpAndSettle();
    expect(native.checks, isEmpty);
    expect(find.widgetWithText(TextField, 'Server URL'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    final web = _ScreenAuth();
    await showAuth(tester, web, origin: _remote.url);
    await tester.pumpAndSettle();
    expect(web.checks, [_remote.url]);
    expect(find.text('Sign In'), findsOneWidget);
  });

  testWidgets('saved server takes precedence over the hosting origin',
      (tester) async {
    final auth = _ScreenAuth();
    await showAuth(tester, auth, saved: [_home], origin: _remote.url);
    await tester.pumpAndSettle();
    expect(auth.checks, [_home.url]);
  });

  testWidgets('unreachable saved server keeps shortcuts and offers retry',
      (tester) async {
    final auth = _ScreenAuth()..fail = true;
    final container = await showAuth(tester, auth, saved: [_home, _remote]);
    await tester.pumpAndSettle();
    expect(find.text('Retry'), findsOneWidget);
    expect(find.text('Change server'), findsOneWidget);
    expect((await container.read(savedServersProvider.future)).length, 2);
    auth.fail = false;
    await tester.ensureVisible(find.text('Retry'));
    await tester.tap(find.text('Retry'));
    await tester.pumpAndSettle();
    expect(find.text('Sign In'), findsOneWidget);
    expect(auth.checks, [_home.url, _home.url]);
  });

  testWidgets(
      'forget removes selected shortcut, undo restores it without probing',
      (tester) async {
    final auth = _ScreenAuth();
    final container = await showAuth(tester, auth, saved: [_home, _remote]);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Change server'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Options for Home'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Forget'));
    await tester.pumpAndSettle();
    expect((await container.read(savedServersProvider.future)).first.url,
        _remote.url);
    expect(
        tester.widget<TextField>(find.byType(TextField).first).controller!.text,
        isEmpty);
    await tester.tap(find.text('Undo'));
    await tester.pumpAndSettle();
    expect((await container.read(savedServersProvider.future)).first.url,
        _home.url);
    expect(auth.checks, [_home.url]);
    expect(find.text('Saved servers'), findsOneWidget);
  });

  testWidgets('late probe cannot overwrite a newer choice or disposed screen',
      (tester) async {
    final auth = _ScreenAuth()..held = Completer();
    await showAuth(tester, auth, saved: [_home, _remote]);
    await tester.pump();
    await tester.pump();
    expect(auth.checks, [_home.url]);
    await tester.tap(find.byKey(ValueKey('saved-server-${_remote.url}')));
    await tester.pumpAndSettle();
    expect(find.text('Remote'), findsOneWidget);
    auth.held!.complete(
        (serverUrl: _home.url, status: const ServerStatus(needsSetup: true)));
    await tester.pumpAndSettle();
    expect(find.text('Remote'), findsOneWidget);
    expect(find.text('Create your admin account'), findsNothing);
    await tester.tap(find.text('Change server'));
    await tester.pumpAndSettle();
    auth.held = Completer();
    await tester.tap(find.byKey(ValueKey('saved-server-${_home.url}')));
    await tester.pumpWidget(const SizedBox());
    auth.held!.completeError(StateError('Server unavailable'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('undo still works after leaving the sign-in screen',
      (tester) async {
    final container = await showAuth(tester, _ScreenAuth(), saved: [_home]);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Change server'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Options for Home'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Forget'));
    await tester.pumpAndSettle();
    tester.state<NavigatorState>(find.byType(Navigator)).pushReplacement(
        MaterialPageRoute<void>(builder: (_) => const Scaffold()));
    await tester.pumpAndSettle();
    expect(find.byType(AuthScreen), findsNothing);
    await tester.tap(find.text('Undo'));
    await tester.pumpAndSettle();
    expect((await container.read(savedServersProvider.future)).single.url,
        _home.url);
  });

  testWidgets(
      'typing and connect-link navigation cancel a pending default probe',
      (tester) async {
    final auth = _ScreenAuth()..held = Completer();
    await showAuth(tester, auth, saved: [_home]);
    await tester.pump();
    await tester.pump();
    await tester.enterText(find.byType(TextField).first, _remote.url);
    auth.held!.complete(
        (serverUrl: _home.url, status: const ServerStatus(needsSetup: false)));
    await tester.pumpAndSettle();
    expect(
        tester.widget<TextField>(find.byType(TextField).first).controller!.text,
        _remote.url);
    auth.held = Completer();
    await tester.tap(find.byKey(ValueKey('saved-server-${_home.url}')));
    await tester.ensureVisible(find.text('Have a connection link?'));
    await tester.tap(find.text('Have a connection link?'));
    await tester.pump();
    auth.held!.complete(
        (serverUrl: _home.url, status: const ServerStatus(needsSetup: false)));
    await tester.pumpAndSettle();
    expect(find.text('Paste your connection link'), findsOneWidget);
  });

  testWidgets('late history cannot replace manual entry', (tester) async {
    final ready = Completer<SharedPreferences>();
    final auth = _ScreenAuth();
    await showAuth(tester, auth, saved: [_home], delayedPreferences: ready);
    await tester.pump();
    await tester.enterText(find.byType(TextField).first, _remote.url);
    ready.complete(await SharedPreferences.getInstance());
    await tester.pumpAndSettle();
    expect(auth.checks, isEmpty);
    expect(
        tester.widget<TextField>(find.byType(TextField).first).controller!.text,
        _remote.url);
  });

  testWidgets(
      'forget cancels a probe immediately and cannot undo a later choice',
      (tester) async {
    final auth = _ScreenAuth()..held = Completer();
    final history = _DelayedForget();
    await showAuth(tester, auth, saved: [_home, _remote], history: history);
    await tester.pump();
    await tester.pump();
    await tester.tap(find.byTooltip('Options for Home'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    await tester.tap(find.text('Forget'));
    await tester.pump();
    auth.held!.complete(
        (serverUrl: _home.url, status: const ServerStatus(needsSetup: false)));
    await tester.pumpAndSettle();
    expect(find.byType(ServerBadge), findsNothing);
    await tester.tap(find.byKey(ValueKey('saved-server-${_remote.url}')));
    await tester.pumpAndSettle();
    history.ready.complete();
    await tester.pumpAndSettle();
    expect(find.text('Remote'), findsOneWidget);
    expect(find.byType(ServerBadge), findsOneWidget);
  });

  testWidgets('explicit authentication cancels an automatic server check',
      (tester) async {
    final auth = _ScreenAuth()..held = Completer();
    await showAuth(tester, auth, saved: [_home]);
    await tester.pump();
    await tester.pump();
    auth.startExplicitSignIn();
    auth.held!.complete(
        (serverUrl: _home.url, status: const ServerStatus(needsSetup: false)));
    await tester.pumpAndSettle();
    expect(find.byType(ServerBadge), findsNothing);
  });

  testWidgets('a restored session is never replaced by a saved default',
      (tester) async {
    final auth = _ScreenAuth()..restored = true;
    await showAuth(tester, auth, saved: [_home]);
    await tester.pumpAndSettle();
    expect(auth.checks, isEmpty);
  });

  testWidgets('unavailable history still permits manual sign-in',
      (tester) async {
    final auth = _ScreenAuth();
    await showAuth(tester, auth, preferencesUnavailable: true);
    await tester.pumpAndSettle();
    expect(
        find.textContaining('Saved servers are unavailable'), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, _remote.url);
    await tester.tap(find.text('Continue'));
    await tester.pumpAndSettle();
    expect(find.text('Sign In'), findsOneWidget);
  });

  testWidgets('long names and URLs fit narrow screens with larger text',
      (tester) async {
    tester.view.physicalSize = const Size(320, 740);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    tester.platformDispatcher.textScaleFactorTestValue = 1.5;
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    const longServer = SavedServer(
      url: 'https://a-very-long-server-address-for-the-household.test/media',
      name: 'The household media server with a very long display name',
    );
    await showAuth(tester, _ScreenAuth(), saved: [longServer, _home]);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.ensureVisible(find.text('Change server'));
    await tester.tap(find.text('Change server'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester
        .ensureVisible(find.byTooltip('Options for ${longServer.name}'));
    expect(tester.takeException(), isNull);
  });
}

class _ScreenAuth extends AuthNotifier {
  final checks = <String>[];
  bool fail = false, restored = false;
  Completer<({String serverUrl, ServerStatus status})>? held;

  @override
  Future<AuthState> build() async => restored
      ? const AuthState(
          connection: BackendConnection(
              serverUrl: 'https://active.test',
              accessToken: 'access',
              refreshToken: 'refresh'),
          user: UserProfile(id: 1, username: 'viewer', role: 'user'))
      : const AuthState();

  @override
  Future<({String serverUrl, ServerStatus status})> checkServer(
      String url) async {
    checks.add(url);
    if (url == _home.url && held != null) return held!.future;
    if (fail) throw StateError('Server unavailable');
    return (
      serverUrl: url,
      status: ServerStatus(
          needsSetup: false,
          ssoAvailable: url == _home.url,
          ssoProvider: 'Family')
    );
  }

  void startExplicitSignIn() {
    state = const AsyncData(AuthState(isLoading: true));
  }
}

class _DelayedForget extends SavedServersNotifier {
  final ready = Completer<void>();
  @override
  Future<void> forget(String url) async {
    await ready.future;
    await super.forget(url);
  }
}
