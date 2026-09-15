import 'dart:async';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_access/data/video_apps.dart';
import 'package:cantinarr/features/media_access/data/media_access_service.dart';
import 'package:cantinarr/features/media_access/logic/video_apps_provider.dart';
import 'package:cantinarr/features/media_access/ui/video_app_field.dart';
import 'package:cantinarr/features/settings/ui/video_apps_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _instances = [
  ServiceInstance(id: 'p1', serviceType: 'plex', name: 'Main Plex'),
  ServiceInstance(id: 'p2', serviceType: 'plex', name: 'Other Plex'),
  ServiceInstance(id: 'j1', serviceType: 'jellyfin', name: 'Jellyfin'),
  ServiceInstance(id: 'e1', serviceType: 'emby', name: 'Emby'),
];

class _Auth extends AuthNotifier {
  _Auth(this.role, this.instances);
  final String role;
  final List<ServiceInstance> instances;
  AuthState _state(int id, String server) => AuthState(
    connection: BackendConnection(serverUrl: server, accessToken: 'access',
      refreshToken: 'refresh', instances: instances),
    user: UserProfile(id: id, username: 'reader', role: role),
  );
  @override
  Future<AuthState> build() async => _state(1, 'https://cantinarr.example');
  void switchAccount(int id, String server) => state = AsyncData(_state(id, server));
}

class _Service extends MediaAccessService {
  _Service() : super(backendDio: Dio());
  Map<String, VideoApps> saved = {for (final type in VideoApps.serviceTypes) type: const VideoApps()};
  bool failRead = false, failSave = false;
  Completer<void>? readGate;
  @override
  Future<Map<String, VideoApps>> getVideoAppPreferences() async {
    await readGate?.future;
    if (failRead) throw StateError('unavailable');
    return saved;
  }
  @override
  Future<Map<String, VideoApps>> saveVideoAppPreferences(Map<String, VideoApps> apps) async {
    if (failSave) throw StateError('unavailable');
    return saved = apps;
  }
}

Future<void> _pump(WidgetTester tester, _Service service,
    {String role = 'user', double scale = 1, double width = 800, _Auth? auth}) async {
  tester.view.physicalSize = Size(width, 2200);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(ProviderScope(overrides: [
    authProvider.overrideWith(() => auth ?? _Auth(role, _instances)),
    mediaAccessServiceProvider.overrideWithValue(service),
  ], child: MaterialApp(theme: AppTheme.dark,
    builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
      .copyWith(textScaler: TextScaler.linear(scale)), child: child!),
    home: const VideoAppsScreen())));
  await tester.pumpAndSettle();
}

Future<void> _choose(WidgetTester tester, String service, String choice) async {
  final field = find.descendant(
    of: find.byWidgetPredicate((w) => w is VideoAppField && w.serviceType == service),
    matching: find.byType(DropdownButtonFormField<String>));
  await tester.ensureVisible(field);
  await tester.tap(field);
  await tester.pumpAndSettle();
  await tester.tap(find.text(choice).last);
  await tester.pumpAndSettle();
}

