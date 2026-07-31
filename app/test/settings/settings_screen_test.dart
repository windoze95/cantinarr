import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/attention_menu_visibility_switch.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
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
    expect(find.text('Configuration History'), findsNothing);
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

  testWidgets('Configuration History is available from admin settings',
      (tester) async {
    await _pumpSettings(
      tester,
      _settings(source: AiAccessSource.shared),
      isAdmin: true,
    );
    await _dragSettingsUntilFound(tester, find.text('Configuration History'));
    expect(find.text('Configuration History'), findsOneWidget);
    expect(
      find.text('Review AI/MCP profile and custom-format changes'),
      findsOneWidget,
    );
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

  testWidgets('About section links to GitHub but hides Donate in store builds',
      (tester) async {
    await _pumpSettings(tester, _settings(source: AiAccessSource.shared));

    await _dragSettingsUntilFound(tester, find.text('GitHub'));
    expect(find.text('GitHub'), findsOneWidget);

    // The About section is the end of the list; scroll out the remainder so
    // a Donate tile below the fold could not hide from the finder.
    for (var i = 0; i < 3; i++) {
      await tester.drag(find.byType(ListView).first, const Offset(0, -200));
      await tester.pumpAndSettle();
    }
    expect(find.text('Donate'), findsNothing);
  }, variant: TargetPlatformVariant.only(TargetPlatform.iOS));

  testWidgets('Donate appears outside the iOS/Android store binaries',
      (tester) async {
    await _pumpSettings(tester, _settings(source: AiAccessSource.shared));

    await _dragSettingsUntilFound(tester, find.text('Donate'));
    expect(find.text('Donate'), findsOneWidget);
    expect(find.text('Support Cantinarr on GitHub Sponsors'), findsOneWidget);
  }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

  group('Setup Checklist tile', _setupChecklistTileTests);
}

/// The colour of the count in "X of Y features configured". That digit is the
/// only state the Setup Checklist tile carries, so its colour is the contract.
Color? _setupCountColor(WidgetTester tester) {
  final text = tester.widget<Text>(find.textContaining('features configured'));
  final span = text.textSpan! as TextSpan;
  return (span.children!.first as TextSpan).style?.color;
}

Map<String, dynamic> _setupPayload(List<(String, bool)> items) => {
      'items': [
        for (final (key, configured) in items)
          {
            'key': key,
            'title': key,
            'description': 'about $key',
            'configured': configured,
            'optional': key != 'radarr' && key != 'sonarr' && key != 'tmdb',
          },
      ],
      'configured': items.where((i) => i.$2).length,
      'total': items.length,
    };

void _setupChecklistTileTests() {
  testWidgets('reds the count when the server has no library at all',
      (tester) async {
    await _pumpSettings(
      tester,
      _settings(source: AiAccessSource.shared),
      isAdmin: true,
      setupStatus: _setupPayload([
        ('radarr', false),
        ('sonarr', false),
        ('tmdb', true),
      ]),
    );
    await _dragSettingsUntilFound(
        tester, find.textContaining('features configured'));

    expect(find.textContaining('1 of 3 features configured'), findsOneWidget);
    expect(_setupCountColor(tester), AppTheme.danger);
  });

  testWidgets('ambers the count for a working but unfinished server',
      (tester) async {
    // A movies-only server: Sonarr is an unconfigured essential and that is a
    // legitimate deployment, so this must not read as broken.
    await _pumpSettings(
      tester,
      _settings(source: AiAccessSource.shared),
      isAdmin: true,
      setupStatus: _setupPayload([
        ('radarr', true),
        ('sonarr', false),
        ('tmdb', true),
      ]),
    );
    await _dragSettingsUntilFound(
        tester, find.textContaining('features configured'));

    expect(_setupCountColor(tester), AppTheme.warning);
  });

  testWidgets('greens the count once nothing is left', (tester) async {
    await _pumpSettings(
      tester,
      _settings(source: AiAccessSource.shared),
      isAdmin: true,
      setupStatus: _setupPayload([
        ('radarr', true),
        ('sonarr', true),
        ('tmdb', true),
      ]),
    );
    await _dragSettingsUntilFound(
        tester, find.textContaining('features configured'));

    expect(_setupCountColor(tester), AppTheme.available);
  });
}

Future<ProviderContainer> _pumpSettings(
  WidgetTester tester,
  AiSettings settings, {
  bool isAdmin = false,
  Map<String, dynamic>? setupStatus,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _SettingsAdapter(setupStatus: setupStatus);
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
  _SettingsAdapter({Map<String, dynamic>? setupStatus})
      : setupStatus = setupStatus ??
            const {'items': <dynamic>[], 'configured': 0, 'total': 0};

  final Map<String, dynamic> setupStatus;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final body = switch (options.uri.path) {
      '/api/admin/setup-status' => setupStatus,
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
