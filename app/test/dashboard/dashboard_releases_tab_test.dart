import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_releases_tab.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('prompts to connect a service when no instances are configured',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_noInstancesState)),
        ],
        child: const MaterialApp(
          home: Scaffold(body: DashboardReleasesTab()),
        ),
      ),
    );
    // Lets the post-frame load run and resolve to the empty-instances state.
    await tester.pumpAndSettle();

    expect(find.text('Nothing to schedule yet'), findsOneWidget);
  });

  testWidgets('a music-only grant loads the Lidarr calendar into the timeline',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final adapter = _CalendarAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_musicOnlyState)),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: const MaterialApp(
          home: Scaffold(body: DashboardReleasesTab()),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(adapter.calendarPaths,
        ['/api/instances/music-1/api/v1/calendar'],
        reason: 'the granted Lidarr instance is the one read');
    expect(find.text('Fear Inoculum'), findsOneWidget);
    expect(find.text('Tool'), findsOneWidget);
  });
}

/// Serves one album on the Lidarr calendar; anything else gets `{}`.
class _CalendarAdapter implements HttpClientAdapter {
  final calendarPaths = <String>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    Object body = const <String, dynamic>{};
    if (options.path.endsWith('/calendar')) {
      calendarPaths.add(options.path);
      body = [
        {
          'id': 9,
          'title': 'Fear Inoculum',
          'artistId': 4,
          'foreignAlbumId': '1f4a9e6b',
          'releaseDate':
              DateTime.now().add(const Duration(days: 3)).toIso8601String(),
          'artist': {'id': 4, 'artistName': 'Tool'},
          'statistics': {'trackFileCount': 0, 'trackCount': 10},
        }
      ];
    }
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

const _musicOnlyState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(lidarr: true),
    instances: [
      ServiceInstance(
        id: 'music-1',
        serviceType: 'lidarr',
        name: 'Music',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

const _noInstancesState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}
