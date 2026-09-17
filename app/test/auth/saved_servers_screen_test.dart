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
import 'package:cantinarr/features/auth/ui/server_setup_help.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
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
    String? passkeyPlatform,
    Future<bool> Function(Uri)? guideLaunch,
  }) async {
    SharedPreferences.setMockInitialValues({
      SavedServersNotifier.storageKey:
          jsonEncode(saved.map((s) => s.toJson()).toList()),
    });
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => auth),
      authPageOriginProvider.overrideWithValue(origin),
      authPasskeyPlatformProvider.overrideWith((ref) async => passkeyPlatform),
      if (guideLaunch != null)
        serverSetupGuideLauncherProvider.overrideWithValue(guideLaunch),
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

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.ensureVisible(find.text(label));
    await tester.tap(find.text(label));
    await tester.pumpAndSettle();
  }

  testWidgets('last server stays on entry until an explicit connection',
      (tester) async {
    final auth = _ScreenAuth();
    final container = await showAuth(tester, auth, saved: [_home, _remote]);
    await tester.pumpAndSettle();
    expect(auth.checks, [_home.url]);
    expect(find.text('Connect'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Password'), findsNothing);
    await tap(tester, 'Connect');
    expect(find.text('Continue with Family'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
    await tester.tap(find.text('Change server'));
    await tester.pumpAndSettle();
    await tap(tester, 'Saved servers');
    expect(find.text('Saved servers'), findsOneWidget);
    expect(find.text('Selected'), findsOneWidget);
    await tester.ensureVisible(find.byKey(ValueKey('saved-server-${_remote.url}')));
    await tester.tap(find.byKey(ValueKey('saved-server-${_remote.url}')));
    await tester.pumpAndSettle();
    expect(auth.checks, [_home.url, _home.url, _remote.url]);
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
    expect(find.widgetWithText(TextField, 'Link or Cantinarr address'), findsOneWidget);
    final connectionField = tester.widget<TextField>(
      find.byKey(const ValueKey('connection-entry')),
    );
    expect(connectionField.decoration?.helperText, isNull);
    expect(find.textContaining('computer or NAS'), findsOneWidget);
    expect(find.text('Set up a Cantinarr server'), findsOneWidget);
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
    expect(find.text('Retry saved server'), findsOneWidget);
    expect(find.text('Connect'), findsOneWidget);
    expect((await container.read(savedServersProvider.future)).length, 2);
    auth.fail = false;
    await tap(tester, 'Retry saved server');
    expect(find.text('Sign In'), findsNothing);
    expect(find.text('Retry saved server'), findsNothing);
    expect(auth.checks, [_home.url, _home.url]);
  });

  testWidgets(
      'forget removes selected shortcut, undo restores it without probing',
      (tester) async {
    final auth = _ScreenAuth();
    final container = await showAuth(tester, auth, saved: [_home, _remote]);
    await tester.pumpAndSettle();
    await tap(tester, 'Saved servers');
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
    await tap(tester, 'Saved servers');
    await tester.ensureVisible(find.byKey(ValueKey('saved-server-${_remote.url}')));
    await tester.tap(find.byKey(ValueKey('saved-server-${_remote.url}')));
    await tester.pumpAndSettle();
    expect(find.text('Remote'), findsOneWidget);
    auth.held!.complete(
        (serverUrl: _home.url, status: const ServerStatus(needsSetup: true)));
    await tester.pumpAndSettle();
    expect(find.text('Remote'), findsOneWidget);
    expect(find.text('Create your admin account'), findsNothing);
    await tap(tester, 'Change server');
    auth.held = Completer();
    await tester.ensureVisible(find.byKey(ValueKey('saved-server-${_home.url}')));
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
    await tap(tester, 'Saved servers');
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
      'typing a link cancels the pending default probe',
      (tester) async {
    final auth = _ScreenAuth()..held = Completer();
    await showAuth(tester, auth, saved: [_home]);
    await tester.pump();
    await tester.pump();
    const link = 'cantinarr://connect?token=invitation&server=https%3A%2F%2Fremote.test';
    await tester.enterText(find.byType(TextField).first, link);
    auth.held!.complete(
        (serverUrl: _home.url, status: const ServerStatus(needsSetup: false)));
    await tester.pumpAndSettle();
    expect(
        tester.widget<TextField>(find.byType(TextField).first).controller!.text,
        link);
    expect(find.text('Connect to Cantinarr'), findsOneWidget);
    await tap(tester, 'Connect');
    expect(auth.links, [link]);
    expect(auth.checks, [_home.url]);
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

  testWidgets('a connection link can replace a pending manual server check',
      (tester) async {
    final auth = _ScreenAuth()..held = Completer();
    await showAuth(tester, auth);
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), _home.url);
    await tester.ensureVisible(find.text('Connect'));
    await tester.tap(find.text('Connect'));
    await tester.pump();
    const link = 'cantinarr://connect?token=invitation&server=https%3A%2F%2Fremote.test';
    await tester.enterText(find.byType(TextField), link);
    auth.held!.complete((serverUrl: _home.url,
        status: const ServerStatus(needsSetup: true)));
    await tester.pumpAndSettle();
    await tap(tester, 'Connect');
    expect(auth.links, [link]);
    expect(find.text('Create your admin account'), findsNothing);
  });

  testWidgets(
      'forget cancels a probe immediately and cannot undo a later choice',
      (tester) async {
    final auth = _ScreenAuth()..held = Completer();
    final history = _DelayedForget();
    await showAuth(tester, auth, saved: [_home, _remote], history: history);
    await tester.pump();
    await tester.pump();
    await tap(tester, 'Saved servers');
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
    await tester.pump();
    expect(find.byType(ServerBadge), findsNothing);
  });

  testWidgets('a restored session is never replaced by a saved default',
      (tester) async {
    final auth = _ScreenAuth()..restored = true;
    await showAuth(tester, auth, saved: [_home]);
    await tester.pumpAndSettle();
    expect(auth.checks, isEmpty);
  });

  testWidgets('a fresh connection link signs in without probing it as an address',
      (tester) async {
    final auth = _ScreenAuth();
    final container = await showAuth(tester, auth);
    await tester.pumpAndSettle();
    const link = 'cantinarr://connect?token=invitation&server=https%3A%2F%2Fremote.test';
    await tester.enterText(find.byKey(const ValueKey('connection-entry')), link);
    await tap(tester, 'Connect');
    expect(auth.links, [link]);
    expect(auth.checks, isEmpty);
    expect(container.read(authProvider).valueOrNull!.isAuthenticated, isTrue);
  });

  testWidgets('incomplete invitations stay editable and never reach the network',
      (tester) async {
    final auth = _ScreenAuth();
    await showAuth(tester, auth);
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField),
        'cantinarr://connect?token=private-invitation');
    await tap(tester, 'Connect');
    expect(find.textContaining('connection link is incomplete'), findsOneWidget);
    expect(auth.checks, isEmpty);
    expect(auth.links, isEmpty);
    await tester.enterText(find.byType(TextField), 'home.local:8585');
    await tap(tester, 'Connect');
    expect(auth.checks, ['home.local:8585']);
    expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
  });

  testWidgets('expired invitations show the error and preserve the pasted link',
      (tester) async {
    final auth = _ScreenAuth()..signInError = 'Connection link expired. Ask your admin for a new link.';
    await showAuth(tester, auth);
    await tester.pumpAndSettle();
    const link = 'cantinarr://connect?token=expired&server=https%3A%2F%2Fremote.test';
    await tester.enterText(find.byType(TextField), link);
    await tap(tester, 'Connect');
    expect(find.textContaining('Connection link expired'), findsOneWidget);
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, link);
    expect(auth.checks, isEmpty);
  });

  testWidgets('repeated keyboard submissions redeem an invitation only once',
      (tester) async {
    final auth = _ScreenAuth()..pendingSignIn = Completer<void>();
    await showAuth(tester, auth);
    await tester.pumpAndSettle();
    const link = 'cantinarr://connect?token=once&server=https%3A%2F%2Fremote.test';
    await tester.enterText(find.byType(TextField), link);
    final submit = tester.widget<TextField>(find.byType(TextField)).onSubmitted!;
    submit(link);
    submit(link);
    await tester.pump();
    expect(auth.links, [link]);
    expect(tester.widget<TextField>(find.byType(TextField)).enabled, isFalse);
    auth.pendingSignIn!.complete();
    await tester.pumpAndSettle();
  });

  testWidgets('an address opens setup and system back returns to the same input',
      (tester) async {
    final auth = _ScreenAuth()
      ..serverStatus = const ServerStatus(needsSetup: true);
    await showAuth(tester, auth);
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), _remote.url);
    await tap(tester, 'Connect');
    expect(find.text('Create your admin account'), findsOneWidget);
    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.text('Connect to Cantinarr'), findsOneWidget);
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text,
        _remote.url);
  });

  testWidgets('supported saved server offers passkey sign-in on entry',
      (tester) async {
    final auth = _ScreenAuth()..serverStatus = _passkeyStatus;
    final container = await showAuth(tester, auth, saved: [_home],
        passkeyPlatform: 'ios');
    await tester.pumpAndSettle();
    expect(find.text('For Home'), findsOneWidget);
    expect(find.text(_home.url), findsWidgets);
    expect(find.widgetWithText(TextField, 'Password'), findsNothing);
    await tap(tester, 'Sign in with passkey');
    expect(auth.passkeys, [_home.url]);
    expect(container.read(authProvider).valueOrNull!.isAuthenticated, isTrue);
  });

  testWidgets('cancelled passkey stays on entry and can be retried',
      (tester) async {
    final auth = _ScreenAuth()
      ..serverStatus = _passkeyStatus
      ..signInError = 'Passkey sign-in was cancelled.';
    await showAuth(tester, auth, saved: [_home], passkeyPlatform: 'ios');
    await tester.pumpAndSettle();
    await tap(tester, 'Sign in with passkey');
    expect(find.text('Passkey sign-in was cancelled.'), findsOneWidget);
    expect(find.text('Connect to Cantinarr'), findsOneWidget);
    auth.signInError = null;
    await tap(tester, 'Sign in with passkey');
    expect(auth.passkeys, [_home.url, _home.url]);
  });

  for (final scenario in [
    (name: 'no saved server', saved: false, platform: 'ios', status: _passkeyStatus),
    (name: 'unsupported device', saved: true, platform: null, status: _passkeyStatus),
    (name: 'missing native association', saved: true, platform: 'android', status: _passkeyStatus),
    (name: 'server needs setup', saved: true, platform: 'ios',
      status: const ServerStatus(needsSetup: true, webAuthnAvailable: true,
        nativePasskeys: NativePasskeyStatus(appleConfigured: true))),
    (name: 'SSO only', saved: true, platform: 'ios',
      status: const ServerStatus(needsSetup: false, ssoOnly: true,
        ssoAvailable: true, webAuthnAvailable: true,
        nativePasskeys: NativePasskeyStatus(appleConfigured: true))),
  ]) {
    testWidgets('entry omits the passkey shortcut for ${scenario.name}', (tester) async {
      final auth = _ScreenAuth()..serverStatus = scenario.status;
      await showAuth(tester, auth, saved: scenario.saved ? [_home] : [],
          passkeyPlatform: scenario.platform);
      await tester.pumpAndSettle();
      expect(find.text('Sign in with passkey'), findsNothing);
      expect(find.text('Connect'), findsOneWidget);
    });
  }

  testWidgets('editing the address removes the old server passkey shortcut',
      (tester) async {
    final auth = _ScreenAuth()..serverStatus = _passkeyStatus;
    await showAuth(tester, auth, saved: [_home], passkeyPlatform: 'ios');
    await tester.pumpAndSettle();
    expect(find.text('Sign in with passkey'), findsOneWidget);
    await tester.enterText(find.byType(TextField), _remote.url);
    await tester.pump();
    expect(find.text('Sign in with passkey'), findsNothing);
    expect(auth.passkeys, isEmpty);
  });

  testWidgets('setup guide opens without losing the connection input',
      (tester) async {
    final opened = <Uri>[];
    final auth = _ScreenAuth();
    await showAuth(tester, auth, guideLaunch: (uri) async {
      opened.add(uri);
      return true;
    });
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), _remote.url);
    await tap(tester, 'Set up a Cantinarr server');
    expect(opened, [Uri.parse(cantinarrSetupGuideUrl)]);
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text,
        _remote.url);
    expect(auth.checks, isEmpty);
  });

  for (final throws in [false, true]) {
    testWidgets('setup guide launcher failure offers a copyable address ($throws)',
        (tester) async {
      String? copied;
      tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
          SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.setData') copied = call.arguments['text'] as String;
        return null;
      });
      addTearDown(() => tester.binding.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null));
      await showAuth(tester, _ScreenAuth(), guideLaunch: (_) async {
        if (throws) throw StateError('Browser unavailable');
        return false;
      });
      await tester.pumpAndSettle();
      await tap(tester, 'Set up a Cantinarr server');
      expect(find.text(cantinarrSetupGuideUrl), findsOneWidget);
      await tap(tester, 'Copy address');
      expect(copied, cantinarrSetupGuideUrl);
      expect(find.byType(AlertDialog), findsNothing);
    });
  }

  testWidgets('unavailable history still permits manual sign-in',
      (tester) async {
    final auth = _ScreenAuth();
    await showAuth(tester, auth, preferencesUnavailable: true);
    await tester.pumpAndSettle();
    expect(
        find.textContaining('Saved servers are unavailable'), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, _remote.url);
    await tap(tester, 'Connect');
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
    await tap(tester, 'Saved servers');
    expect(tester.takeException(), isNull);
    await tester
        .ensureVisible(find.byTooltip('Options for ${longServer.name}'));
    expect(tester.takeException(), isNull);
  });

  testWidgets('connection controls remain reachable with a phone keyboard',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    tester.view.viewInsets = const FakeViewPadding(bottom: 300);
    addTearDown(tester.view.reset);
    await showAuth(tester, _ScreenAuth());
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byType(TextField));
    await tester.enterText(find.byType(TextField), _remote.url);
    await tester.ensureVisible(find.text('Connect'));
    expect(find.text('Connect').hitTestable(), findsOneWidget);
    await tester.ensureVisible(find.text('Set up a Cantinarr server'));
    expect(find.text('Set up a Cantinarr server').hitTestable(), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

class _ScreenAuth extends AuthNotifier {
  final checks = <String>[];
  final links = <String>[];
  final passkeys = <String>[];
  ServerStatus? serverStatus;
  String? signInError;
  Completer<void>? pendingSignIn;
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
      status: serverStatus ?? ServerStatus(
          needsSetup: false,
          ssoAvailable: url == _home.url,
          ssoProvider: 'Family')
    );
  }

  @override
  Future<void> connectWithLink(String link) async {
    links.add(link);
    await _finishSignIn();
  }

  @override
  Future<void> loginWithPasskey(String serverUrl) async {
    passkeys.add(serverUrl);
    await _finishSignIn();
  }

  Future<void> _finishSignIn() async {
    state = const AsyncData(AuthState(isLoading: true));
    await pendingSignIn?.future;
    state = signInError != null
        ? AsyncData(AuthState(error: signInError))
        : const AsyncData(AuthState(
            connection: BackendConnection(serverUrl: 'https://remote.test',
                accessToken: 'access', refreshToken: 'refresh'),
            user: UserProfile(id: 1, username: 'viewer', role: 'user')));
  }

  void startExplicitSignIn() {
    state = const AsyncData(AuthState(isLoading: true));
  }
}

const _passkeyStatus = ServerStatus(needsSetup: false, webAuthnAvailable: true,
    nativePasskeys: NativePasskeyStatus(appleConfigured: true));

class _DelayedForget extends SavedServersNotifier {
  final ready = Completer<void>();
  @override
  Future<void> forget(String url) async {
    await ready.future;
    await super.forget(url);
  }
}