void main() {
  for (final role in ['admin', 'user']) {
    testWidgets('$role saves separate service overrides and can inherit again', (tester) async {
      final service = _Service();
      await _pump(tester, service, role: role);
      expect(find.byType(VideoAppField), findsNWidgets(3));
      await _choose(tester, 'plex', 'Infuse');
      await _choose(tester, 'jellyfin', 'Browser');
      await _choose(tester, 'emby', 'Emby');
      expect(service.saved.map((k, v) => MapEntry(k, v.ios)),
        {'plex': 'infuse', 'jellyfin': 'browser', 'emby': 'service'});
      await _choose(tester, 'plex', 'Use admin default');
      expect(service.saved['plex']!.ios, '');
      expect(service.saved['jellyfin']!.ios, 'browser');
      final container = ProviderScope.containerOf(tester.element(find.byType(VideoAppsScreen)));
      container.invalidate(videoAppPreferencesProvider);
      await tester.pumpAndSettle();
      expect(find.text('Browser'), findsOneWidget);
      expect(find.text('Use admin default'), findsOneWidget);
      expect(container.read(videoAppRevisionProvider), 4);
    });
  }

  testWidgets('switching accounts or Cantinarr servers clears previous choices', (tester) async {
    final auth = _Auth('user', _instances);
    final service = _Service()..saved['plex'] = const VideoApps(ios: 'infuse');
    await _pump(tester, service, auth: auth);
    expect(find.text('Infuse'), findsOneWidget);
    service.saved = {for (final type in VideoApps.serviceTypes) type: const VideoApps(ios: 'browser')};
    auth.switchAccount(2, 'https://cantinarr.example');
    await tester.pumpAndSettle();
    expect(find.text('Infuse'), findsNothing);
    expect(find.text('Browser'), findsNWidgets(3));
    service.saved = {for (final type in VideoApps.serviceTypes) type: const VideoApps()};
    auth.switchAccount(2, 'https://other.example');
    await tester.pumpAndSettle();
    expect(find.text('Browser'), findsNothing);
    expect(find.text('Use admin default'), findsNWidgets(3));
  });

  testWidgets('a failed save restores the previous choices', (tester) async {
    final service = _Service()..failSave = true;
    await _pump(tester, service);
    await _choose(tester, 'plex', 'Infuse');
    expect(service.saved['plex']!.ios, '');
    expect(find.text('Use admin default'), findsNWidgets(3));
    expect(find.text("Couldn't save your video apps. Try again."), findsOneWidget);
  });

  testWidgets('a slow refresh cannot overwrite the previous service choice', (tester) async {
    final service = _Service();
    await _pump(tester, service);
    service.readGate = Completer<void>();
    final plex = find.byWidgetPredicate((w) => w is VideoAppField && w.serviceType == 'plex');
    await tester.tap(find.descendant(of: plex,
      matching: find.byType(DropdownButtonFormField<String>)));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Infuse').last);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));
    expect(service.saved['plex']!.ios, 'infuse');
    for (final field in tester.widgetList<VideoAppField>(find.byType(VideoAppField))) {
      expect(field.onChanged, isNull);
    }
    expect(find.byType(VideoAppField), findsNWidgets(3));
    service.readGate!.complete();
    await tester.pumpAndSettle();
    await _choose(tester, 'jellyfin', 'Browser');
    expect(service.saved['plex']!.ios, 'infuse');
    expect(service.saved['jellyfin']!.ios, 'browser');
  });

  testWidgets('a failed read can retry without guessing saved choices', (tester) async {
    final service = _Service()..failRead = true;
    await _pump(tester, service);
    expect(find.byType(VideoAppField), findsNothing);
    service.failRead = false;
    await tester.tap(find.text("Couldn't load video apps · Retry"));
    await tester.pumpAndSettle();
    expect(find.byType(VideoAppField), findsNWidgets(3));
  });

  testWidgets('resume refreshes choices changed on another device', (tester) async {
    final service = _Service();
    await _pump(tester, service);
    service.saved = {...service.saved, 'plex': const VideoApps(ios: 'infuse')};
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(find.text('Infuse'), findsOneWidget);
  });

  testWidgets('only configured video service types have preferences', (tester) async {
    final auth = _Auth('user', [_instances.first]);
    await _pump(tester, _Service(), auth: auth);
    expect(find.byType(VideoAppField), findsOneWidget);
    expect(find.text('Jellyfin'), findsNothing);
  });

  testWidgets('video preferences fit a narrow screen with large text', (tester) async {
    await _pump(tester, _Service(), width: 320, scale: 2);
    for (final type in VideoApps.serviceTypes) {
      await _choose(tester, type, 'Infuse');
    }
    await tester.ensureVisible(find.text('Get Infuse'));
    expect(tester.takeException(), isNull);
  });
}
