import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/user_request_settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// The per-user admin screen: the default-instance dropdowns keep their pin
/// semantics, and the new grants checkboxes (shown only where siblings exist)
/// save additive library access without touching the default.
void main() {
  late _FakeAdapter adapter;

  Future<void> pumpScreen(WidgetTester tester) async {
    tester.view.physicalSize = const Size(390, 1600);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = adapter;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_adminState())),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: const MaterialApp(
          home: UserRequestSettingsScreen(userId: 7, username: 'alice'),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets(
      'grants checkboxes appear beside the default and save additively',
      (tester) async {
    adapter = _FakeAdapter(grants: {
      'radarr': ['radarr-4k'],
    });
    await pumpScreen(tester);

    // The screen loaded past its error state and lists the instance section.
    expect(find.text('Retry'), findsNothing);
    expect(find.text('Require approval'), findsOneWidget);

    // The section sits below the fold of the settings ListView.
    await tester.scrollUntilVisible(
      find.text('Also grant Radarr libraries'),
      200,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pumpAndSettle();

    // Radarr has a sibling, so its grants section renders (checked from the
    // stored grant); single-instance Sonarr gets no grants section.
    expect(find.text('Also grant Radarr libraries'), findsOneWidget);
    expect(find.text('Also grant Sonarr libraries'), findsNothing);
    final fourK = tester.widget<CheckboxListTile>(
        find.widgetWithText(CheckboxListTile, '4K Movies'));
    expect(fourK.value, isTrue);

    // Grant the main library too, then save.
    await tester.tap(find.widgetWithText(CheckboxListTile, 'Movies'));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.widgetWithText(ElevatedButton, 'Save'),
      200,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save'));
    await tester.pumpAndSettle();

    final putGrants = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/instance-grants');
    final body = putGrants.body as Map<String, dynamic>;
    expect(List.of(body['radarr'] as List)..sort(),
        ['radarr-4k', 'radarr-main']);
    // Every visible type is named so an emptied set clears server-side.
    expect(body.containsKey('sonarr'), isTrue);
    // The default pin was not touched by granting.
    final putDefaults = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/default-instances');
    expect((putDefaults.body as Map<String, dynamic>)['radarr'], isNull);
  });
}

AuthState _adminState() => const AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(),
        instances: [
          ServiceInstance(
            id: 'radarr-main',
            serviceType: 'radarr',
            name: 'Movies',
            isDefault: true,
          ),
          ServiceInstance(
            id: 'radarr-4k',
            serviceType: 'radarr',
            name: '4K Movies',
          ),
          ServiceInstance(
            id: 'sonarr-main',
            serviceType: 'sonarr',
            name: 'TV',
            isDefault: true,
          ),
        ],
      ),
      user: UserProfile(id: 1, username: 'admin', role: 'admin'),
    );

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;
  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

class _FakeAdapter implements HttpClientAdapter {
  final Map<String, List<String>> grants;
  final List<({String method, String path, dynamic body})> requests = [];

  _FakeAdapter({this.grants = const {}});

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    requests.add((method: options.method, path: path, body: body));

    Object response = <String, dynamic>{};
    if (path.endsWith('/request-settings') && options.method == 'GET') {
      response = path.contains('/users/')
          ? <String, dynamic>{}
          : {
              'settings': {
                'require_approval': false,
                'allow_season_choice': true,
                'default_season_scope': 'all',
                'allow_quality_choice': false,
              },
              'radarr_profiles': <dynamic>[],
              'sonarr_profiles': <dynamic>[],
            };
    } else if (path.endsWith('/default-instances')) {
      response = <String, dynamic>{};
    } else if (path.endsWith('/instance-grants')) {
      response = grants;
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
