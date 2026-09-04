import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:cantinarr/features/ai_assistant/ui/ai_access_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/ui/ai_remediation_settings_screen.dart';
import 'package:cantinarr/features/notifications/ui/notification_preferences_screen.dart';
import 'package:cantinarr/features/settings/data/settings_search_index.dart';
import 'package:cantinarr/features/settings/ui/ai_tools_screen.dart';
import 'package:cantinarr/features/settings/ui/credentials_screen.dart';
import 'package:cantinarr/features/settings/ui/discovery_settings_screen.dart';
import 'package:cantinarr/features/settings/ui/request_settings_screen.dart';
import 'package:cantinarr/features/settings/ui/settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Drift guard for the settings-search index: every entry's `title` must be
/// rendered verbatim by the screen the entry claims to live on, under a
/// persona its gate admits. When a screen's copy is reworded, this fails
/// until the registry entry is updated alongside it.
///
/// Coverage model: entries whose id starts with `root.` or `screen.` render
/// on the root settings screen (root rows and the nav tiles that name other
/// screens); every other entry is a control on its own `route`, which must
/// be pumped below. The exhaustiveness test enforces that no entry can be
/// added outside this model unnoticed.

const _admin = UserProfile(id: 1, username: 'admin', role: 'admin');
const _user = UserProfile(
  id: 2,
  username: 'viewer',
  role: 'user',
  permissions: ['ai:chat'],
);

const _adminGates = SettingsSearchGates(
  user: _admin,
  chaptarrEnabled: true,
  lidarrEnabled: true,
  donateVisible: true,
  phoneAppsVisible: true,
  mediaServersVisible: true,
);
const _userGates = SettingsSearchGates(user: _user);

/// The sub-screens pumped by this file. Root-rendered entries are covered by
/// the root pumps instead.
const _pumpedRoutes = {
  '/settings/request-settings',
  '/settings/notifications',
  '/settings/ai-remediation',
  '/settings/credentials',
  '/settings/plex',
  '/settings/discovery',
  '/settings/ai-tools',
  '/settings/ai',
};

bool _isRootRendered(SettingsSearchEntry e) =>
    e.id.startsWith('root.') || e.id.startsWith('screen.');

List<SettingsSearchEntry> _controlsFor(String route) => settingsSearchIndex
    .where((e) => e.route == route && !_isRootRendered(e))
    .toList();

/// Scrolls the first ListView down until [finder] builds. Entries are
/// asserted in registry order (top to bottom), so a one-way hunt suffices.
Future<void> _hunt(WidgetTester tester, Finder finder) async {
  final scrollable = find.byType(ListView).first;
  for (var i = 0; i < 100 && finder.evaluate().isEmpty; i++) {
    await tester.drag(scrollable, const Offset(0, -60));
    await tester.pumpAndSettle();
  }
}

Future<void> _assertTitles(
  WidgetTester tester,
  Iterable<SettingsSearchEntry> entries,
  SettingsSearchGates gates,
) async {
  for (final entry in entries) {
    if (!entry.gate(gates)) continue;
    final finder = find.text(entry.title);
    await _hunt(tester, finder);
    expect(finder, findsWidgets,
        reason: '"${entry.title}" (${entry.id}) is not rendered by '
            '${entry.route} — screen copy and index have drifted');
  }
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier({
    required this.user,
    this.chaptarr = false,
    this.lidarr = false,
    this.instances = const [],
  });

  final UserProfile user;
  final bool chaptarr;
  final bool lidarr;

  /// A jellyfin instance here renders the root's media server guide row
  /// (gated on `mediaServersVisible`), keeping that entry drift-checked.
  final List<ServiceInstance> instances;

  @override
  Future<AuthState> build() async => AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
          services: AvailableServices(chaptarr: chaptarr, lidarr: lidarr),
          instances: instances,
        ),
        user: user,
      );

  @override
  Future<void> refreshUser() async {}

  @override
  Future<void> refreshConfig() async {}
}

/// Serves canned JSON per request path; unknown paths get `{}`.
class _JsonAdapter implements HttpClientAdapter {
  _JsonAdapter(this.byPath);

