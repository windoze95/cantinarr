import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../core/network/backend_client.dart';
import 'notification_prefs.dart';

/// API client for the current user's push-notification preferences.
class NotificationPrefsService {
  final Dio _dio;

  NotificationPrefsService({required Dio backendDio}) : _dio = backendDio;

  Future<PushNotificationPolicy> getServerPolicy() async =>
      PushNotificationPolicy.fromJson(
          (await _dio.get('/api/admin/push-notifications')).data
              as Map<String, dynamic>);

  Future<PushNotificationPolicy> updateServerPolicy(
          PushNotificationPolicy policy) async =>
      PushNotificationPolicy.fromJson((await _dio
              .put('/api/admin/push-notifications', data: policy.toJson()))
          .data as Map<String, dynamic>);

  Future<NotificationPrefs> getPreferences() async {
    final resp = await _dio.get('/api/notifications/preferences');
    return NotificationPrefs.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<NotificationPrefs> updatePreferences(NotificationPrefs prefs) async {
    final resp =
        await _dio.put('/api/notifications/preferences', data: prefs.toJson());
    return NotificationPrefs.fromJson(resp.data as Map<String, dynamic>);
  }
}

final notificationPrefsServiceProvider = Provider<NotificationPrefsService>(
  (ref) => NotificationPrefsService(
    backendDio: ref.watch(backendClientProvider),
  ),
);
