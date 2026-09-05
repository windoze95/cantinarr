import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/ui/auth_screen.dart';
import 'package:cantinarr/features/settings/ui/oidc_settings_screen.dart';
import 'package:cantinarr/features/settings/ui/oidc_account_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const connection = BackendConnection(
    serverUrl: 'https://media.example.com',
    accessToken: 'access',
    refreshToken: 'refresh');

class ScreenAuth extends AuthNotifier {
  final bool signedIn;
  final bool sso;
  int starts = 0;
  ScreenAuth({this.signedIn = false, this.sso = true});
  @override
  Future<AuthState> build() async => signedIn
      ? const AuthState(
          connection: connection,
          user: UserProfile(id: 1, username: 'admin', role: 'admin'))
      : const AuthState();
  @override
  Future<({String serverUrl, ServerStatus status})> checkServer(
          String url) async =>
      (
        serverUrl: url,
        status: ServerStatus(
            needsSetup: false,
            ssoAvailable: sso,
            ssoOnly: sso,
            ssoProvider: 'Family')
      );
  @override
  Future<void> startSSO(String server,
      {String purpose = 'login',
      String? invitation,
      String? externalOrigin}) async {
    starts++;
  }
}

class ScreenService extends AuthService {
  Map<String, dynamic>? saved;
  @override
  Future<ServerStatus> getServerStatus(String server) async =>
      const ServerStatus(
          needsSetup: false, ssoAvailable: true, ssoProvider: 'Family');
  @override
  Future<Map<String, dynamic>> oidcRequest(String server, String path,
      {String method = 'GET',
      String? accessToken,
      Map<String, dynamic>? data}) async {
    if (path.endsWith('identities')) {
      return {
        'identities': [
          {'issuer': 'https://idp.example.com', 'subject': 'user-123'}
        ]
      };
    }
    if (method == 'PUT') saved = data;
    return {
      'enabled': true,
      'label': 'Family',
      'issuer': 'https://idp.example.com',
      'client_id': 'client',
      'group_claim': 'groups',
      'additional_scopes': <String>[],
      'allowed_groups': <String>[],
      'has_secret': true,
      'tested': false,
      'callback_url': 'https://media.example.com/api/auth/oidc/callback'
    };
  }
}

void main() {
  testWidgets('login offers provider and retains administrator recovery',
      (tester) async {
    final auth = ScreenAuth();
    await tester.pumpWidget(ProviderScope(
        overrides: [authProvider.overrideWith(() => auth)],
        child: const MaterialApp(home: AuthScreen())));
    await tester.pumpAndSettle();
    await tester.enterText(
        find.byType(TextField).first, 'https://media.example.com');
    await tester.tap(find.text('Continue'));
    await tester.pumpAndSettle();
    expect(find.text('Continue with Family'), findsOneWidget);
    expect(find.textContaining('Administrators can use local sign-in'),
        findsOneWidget);
    expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
    await tester.tap(find.text('Continue with Family'));
    await tester.pumpAndSettle();
    expect(auth.starts, 1);
  });
  testWidgets('older servers keep local login without an SSO button',
      (tester) async {
    await tester.pumpWidget(ProviderScope(
        overrides: [authProvider.overrideWith(() => ScreenAuth(sso: false))],
        child: const MaterialApp(home: AuthScreen())));
    await tester.pumpAndSettle();
    await tester.enterText(
        find.byType(TextField).first, 'https://media.example.com');
    await tester.tap(find.text('Continue'));
    await tester.pumpAndSettle();
    expect(find.text('Continue with Family'), findsNothing);
    expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
  });
  testWidgets(
      'configuration never displays a stored secret and saves explicit controls',
      (tester) async {
    final service = ScreenService();
    await tester.pumpWidget(ProviderScope(overrides: [
      authProvider.overrideWith(() => ScreenAuth(signedIn: true)),
      authServiceProvider.overrideWithValue(service)
    ], child: const MaterialApp(home: OIDCSettingsScreen())));
    await tester.pumpAndSettle();
    expect(find.text('Single sign-on'), findsOneWidget);
    final secret = find.widgetWithText(TextField, 'Client secret');
    await tester.scrollUntilVisible(secret, 150,
        scrollable: find.byType(Scrollable).first);
    expect(tester.widget<TextField>(secret).controller!.text, isEmpty);
    expect(tester.widget<TextField>(secret).obscureText, isTrue);
    final save = find.text('Save settings');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();
    expect(service.saved!['client_secret'], isNull);
    expect(service.saved!['group_claim'], 'groups');
    expect(find.text('Single sign-on settings saved.'), findsOneWidget);
  });
  testWidgets('account screen displays the exact linked identity',
      (tester) async {
    await tester.pumpWidget(ProviderScope(overrides: [
      authProvider.overrideWith(() => ScreenAuth(signedIn: true)),
      authServiceProvider.overrideWithValue(ScreenService())
    ], child: const MaterialApp(home: OIDCAccountScreen())));
    await tester.pumpAndSettle();
    expect(find.text('Linked'), findsOneWidget);
    expect(find.text('Identity: user-123'), findsOneWidget);
    expect(find.text('Link with Family'), findsOneWidget);
  });
}
