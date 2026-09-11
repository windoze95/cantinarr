import 'package:dio/dio.dart';

class DiscordDelivery {
  final int? requestId;
  final String status;
  final String detail;
  final DateTime? updatedAt;

  DiscordDelivery.fromJson(Map<String, dynamic> json)
      : requestId = json['request_id'] as int?,
        status = json['status'] as String? ?? 'unconfirmed',
        detail =
            json['detail'] as String? ?? 'Delivery could not be confirmed.',
        updatedAt = (json['updated_at'] as int? ?? 0) > 0
            ? DateTime.fromMillisecondsSinceEpoch(
                    (json['updated_at'] as int) * 1000)
                .toLocal()
            : null;

  String get label => switch (status) {
        'sent' => 'Delivered',
        'pending' => 'Waiting to send',
        'sending' => 'Sending',
        'failed' => 'Failed',
        'cancelled' => 'Cancelled',
        _ => 'Delivery unconfirmed',
      };
}

class DiscordNotificationSettings {
  final bool enabled;
  final bool hasWebhook;
  final List<DiscordDelivery> recent;
  final String? error;

  DiscordNotificationSettings.fromJson(Map<String, dynamic> json)
      : enabled = json['enabled'] == true,
        hasWebhook = json['has_webhook'] == true,
        recent = [
          for (final row in (json['recent'] as List? ?? []))
            DiscordDelivery.fromJson(Map<String, dynamic>.from(row as Map)),
        ],
        error = json['error'] as String?;
}

class DiscordNotificationsService {
  DiscordNotificationsService(this._dio);
  final Dio _dio;
  static const path = '/api/admin/discord-notifications';

  Future<DiscordNotificationSettings> get() async =>
      DiscordNotificationSettings.fromJson(
          (await _dio.get<Map<String, dynamic>>(path)).data!);

  Future<DiscordNotificationSettings> save(
          bool enabled, String webhook) async =>
      DiscordNotificationSettings.fromJson(
          (await _dio.put<Map<String, dynamic>>(
        path,
        data: {
          'enabled': enabled,
          if (webhook.trim().isNotEmpty) 'webhook_url': webhook.trim(),
        },
      ))
              .data!);

  Future<DiscordNotificationSettings> remove() async =>
      DiscordNotificationSettings.fromJson(
          (await _dio.delete<Map<String, dynamic>>(path)).data!);

  Future<DiscordDelivery> test(String webhook) async =>
      DiscordDelivery.fromJson((await _dio.post<Map<String, dynamic>>(
        '$path/test',
        data: {
          if (webhook.trim().isNotEmpty) 'webhook_url': webhook.trim(),
        },
      ))
          .data!);
}
