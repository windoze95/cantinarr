import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/notifications/notification_categories.dart';
import 'package:cantinarr/features/notifications/ui/push_notifications_screen.dart';
import 'package:cantinarr/features/notifications/ui/server_push_notifications_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

class _Auth extends AuthNotifier {
  _Auth(this.admin);
  final bool admin;
  @override
  Future<AuthState> build() async => AuthState(
        user: UserProfile(
            id: 1, username: 'viewer', role: admin ? 'admin' : 'user'),
        connection: const BackendConnection(
            serverUrl: 'https://cantinarr.test',
            accessToken: 'test',
            refreshToken: 'test',
            services: AvailableServices(chaptarr: true, lidarr: true)),
      );
}

class _Adapter implements HttpClientAdapter {
  bool serverEnabled = true;
  bool legacy = false;
  bool failSave = false;
  Completer<void>? holdSave;
  int saves = 0;
  final allowed = {for (final c in pushCategories) c.key: true};
  final prefs = <String, dynamic>{
    'push_enabled': true,
    for (final c in pushCategories)
      c.key: !{'request_auto_approved', 'request_decision', 'content_upgraded'}
          .contains(c.key),
  };
  Map<String, dynamic> get policy =>
      {'enabled': serverEnabled, 'categories': allowed};

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    final server = options.path == '/api/admin/push-notifications';
    if (options.method == 'PUT') {
      saves++;
      if (holdSave != null) await holdSave!.future;
      if (failSave) {
        return ResponseBody.fromString('{"error":"Could not save"}', 500);
      }
      final body = options.data as Map<String, dynamic>;
      expect(body.containsKey('server_policy'), isFalse);
      if (server) {
        serverEnabled = body['enabled'] == true;
        allowed.addAll(Map<String, bool>.from(body['categories'] as Map));
      } else {
        prefs.addAll(body);
      }
    }
    final result = server
        ? policy
        : {
            ...prefs,
            if (!legacy) 'server_policy': policy,
          };
    if (legacy && !server) {
      result.remove('push_enabled');
      result.remove('request_auto_approved');
    }
    return ResponseBody.fromString(jsonEncode(result), 200, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType],
    });
  }

  @override
  void close({bool force = false}) {}
}

Future<void> _pump(WidgetTester tester, _Adapter adapter,
    {bool admin = true}) async {
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.test'))
    ..httpClientAdapter = adapter;
  final router =
      GoRouter(initialLocation: '/settings/push-notifications', routes: [
    GoRoute(
        path: '/settings/push-notifications',
        builder: (_, __) => const PushNotificationsScreen(highlightId: 'test')),
    GoRoute(
        path: '/settings/push-notifications/server',
        builder: (_, __) =>
            const ServerPushNotificationsScreen(highlightId: 'test')),
  ]);
  addTearDown(router.dispose);
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _Auth(admin)),
    backendClientProvider.overrideWithValue(dio),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  await tester.pumpWidget(UncontrolledProviderScope(
      container: container, child: MaterialApp.router(routerConfig: router)));
  await tester.pumpAndSettle();
}

Finder _tile(String title) =>
    find.ancestor(of: find.text(title), matching: find.byType(SwitchListTile));
Future<void> _reveal(WidgetTester tester, String title) async {
  final finder = find.text(title);
  if (finder.evaluate().isEmpty) {
    tester
        .state<ScrollableState>(find.byType(Scrollable).first)
        .position
        .jumpTo(0);
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(finder, 180,
        scrollable: find.byType(Scrollable).first);
  }
  await tester.ensureVisible(finder);
  await tester.pumpAndSettle();
}

Future<SwitchListTile> _switch(WidgetTester tester, String title) async {
  await _reveal(tester, title);
  return tester.widget<SwitchListTile>(_tile(title));
}

