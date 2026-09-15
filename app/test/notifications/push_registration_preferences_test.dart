import 'dart:async';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/notifications/notification_prefs.dart';
import 'package:cantinarr/features/notifications/notification_prefs_service.dart';
import 'package:cantinarr/features/notifications/push_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Auth extends AuthNotifier {
  AuthState account(int id, String server) => AuthState(
        user: UserProfile(id: id, username: 'viewer', role: 'user'),
        connection: BackendConnection(
            serverUrl: server,
            accessToken: 'test',
            refreshToken: 'test',
            services: const AvailableServices()),
      );
  @override
  Future<AuthState> build() async => account(1, 'https://first.test');
  void change(int id, String server) => state = AsyncData(account(id, server));
}

class _Prefs extends NotificationPrefsService {
  _Prefs(this.prefs) : super(backendDio: Dio());
  final NotificationPrefs prefs;
  final started = Completer<void>();
  Completer<void>? gate;
  @override
  Future<NotificationPrefs> getPreferences() async {
    if (!started.isCompleted) started.complete();
    if (gate != null) await gate!.future;
    return prefs;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('codes.julian.cantinarr/push');
  for (final disabled in [
    'server',
    'account',
    'neither',
    'changed-account',
    'changed-server'
  ]) {
    test('permission request respects $disabled', () async {
      var permissionCalls = 0;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
        if (call.method == 'requestPermission') permissionCalls++;
        return false;
      });
      addTearDown(() => TestDefaultBinaryMessengerBinding
          .instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, null));
      final auth = _Auth();
      final prefs = _Prefs(NotificationPrefs.fromJson({
        'push_enabled': disabled != 'account',
        'server_policy': {
          'enabled': disabled != 'server',
          'categories': <String, bool>{}
        },
      }));
      if (disabled.startsWith('changed-')) prefs.gate = Completer<void>();
      final container = ProviderContainer(overrides: [
        authProvider.overrideWith(() => auth),
        notificationPrefsServiceProvider.overrideWithValue(prefs),
        pushServiceProvider
            .overrideWith((ref) => PushService(ref, supported: true)),
      ]);
      addTearDown(container.dispose);
      await container.read(authProvider.future);
      final registration =
          container.read(pushServiceProvider).registerForPush();
      if (prefs.gate != null) {
        await prefs.started.future;
        auth.change(
            disabled == 'changed-account' ? 2 : 1,
            disabled == 'changed-server'
                ? 'https://second.test'
                : 'https://first.test');
        prefs.gate!.complete();
      }
      await registration;
      expect(permissionCalls, disabled == 'neither' ? 1 : 0);
    });
  }
}