  final Map<String, Map<String, dynamic>> byPath;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    return ResponseBody.fromString(
      jsonEncode(byPath[options.uri.path] ?? const <String, dynamic>{}),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

Dio _dioFor(Map<String, Map<String, dynamic>> byPath) =>
    Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = _JsonAdapter(byPath);

Future<void> _pumpScreen(
  WidgetTester tester,
  Widget screen, {
  required Dio dio,
  UserProfile? authUser,
  bool chaptarr = false,
  bool lidarr = false,
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendClientProvider.overrideWithValue(dio),
        if (authUser != null)
          authProvider.overrideWith(() => _FakeAuthNotifier(
              user: authUser, chaptarr: chaptarr, lidarr: lidarr)),
      ],
      child: MaterialApp(theme: AppTheme.dark, home: screen),
    ),
  );
  await tester.pumpAndSettle();
}

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

  test('every index entry is drift-covered by a pump in this file', () {
    for (final entry in settingsSearchIndex) {
      expect(_isRootRendered(entry) || _pumpedRoutes.contains(entry.route),
          isTrue,
          reason: '${entry.id} is neither root-rendered nor on a pumped '
              'route — add a pump (or root tile) so its title stays '
              'drift-checked');
    }
  });

  group('root settings screen', () {
    Future<void> pumpRoot(WidgetTester tester, UserProfile user) async {
      final dio = _dioFor(const {
        '/api/admin/setup-status': {
          'items': <dynamic>[],
          'configured': 0,
          'total': 0,
        },
        '/api/admin/update-status': {
          'update': <String, dynamic>{},
          'management_url': '',
        },
      });
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            backendClientProvider.overrideWithValue(dio),
            authProvider.overrideWith(() => _FakeAuthNotifier(
                  user: user,
                  instances: const [
                    ServiceInstance(
                      id: 'jf-a',
                      serviceType: 'jellyfin',
                      name: 'Home Jellyfin',
                    ),
                  ],
                )),
            aiSettingsProvider.overrideWith((_) async => _aiSettings()),
          ],
          child: const MaterialApp(home: SettingsScreen()),
        ),
      );
      await tester.pumpAndSettle();
    }

    testWidgets('renders every admin-visible entry title', (tester) async {
      await pumpRoot(tester, _admin);
      await _assertTitles(
        tester,
        settingsSearchIndex.where(_isRootRendered),
        _adminGates,
      );
    }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

    testWidgets('renders every non-admin entry title', (tester) async {
      await pumpRoot(tester, _user);
      await _assertTitles(
        tester,
        settingsSearchIndex.where(_isRootRendered),
        _userGates,
      );
    });
  });

  testWidgets('request defaults controls', (tester) async {
    await _pumpScreen(
      tester,
      const RequestSettingsScreen(),
      dio: _dioFor(const {
        '/api/admin/request-settings': {
          'settings': <String, dynamic>{},
          'radarr_profiles': <dynamic>[],
          'sonarr_profiles': <dynamic>[],
        },
      }),
    );
    await _assertTitles(
        tester, _controlsFor('/settings/request-settings'), _adminGates);
  });

  testWidgets('notification preference controls', (tester) async {
    await _pumpScreen(
      tester,
      const NotificationPreferencesScreen(),
      dio: _dioFor(const {
        '/api/notifications/preferences': <String, dynamic>{},
      }),
      authUser: _admin,
      chaptarr: true,
      lidarr: true,
    );
    await _assertTitles(
        tester, _controlsFor('/settings/notifications'), _adminGates);
  });

  testWidgets('AI remediation controls', (tester) async {
    await _pumpScreen(
      tester,
      const AiRemediationSettingsScreen(),
      dio: _dioFor(const {
        '/api/admin/credentials': {
          'credentials': <String, bool>{},
          'ai': {
            'config': {'provider': 'openai', 'model': 'gpt-5.5'},
            'providers': [
              {
                'id': 'openai',
                'label': 'OpenAI',
                'auth_type': 'api_key',
                'credential_key': 'openai_key',
                'models': [
                  {'id': 'gpt-5.5', 'label': 'GPT-5.5'},
                ],
              },
            ],
            'health_check': {'enabled': true, 'interval_hours': 24},
          },
        },
        // Everything else (the issues settings endpoint) parses from
        // defaults: RemediationSettings.fromJson tolerates missing keys.
      }),
    );
    await _assertTitles(
        tester, _controlsFor('/settings/ai-remediation'), _adminGates);
  });

  testWidgets('credentials sections', (tester) async {
    await _pumpScreen(
      tester,
      const CredentialsScreen(),
      dio: _dioFor(const {
        '/api/admin/credentials': {
          'credentials': <String, bool>{},
          'ai': {
            'config': {'provider': 'openai', 'model': 'gpt-5.5'},
            'providers': [
              {
                'id': 'openai',
                'label': 'OpenAI',
                'auth_type': 'api_key',
                'credential_key': 'openai_key',
                // Renders the reasoning-effort control so its index title
                // stays drift-checked against the screen.
                'supports_reasoning_effort': true,
                'models': [
                  {'id': 'gpt-5.5', 'label': 'GPT-5.5'},
                ],
              },
            ],
            'health_check': {'enabled': true, 'interval_hours': 24},
          },
        },
      }),
    );
    await _assertTitles(
        tester, _controlsFor('/settings/credentials'), _adminGates);
  });

  testWidgets('discovery controls', (tester) async {
    await _pumpScreen(
      tester,
      const DiscoverySettingsScreen(),
      dio: _dioFor(const {
        '/api/admin/discovery-settings': {
          'source': 'tmdb_trending',
          'english_only': false,
          'sources': <dynamic>[],
          'trakt_configured': false,
        },
      }),
    );
    await _assertTitles(
        tester, _controlsFor('/settings/discovery'), _adminGates);
  });

  testWidgets('AI tools controls', (tester) async {
    await _pumpScreen(
      tester,
      const AiToolsScreen(),
      dio: _dioFor(const {
        '/api/admin/ai-tools': {
          'tools': <dynamic>[],
          'debug': <String, dynamic>{},
        },
      }),
    );
    await _assertTitles(
        tester, _controlsFor('/settings/ai-tools'), _adminGates);
  });

  testWidgets('AI access panels', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          aiSettingsProvider.overrideWith((_) async => _aiSettings()),
          authProvider.overrideWith(() => _FakeAuthNotifier(user: _user)),
        ],
        child: const MaterialApp(home: AiAccessScreen()),
      ),
    );
    await tester.pumpAndSettle();
    await _assertTitles(tester, _controlsFor('/settings/ai'), _userGates);
  });
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
