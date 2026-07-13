import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
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

  testWidgets('shows the effective included provider', (tester) async {
    await _pumpSettings(tester, _settings(source: AiAccessSource.shared));

    expect(find.text('AI Access'), findsOneWidget);
    expect(find.text('Included · OpenAI'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.text('AI Assistant'),
      200,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.text('Available'), findsOneWidget);
  });

  testWidgets('marks a broken personal override instead of showing included',
      (tester) async {
    await _pumpSettings(
      tester,
      _settings(source: AiAccessSource.personal, available: false),
    );

    expect(find.text('Personal AI needs attention'), findsOneWidget);
    expect(find.text('Included · OpenAI'), findsNothing);
  });

  testWidgets('AI Access remains visible when no source is configured',
      (tester) async {
    await _pumpSettings(tester, _settings(source: AiAccessSource.none));

    expect(find.text('AI Access'), findsOneWidget);
    expect(find.text('Add a personal provider'), findsOneWidget);
  });
}

Future<void> _pumpSettings(
  WidgetTester tester,
  AiSettings settings,
) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        aiSettingsProvider.overrideWith((_) async => settings),
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
        user: UserProfile(
          id: 1,
          username: 'viewer',
          role: 'user',
          permissions: ['ai:chat'],
        ),
      );

  @override
  Future<void> refreshUser() async {}
}

AiSettings _settings({
  required AiAccessSource source,
  bool available = true,
}) =>
    AiSettings(
      providers: const [],
      personal: PersonalAiSettings(
        selected: source == AiAccessSource.personal,
        config: source == AiAccessSource.personal
            ? const AiProviderConfig(provider: 'openai', model: 'gpt-5.4-mini')
            : null,
        credentials: const {},
      ),
      shared: SharedAiSettings(
        granted: source == AiAccessSource.shared,
        configured: source == AiAccessSource.shared,
        config: const AiProviderConfig(
          provider: 'openai',
          model: 'gpt-5.4-mini',
        ),
      ),
      effective: EffectiveAiSettings(
        available: source == AiAccessSource.none ? false : available,
        source: source,
        provider: source == AiAccessSource.none ? '' : 'openai',
        model: source == AiAccessSource.none ? '' : 'gpt-5.4-mini',
        reason: available ? '' : 'personal_credential_missing',
      ),
    );