Future<void> _tap(WidgetTester tester, String title) async {
  await _reveal(tester, title);
  await tester.tap(find.text(title));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('media server access has one personal choice and one server gate',
      (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect((await _switch(tester, 'Media server access')).value, isTrue);
    await _tap(tester, 'Media server access');
    expect(adapter.prefs['media_server_access'], isFalse);
    expect(adapter.prefs['plex_invite_sent'], isFalse);
    expect(adapter.allowed['media_server_access'], isTrue);
    await _tap(tester, 'Server settings');
    await _tap(tester, 'Media server access');
    expect(adapter.allowed['media_server_access'], isFalse);
    expect(adapter.prefs['media_server_access'], isFalse);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
      'personal master preserves categories and automatic alerts require opting in',
      (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect((await _switch(tester, 'Automatically approved requests')).value,
        isFalse);
    await _tap(tester, 'Receive push notifications');
    expect(adapter.prefs['push_enabled'], isFalse);
    expect(adapter.prefs['new_movie'], isTrue);
    expect((await _switch(tester, 'New movie available')).onChanged, isNull);
    await _tap(tester, 'Receive push notifications');
    expect((await _switch(tester, 'New movie available')).value, isTrue);
    await _tap(tester, 'Automatically approved requests');
    expect(adapter.prefs['request_auto_approved'], isTrue);
    expect(adapter.serverEnabled, isTrue);
  });

  testWidgets(
      'regular users can opt out while the server is off and cannot manage it',
      (tester) async {
    final adapter = _Adapter()..serverEnabled = false;
    await _pump(tester, adapter, admin: false);
    expect(find.text('Server settings'), findsNothing);
    expect(find.text('Automatically approved requests'), findsNothing);
    expect((await _switch(tester, 'New movie available')).onChanged, isNull);
    await _tap(tester, 'Receive push notifications');
    expect(adapter.prefs['push_enabled'], isFalse);
    expect(adapter.serverEnabled, isFalse);
  });

  testWidgets('server settings have independent master and category controls',
      (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    await _tap(tester, 'Server settings');
    expect(find.text('Server Push Notifications'), findsOneWidget);
    await _tap(tester, 'New movie available');
    expect(adapter.allowed['new_movie'], isFalse);
    expect(adapter.prefs['new_movie'], isTrue);
    await _tap(tester, 'Allow push notifications');
    expect(adapter.serverEnabled, isFalse);
    expect((await _switch(tester, 'New episodes available')).onChanged, isNull);
    await _tap(tester, 'Allow push notifications');
    await tester.tap(find.byTooltip('Back'));
    await tester.pumpAndSettle();
    expect((await _switch(tester, 'New movie available')).onChanged, isNull);
    expect(
        (await _switch(tester, 'New episodes available')).onChanged, isNotNull);
  });

  testWidgets('failed and overlapping saves cannot overwrite saved choices',
      (tester) async {
    final adapter = _Adapter()
      ..holdSave = Completer<void>()
      ..failSave = true;
    await _pump(tester, adapter);
    await tester.ensureVisible(find.text('Receive push notifications'));
    await tester.tap(find.text('Receive push notifications'));
    await tester.pump();
    expect(
        tester
            .widget<SwitchListTile>(_tile('Receive push notifications'))
            .onChanged,
        isNull);
    for (var i = 0; i < 10 && adapter.saves == 0; i++) {
      await tester.pump(const Duration(milliseconds: 10));
    }
    expect(adapter.saves, 1);
    adapter.holdSave!.complete();
    await tester.pumpAndSettle();
    expect((await _switch(tester, 'Receive push notifications')).value, isTrue);
    expect(adapter.prefs['push_enabled'], isTrue);
    expect((await _switch(tester, 'New movie available')).onChanged, isNotNull);
  });

  testWidgets(
      'older servers keep existing choices and explain unavailable controls',
      (tester) async {
    final adapter = _Adapter()..legacy = true;
    await _pump(tester, adapter);
    expect((await _switch(tester, 'Receive push notifications')).onChanged,
        isNull);
    expect((await _switch(tester, 'Automatically approved requests')).onChanged,
        isNull);
    await _reveal(tester, 'Receive push notifications');
    expect(find.textContaining('Update your server to use the master switch'),
        findsOneWidget);
    expect((await _switch(tester, 'New movie available')).onChanged, isNotNull);
  });

  testWidgets('notification pages fit a narrow phone with large text',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 1.5;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    await _pump(tester, _Adapter());
    expect(tester.takeException(), isNull);
    await _tap(tester, 'Server settings');
    expect(tester.takeException(), isNull);
  });
}
