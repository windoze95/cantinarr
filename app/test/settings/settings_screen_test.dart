import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/core/widgets/attention_menu_visibility_switch.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/settings_screen.dart';
import 'package:dio/dio.dart';
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
    expect(find.byType(AttentionMenuVisibilitySwitch), findsNothing);
    expect(find.text('NEEDS ATTENTION MENU'), findsNothing);
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

  testWidgets('admin settings can restore every conditionally hidden menu item',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
    });
    final container = await _pumpSettings(
      tester,
      _settings(source: AiAccessSource.shared),
      isAdmin: true,
    );

    await _dragSettingsUntilFound(
      tester,
      find.text('NEEDS ATTENTION MENU'),
    );
    expect(find.text('NEEDS ATTENTION MENU'), findsOneWidget);

    final controls = find.byType(
      AttentionMenuVisibilitySwitch,
      skipOffstage: false,
    );
    expect(controls, findsNWidgets(3));
    expect(
      tester
          .widgetList<AttentionMenuVisibilitySwitch>(controls)
          .map((control) => control.item),
      unorderedEquals(AttentionMenuItem.values),
    );

    final approvalsToggle = find.byKey(
      const ValueKey('approvals-conditional-menu-visibility'),
      skipOffstage: false,
    );
    final issuesToggle = find.byKey(
      const ValueKey('issues-conditional-menu-visibility'),
      skipOffstage: false,
    );
    final agentFixesToggle = find.byKey(
      const ValueKey('agentFixes-conditional-menu-visibility'),
      skipOffstage: false,
    );

    for (final toggle in [
      approvalsToggle,
      issuesToggle,
      agentFixesToggle,
    ]) {
      await tester.ensureVisible(toggle);
      await tester.pumpAndSettle();
      expect(tester.widget<SwitchListTile>(toggle).value, isTrue);
      await tester.tap(toggle);
      await tester.pumpAndSettle();
      expect(tester.widget<SwitchListTile>(toggle).value, isFalse);
    }

    expect(container.read(approvalsMenuOnlyWhenPendingProvider), isFalse);
    expect(container.read(issuesMenuOnlyWhenActiveProvider), isFalse);
    expect(
      container.read(agentFixesMenuOnlyWhenAwaitingReviewProvider),
      isFalse,
    );
  });
}

Future<ProviderContainer> _pumpSettings(
  WidgetTester tester,
  AiSettings settings, {
  bool isAdmin = false,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _SettingsAdapter();
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(isAdmin: isAdmin)),
      aiSettingsProvider.overrideWith((_) async => settings),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  addTearDown(container.dispose);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: SettingsScreen()),
    ),
  );
  await tester.pumpAndSettle();
  return container;
}

Future<void> _dragSettingsUntilFound(
  WidgetTester tester,
  Finder finder,
) async {
  final scrollable = find.byType(ListView).first;
  for (var i = 0; i < 80 && finder.evaluate().isEmpty; i++) {
    await tester.drag(scrollable, const Offset(0, -50));
    await tester.pumpAndSettle();
  }
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier({required this.isAdmin});

  final bool isAdmin;

  @override
  Future<AuthState> build() async => AuthState(
        connection: const BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(
          id: 1,
          username: isAdmin ? 'admin' : 'viewer',
          role: isAdmin ? 'admin' : 'user',
          permissions: const ['ai:chat'],
        ),
      );

  @override
  Future<void> refreshUser() async {}
}

class _SettingsAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final body = switch (options.uri.path) {
      '/api/admin/setup-status' => {
          'items': const [],
          'configured': 0,
          'total': 0,
        },
      '/api/admin/update-status' => {
          'update': const <String, dynamic>{},
          'management_url': '',
        },
      _ => const <String, dynamic>{},
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
