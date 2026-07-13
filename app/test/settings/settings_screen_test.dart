import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/ai_assistant/data/codex_oauth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    PackageInfo.setMockInitialValues(
      appName: 'Cantinarr',
      packageName: 'com.example.cantinarr',
      version: '1.0.0',
      buildNumber: '1',
      buildSignature: '',
    );
  });

  testWidgets('shows ChatGPT account tile whenever Codex is selected',
      (tester) async {
    await _pumpSettings(
      tester,
      const CodexConnectionStatus(
        selected: true,
        available: false,
        connected: false,
      ),
    );

    expect(find.text('ChatGPT'), findsOneWidget);
    expect(find.text('Sign-in is unavailable on this server'), findsOneWidget);
  });

  testWidgets('hides ChatGPT account tile for a different unlinked provider',
      (tester) async {
    await _pumpSettings(
      tester,
      const CodexConnectionStatus(
        selected: false,
        available: false,
        connected: false,
      ),
    );

    expect(find.text('ChatGPT'), findsNothing);
  });

  testWidgets('keeps ChatGPT account tile after the provider changes',
      (tester) async {
    await _pumpSettings(
      tester,
      const CodexConnectionStatus(
        selected: false,
        available: false,
        connected: true,
        accountEmail: 'viewer@example.com',
      ),
    );

    expect(find.text('ChatGPT'), findsOneWidget);
    expect(find.text('Connected as viewer@example.com'), findsOneWidget);
  });
}

Future<void> _pumpSettings(
  WidgetTester tester,
  CodexConnectionStatus codexStatus,
) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        codexConnectionStatusProvider.overrideWith((_) async => codexStatus),
      ],
      child: const MaterialApp(home: SettingsScreen()),
    ),
  );
  await tester.pumpAndSettle();
}

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(id: 1, username: 'viewer', role: 'user'),
      );

  @override
  Future<void> refreshUser() async {}
}
