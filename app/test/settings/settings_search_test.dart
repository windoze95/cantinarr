import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Behavior of the settings search bar on the root settings screen: typing
/// replaces the browse sections with gated results, tapping a sub-screen
/// result deep-links with `?highlight=`, root action results run their own
/// handlers, and clearing restores browsing.

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

  testWidgets('typing replaces the browse sections with ranked results',
      (tester) async {
    await _pump(tester, isAdmin: true);

    expect(find.text('SERVER'), findsOneWidget);
    await tester.enterText(_searchField(), 'approval');
    await tester.pumpAndSettle();

    expect(find.text('SERVER'), findsNothing);
    final result = find.text('Require approval for new requests');
    await _huntResult(tester, result);
    expect(result, findsOneWidget);
    expect(find.text('Request Defaults › Approval'), findsOneWidget);
  });

  testWidgets('tapping a sub-screen result deep-links with the anchor',
      (tester) async {
    final pushed = <String>[];
    await _pump(tester, isAdmin: true, pushed: pushed);

    await tester.enterText(_searchField(), 'approval');
    await tester.pumpAndSettle();
    final result = find.text('Require approval for new requests');
    await _huntResult(tester, result);
    await tester.ensureVisible(result);
    await tester.pumpAndSettle();
    await tester.tap(result);
    await tester.pumpAndSettle();

    expect(
      pushed,
      contains(
        '/settings/request-settings?highlight=requests.require-approval',
      ),
    );
  });

  testWidgets('results respect gating and explain empty answers',
      (tester) async {
    await _pump(tester, isAdmin: false);

    await tester.enterText(_searchField(), 'users');
    await tester.pumpAndSettle();
    expect(find.textContaining('No settings match'), findsOneWidget);
    expect(
      find.textContaining('your account can see'),
      findsOneWidget,
    );

    await tester.enterText(_searchField(), 'reports');
    await tester.pumpAndSettle();
    expect(find.text('My reports'), findsOneWidget);
  });

  testWidgets('a root action result runs its handler in place',
      (tester) async {
    await _pump(tester, isAdmin: true);

    await tester.enterText(_searchField(), 'connect link');
    await tester.pumpAndSettle();
    final result = find.text('Generate Connect Link');
    await _huntResult(tester, result);
    await tester.tap(result);
    await tester.pumpAndSettle();

    expect(find.byType(AlertDialog), findsOneWidget);
    expect(
      find.descendant(
        of: find.byType(AlertDialog),
        matching: find.text('Generate Connect Link'),
      ),
      findsOneWidget,
    );
  });

  testWidgets('clearing the query restores the browse list', (tester) async {
    await _pump(tester, isAdmin: true);

    await tester.enterText(_searchField(), 'approval');
    await tester.pumpAndSettle();
    expect(find.text('SERVER'), findsNothing);

    await tester.tap(find.byTooltip('Clear settings search'));
    await tester.pumpAndSettle();
    expect(find.text('SERVER'), findsOneWidget);
  });

  testWidgets('a root row result reveals the row in place', (tester) async {
    await _pump(tester, isAdmin: true);

    await tester.enterText(_searchField(), 'request updates');
    await tester.pumpAndSettle();
    final result = find.text('Request updates');
    await _huntResult(tester, result);
    await tester.tap(result);
    await tester.pumpAndSettle();

    // Search dismissed (breadcrumb subtitles gone), browsing resumed, and
    // SettingsHighlight scrolled the revealed row into view — which puts the
    // top-of-list sections offstage, proving the scroll happened.
    expect(find.text('Settings › Notifications'), findsNothing);
    expect(find.text('Request updates'), findsOneWidget);
    expect(
      tester
          .state<ScrollableState>(find.byType(Scrollable).first)
          .position
          .pixels,
      greaterThan(0),
    );
  });
}

Finder _searchField() => find.byWidgetPredicate(
      (w) => w is TextField && w.decoration?.hintText == 'Search all settings',
    );

Future<void> _huntResult(WidgetTester tester, Finder finder) async {
  final scrollable = find.byType(ListView).first;
  for (var i = 0; i < 40 && finder.evaluate().isEmpty; i++) {
    await tester.drag(scrollable, const Offset(0, -80));
    await tester.pumpAndSettle();
  }
}

Future<void> _pump(
  WidgetTester tester, {
  required bool isAdmin,
  List<String>? pushed,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = _JsonAdapter();
  final router = GoRouter(
    initialLocation: '/settings',
    routes: [
      GoRoute(
        path: '/settings',
        builder: (_, __) => const SettingsScreen(),
      ),
      GoRoute(
        path: '/settings/request-settings',
        builder: (_, state) {
          pushed?.add(state.uri.toString());
          return const Scaffold(body: Text('request settings route'));
        },
      ),
    ],
  );
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendClientProvider.overrideWithValue(dio),
        authProvider.overrideWith(() => _FakeAuthNotifier(isAdmin: isAdmin)),
        aiSettingsProvider.overrideWith((_) async => _aiSettings()),
      ],
      child: MaterialApp.router(theme: AppTheme.dark, routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
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

class _JsonAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final body = switch (options.uri.path) {
      '/api/admin/setup-status' => const {
          'items': <dynamic>[],
          'configured': 0,
          'total': 0,
        },
      '/api/admin/update-status' => const {
          'update': <String, dynamic>{},
          'management_url': '',
        },
      _ => const <String, dynamic>{},
    };
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

AiSettings _aiSettings() => const AiSettings(
      providers: [],
      personal: PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {},
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'openai', model: 'gpt-5.5'),
      ),
      effective: EffectiveAiSettings(
        available: true,
        source: AiAccessSource.shared,
        provider: 'openai',
        model: 'gpt-5.5',
        reason: '',
      ),
    );
