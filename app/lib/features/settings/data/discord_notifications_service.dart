import 'package:dio/dio.dart';

const discordEventLabels = {
  'request_pending': 'Request awaiting approval',
  'request_auto_approved': 'Automatically approved request',
  'request_approved': 'Request approved',
  'request_denied': 'Request denied',
  'request_available': 'Requested content available',
  'request_failed': 'Request needs attention',
  'issue_created': 'Problem reported',
  'issue_comment': 'Reply to a report',
  'issue_resolved': 'Problem resolved',
  'issue_reopened': 'Problem report reopened',
};

const discordAdminEvents = {
  'request_pending', 'request_auto_approved', 'request_failed', 'issue_created',
};

Map<String, bool> _eventMap(dynamic value) => {
  if (value is Map)
    for (final entry in value.entries) entry.key.toString(): entry.value == true,
};

class DiscordDelivery {
  final int? requestId;
  final int? issueId;
  final String? event;
  final String status;
  final String detail;
  final DateTime? updatedAt;

  DiscordDelivery.fromJson(Map<String, dynamic> json)
      : requestId = json['request_id'] as int?,
        issueId = json['issue_id'] as int?,
        event = json['event'] as String?,
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
        'split' => 'Grouped deliveries',
        _ => 'Delivery unconfirmed',
      };

  String get subject => event == null
      ? 'Request #$requestId'
      : '${discordEventLabels[event] ?? 'Notification'}${(issueId ?? 0) > 0 ? ' #$issueId' : ''}';
}

class DiscordNotificationSettings {
  final bool enabled;
  final bool hasWebhook;
  final bool includeAutoApproved;
  final List<DiscordDelivery> recent;
  final String? error;
  final Map<String, bool> events;
  final Map<String, bool> roleEvents;
  final bool enableMentions;
  final bool embedPoster;
  final String roleId;
  final String threadId;
  final String username;
  final String avatarUrl;

  DiscordNotificationSettings.fromJson(Map<String, dynamic> json)
      : enabled = json['enabled'] == true,
        hasWebhook = json['has_webhook'] == true,
        includeAutoApproved = json['include_auto_approved'] == true,
        events = json['events'] == null
            ? {'request_pending': true, 'request_auto_approved': json['include_auto_approved'] == true}
            : _eventMap(json['events']),
        roleEvents = _eventMap(json['role_events']),
        enableMentions = json['enable_mentions'] == true,
        embedPoster = json['embed_poster'] == true,
        roleId = json['role_id'] as String? ?? '',
        threadId = json['thread_id'] as String? ?? '',
        username = json['username'] as String? ?? '',
        avatarUrl = json['avatar_url'] as String? ?? '',
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
          bool enabled, String webhook, bool includeAutoApproved,
          {Map<String, dynamic> options = const {}}) async =>
      DiscordNotificationSettings.fromJson(
          (await _dio.put<Map<String, dynamic>>(
        path,
        data: {
          'enabled': enabled,
          'include_auto_approved': includeAutoApproved,
          ...options,
          if (webhook.trim().isNotEmpty) 'webhook_url': webhook.trim(),
        },
      ))
              .data!);

  Future<DiscordNotificationSettings> remove() async =>
      DiscordNotificationSettings.fromJson(
          (await _dio.delete<Map<String, dynamic>>(path)).data!);

  Future<DiscordDelivery> test(String webhook,
          {Map<String, dynamic> options = const {}}) async =>
      DiscordDelivery.fromJson((await _dio.post<Map<String, dynamic>>(
        '$path/test',
        data: {
          ...options,
          if (webhook.trim().isNotEmpty) 'webhook_url': webhook.trim(),
        },
      ))
          .data!);
}

class DiscordPreferences {
  final bool enabled;
  final List<String> discordIds;
  final Map<String, bool> events;
  final Map<String, bool> allowedEvents;
  final String? blockedReason;

  DiscordPreferences.fromJson(Map<String, dynamic> json)
      : enabled = json['enabled'] == true,
        discordIds = List<String>.from(json['discord_ids'] as List? ?? []),
        events = _eventMap(json['events']),
        allowedEvents = _eventMap(json['allowed_events']),
        blockedReason = json['blocked_reason'] as String?;
}

class DiscordPreferencesService {
  DiscordPreferencesService(this._dio);
  final Dio _dio;
  static const path = '/api/auth/discord-notifications';

  Future<DiscordPreferences> get() async => DiscordPreferences.fromJson(
      (await _dio.get<Map<String, dynamic>>(path)).data!);

  Future<DiscordPreferences> save(bool enabled, List<String> ids,
          Map<String, bool> events) async => DiscordPreferences.fromJson(
      (await _dio.put<Map<String, dynamic>>(path, data: {
        'enabled': enabled, 'discord_ids': ids, 'events': events,
      })).data!);

  Future<DiscordDelivery> test() async => DiscordDelivery.fromJson(
      (await _dio.post<Map<String, dynamic>>('$path/test')).data!);
}
