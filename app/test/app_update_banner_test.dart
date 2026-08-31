import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/app.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/core/utils/version_compat.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The app-wide banner slot, exercised end to end through the real provider +
/// service chain against a canned /api/admin/update-status payload: the
/// warn-only version-skew notices it does carry, and the release-news bar it
/// deliberately does not.
void main() {
  testWidgets('never nags about a newer release, even for an admin',
      (tester) async {
    await _pumpApp(tester, role: 'admin', adapter: _UpdateStatusAdapter());

    expect(find.textContaining('is available'), findsNothing,
        reason: 'the app-wide release-news bar is off');
    expect(find.byIcon(Icons.close), findsNothing,
        reason: 'no banner means no dismiss button in the slot');
  });

  testWidgets('never fetches the admin-only status for a non-admin',
      (tester) async {
    final adapter = _UpdateStatusAdapter();
    await _pumpApp(tester, role: 'user', adapter: adapter);

    expect(adapter.updateStatusCalls, 0,
        reason: 'the admin-only endpoint must not be called for a requester');
  });

  const appTooOldMessage =
      'This app is older than your server supports — update it from the app store';
  const serverTooOldMessage =
      'Server 0.2.0 is older than this app supports — update the server';

  testWidgets('warns everyone when the app is below the server floor',
      (tester) async {
    _mockAppVersion('0.1.0');
    await _pumpApp(
      tester,
      role: 'user',
      adapter: _UpdateStatusAdapter(),
      serverMinAppVersion: '9.9.9',
    );
    expect(find.text(appTooOldMessage), findsOneWidget);

    await tester.tap(find.byIcon(Icons.close));
    await tester.pumpAndSettle();

    expect(find.text(appTooOldMessage), findsNothing);
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('dismissed_app_skew_pair'), '0.1.0|9.9.9',
        reason: 'either side changing must bring the warning back');
  });

  testWidgets('a dismissed app-skew pair silences only that exact pair',
      (tester) async {
    _mockAppVersion('0.1.0');
    await _pumpApp(
      tester,
      role: 'user',
      adapter: _UpdateStatusAdapter(),
      serverMinAppVersion: '9.9.9',
      prefs: {'dismissed_app_skew_pair': '0.1.0|9.9.8'},
    );
    expect(find.text(appTooOldMessage), findsOneWidget,
        reason: 'the server floor moved, so the warning resurfaces');
  });

  testWidgets('warns an admin when the server is below the app floor',
      (tester) async {
    await _pumpApp(
      tester,
      role: 'admin',
      adapter: _UpdateStatusAdapter(),
      serverVersion: '0.2.0',
      minServerFloor: '9.9.9',
    );
    expect(find.text(serverTooOldMessage), findsOneWidget);
    expect(find.text('How to update'), findsOneWidget,
        reason: 'no portal configured, so the action is the update guide');
  });

  testWidgets('points the server-too-old action at the portal when set',
      (tester) async {
    await _pumpApp(
      tester,
      role: 'admin',
      adapter: _UpdateStatusAdapter(managementUrl: 'http://tower.local/docker'),
      serverVersion: '0.2.0',
      minServerFloor: '9.9.9',
    );
    expect(find.text('Update'), findsOneWidget);
    expect(find.text('How to update'), findsNothing);
  });

  testWidgets('a requester never sees the server-too-old warning',
      (tester) async {
    await _pumpApp(
      tester,
      role: 'user',
      adapter: _UpdateStatusAdapter(),
      serverVersion: '0.2.0',
      minServerFloor: '9.9.9',
    );
    expect(find.text(serverTooOldMessage), findsNothing,
        reason: 'a requester cannot update the server');
  });

  testWidgets('app-too-old outranks server-too-old', (tester) async {
    _mockAppVersion('0.1.0');
    await _pumpApp(
      tester,
      role: 'admin',
      adapter: _UpdateStatusAdapter(),
      serverVersion: '0.2.0',
      serverMinAppVersion: '9.9.9',
      minServerFloor: '9.9.9',
    );
    expect(find.text(appTooOldMessage), findsOneWidget);
    expect(find.text(serverTooOldMessage), findsNothing);
  });
}

void _mockAppVersion(String version) {
  PackageInfo.setMockInitialValues(
    appName: 'Cantinarr',
    packageName: 'com.example.cantinarr',
    version: version,
    buildNumber: '1',
    buildSignature: '',
  );
}

const _server = 'https://media.example.com';

AuthState _stateWithRole(
  String role, {
  String? serverVersion,
  String? minAppVersion,
}) =>
    AuthState(
      connection: BackendConnection(
        serverUrl: _server,
        accessToken: 'access',
        refreshToken: 'refresh',
        serverVersion: serverVersion,
        minAppVersion: minAppVersion,
        services: const AvailableServices(),
      ),
      user: UserProfile(id: 1, username: 'tester', role: role),
    );

/// Pumps [CantinarrApp] with the platform boundaries faked, routing HTTP into
/// [adapter] so the update-status chain runs for real. The optional version
/// knobs feed the skew-warning inputs: what the connection reports
/// ([serverVersion], [serverMinAppVersion]) and this build's own floor
/// ([minServerFloor]).
Future<ProviderContainer> _pumpApp(
  WidgetTester tester, {
  required String role,
  required _UpdateStatusAdapter adapter,
  Map<String, Object> prefs = const {},
  String? serverVersion,
  String? serverMinAppVersion,
  String? minServerFloor,
}) async {
  SharedPreferences.setMockInitialValues(prefs);
  final links = _FakeDeepLinks();
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _FakeAuthNotifier(_stateWithRole(
          role,
          serverVersion: serverVersion,
          minAppVersion: serverMinAppVersion,
        ))),
    deepLinkSourceProvider.overrideWithValue(links),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    backendClientProvider.overrideWithValue(
      Dio(BaseOptions(baseUrl: _server))..httpClientAdapter = adapter,
    ),
    if (minServerFloor != null)
      minServerVersionProvider.overrideWithValue(minServerFloor),
  ]);
  addTearDown(container.dispose);
  addTearDown(links.controller.close);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const CantinarrApp(),
    ),
  );
  await tester.pumpAndSettle();
  return container;
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

class _FakeDeepLinks implements DeepLinkSource {
  final StreamController<Uri> controller = StreamController<Uri>.broadcast();

  @override
  Future<Uri?> getInitialLink() async => null;

  @override
  Stream<Uri> get uriLinkStream => controller.stream;
}

/// Canned backend: /api/admin/update-status always reports a newer release
/// (so "never nags" above is load-bearing) with the portal from the
/// constructor knob, and counts its calls; everything else gets the empty
/// paged payload the landing screens expect.
class _UpdateStatusAdapter implements HttpClientAdapter {
  _UpdateStatusAdapter({this.managementUrl = ''});

  final String managementUrl;
  int updateStatusCalls = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body;
    if (options.path == '/api/admin/update-status') {
      updateStatusCalls++;
      body = {
        'update': {
          'current': '1.2.3',
          'latest': '1.3.0',
          'available': true,
          'url': 'https://github.com/windoze95/cantinarr/releases/tag/v1.3.0',
        },
        'management_url': managementUrl,
      };
    } else if (options.path == '/api/trakt/anticipated') {
      body = <dynamic>[];
    } else {
      body = {
        'page': 1,
        'results': <dynamic>[],
        'total_pages': 0,
        'total_results': 0,
      };
    }
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
