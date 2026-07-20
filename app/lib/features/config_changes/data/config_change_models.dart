import 'dart:convert';

/// One field in the server-authored before/recorded/current comparison.
///
/// The current server sends bounded display strings. Values remain passive
/// objects here so a future resource-specific projection cannot accidentally
/// become a client-authored restore payload.
class ConfigFieldChange {
  final String key;
  final String label;
  final Object? before;
  final Object? after;
  final Object? current;
  final bool hasCurrent;
  final String? currentState;

  const ConfigFieldChange({
    required this.key,
    required this.label,
    required this.before,
    required this.after,
    this.current,
    this.hasCurrent = false,
    this.currentState,
  });

  factory ConfigFieldChange.fromJson(Map<String, dynamic> json) =>
      ConfigFieldChange(
        key: json['key'] as String? ?? '',
        label: (json['label'] as String?)?.trim().isNotEmpty == true
            ? (json['label'] as String).trim()
            : (json['key'] as String? ?? 'Setting'),
        before: json['before'],
        after: json['after'],
        current: json['current'],
        hasCurrent: json.containsKey('current'),
        currentState: _optionalString(json['current_state']),
      );

  String? currentStateLabelFor(String recordedValueLabel) =>
      switch (currentState) {
        'matches_applied' => 'Matches ${recordedValueLabel.toLowerCase()}',
        'matches_before' => 'Matches before',
        'different' => 'Different',
        final value? when value.isNotEmpty => _humanize(value),
        _ => null,
      };
}

enum ConfigChangeSource {
  aiChat,
  adminRevert,
  externalMcp,
  system,
  setup,
  adminUi,
  unknown,
}

enum ConfigChangeOperation { create, update, revert, unknown }

enum ConfigChangeStatus { executing, applied, failed, outcomeUnknown, unknown }

enum ConfigCurrentStatus { matchesApplied, different, unavailable, unknown }

/// Durable record of one connected-app settings mutation.
///
/// List responses omit field projections and the live comparison, with
/// [canRevert] false until verified. Detail responses use the same model and
/// add the bounded recorded values and current state.
class ConfigChange {
  final int id;
  final int? parentId;
  final int actorUserId;
  final String actorName;
  final ConfigChangeSource source;
  final String sourceRaw;
  final String serviceType;
  final String instanceId;
  final String instanceName;
  final String resourceType;
  final String resourceId;
  final String resourceName;
  final ConfigChangeOperation operation;
  final String operationRaw;
  final ConfigChangeStatus status;
  final String statusRaw;
  final String summary;
  final List<ConfigFieldChange> changes;
  final String? errorText;
  final DateTime? createdAt;
  final DateTime? completedAt;
  final ConfigCurrentStatus? currentStatus;
  final String? currentError;
  final bool? canRevert;

  const ConfigChange({
    required this.id,
    required this.parentId,
    required this.actorUserId,
    required this.actorName,
    required this.source,
    required this.sourceRaw,
    required this.serviceType,
    required this.instanceId,
    required this.instanceName,
    required this.resourceType,
    required this.resourceId,
    required this.resourceName,
    required this.operation,
    required this.operationRaw,
    required this.status,
    required this.statusRaw,
    required this.summary,
    required this.changes,
    required this.errorText,
    required this.createdAt,
    required this.completedAt,
    this.currentStatus,
    this.currentError,
    this.canRevert,
  });

  factory ConfigChange.fromJson(Map<String, dynamic> json) {
    final sourceRaw = json['source'] as String? ?? '';
    final operationRaw = json['operation'] as String? ?? '';
    final statusRaw = json['status'] as String? ?? '';
    final currentStatusRaw = json['current_status'] as String?;
    return ConfigChange(
      id: (json['id'] as num?)?.toInt() ?? 0,
      parentId: (json['parent_id'] as num?)?.toInt(),
      actorUserId: (json['actor_user_id'] as num?)?.toInt() ?? 0,
      actorName: json['actor_name'] as String? ?? '',
      source: _sourceFromRaw(sourceRaw),
      sourceRaw: sourceRaw,
      serviceType: json['service_type'] as String? ?? '',
      instanceId: json['instance_id'] as String? ?? '',
      instanceName: json['instance_name'] as String? ?? '',
      resourceType: json['resource_type'] as String? ?? '',
      resourceId: _stringValue(json['resource_id']),
      resourceName: json['resource_name'] as String? ?? '',
      operation: _operationFromRaw(operationRaw),
      operationRaw: operationRaw,
      status: _statusFromRaw(statusRaw),
      statusRaw: statusRaw,
      summary: json['summary'] as String? ?? '',
      changes: ((json['changes'] as List?) ?? const [])
          .whereType<Map>()
          .map((item) => ConfigFieldChange.fromJson(
                item.map((key, value) => MapEntry(key.toString(), value)),
              ))
          .toList(growable: false),
      errorText: _optionalString(json['error_text']),
      createdAt: _date(json['created_at']),
      completedAt: _date(json['completed_at']),
      currentStatus: currentStatusRaw == null
          ? null
          : _currentStatusFromRaw(currentStatusRaw),
      currentError: _optionalString(json['current_error']),
      canRevert: json['can_revert'] as bool?,
    );
  }

  String get serviceLabel => switch (serviceType.toLowerCase()) {
        'radarr' => 'Radarr',
        'sonarr' => 'Sonarr',
        'chaptarr' => 'Chaptarr',
        'sabnzbd' => 'SABnzbd',
        'qbittorrent' => 'qBittorrent',
        'nzbget' => 'NZBGet',
        'transmission' => 'Transmission',
        'tautulli' => 'Tautulli',
        _ => serviceType.trim().isEmpty ? 'Connected app' : serviceType,
      };

  String get sourceLabel => switch (source) {
        ConfigChangeSource.aiChat => 'AI Assistant',
        ConfigChangeSource.adminRevert => 'Admin restore',
        ConfigChangeSource.externalMcp => 'External MCP',
        ConfigChangeSource.system => 'System',
        ConfigChangeSource.setup => 'Setup',
        ConfigChangeSource.adminUi => 'Admin UI',
        ConfigChangeSource.unknown =>
          sourceRaw.trim().isEmpty ? 'Cantinarr' : _humanize(sourceRaw),
      };

  String get statusLabel => switch (status) {
        ConfigChangeStatus.executing => 'Applying',
        ConfigChangeStatus.applied =>
          operation == ConfigChangeOperation.revert ? 'Restored' : 'Applied',
        ConfigChangeStatus.failed => 'Failed',
        ConfigChangeStatus.outcomeUnknown => 'Outcome unknown',
        ConfigChangeStatus.unknown =>
          statusRaw.trim().isEmpty ? 'Unknown' : _humanize(statusRaw),
      };

  /// Label for the recorded after projection.
  ///
  /// Only an applied row proves that the connected app accepted this state.
  /// Failed and unresolved rows record an attempted state, while executing or
  /// unrecognized rows only establish intent.
  String get recordedValueLabel => switch (status) {
        ConfigChangeStatus.applied => 'Applied',
        ConfigChangeStatus.failed || ConfigChangeStatus.outcomeUnknown =>
          'Attempted',
        ConfigChangeStatus.executing || ConfigChangeStatus.unknown =>
          'Intended',
      };

  String get comparisonTitle =>
      'Before, ${recordedValueLabel.toLowerCase()}, and current';

  String get recordedValueMatchLabel =>
      'Matches ${recordedValueLabel.toLowerCase()}';

  String get displaySummary {
    if (summary.trim().isNotEmpty) return summary.trim();
    if (operation == ConfigChangeOperation.revert) {
      return resourceName.trim().isEmpty
          ? 'Restored previous settings'
          : 'Restored ${resourceName.trim()}';
    }
    return resourceName.trim().isEmpty
        ? 'Updated connected-app settings'
        : 'Updated ${resourceName.trim()}';
  }

  bool get isApplied => status == ConfigChangeStatus.applied;
  bool get isLive => status == ConfigChangeStatus.executing;
}

ConfigChangeSource _sourceFromRaw(String value) => switch (value) {
      'ai_chat' => ConfigChangeSource.aiChat,
      'admin_revert' => ConfigChangeSource.adminRevert,
      'external_mcp' => ConfigChangeSource.externalMcp,
      'system' => ConfigChangeSource.system,
      'setup' => ConfigChangeSource.setup,
      'admin_ui' => ConfigChangeSource.adminUi,
      _ => ConfigChangeSource.unknown,
    };

ConfigChangeOperation _operationFromRaw(String value) => switch (value) {
      'create' => ConfigChangeOperation.create,
      'update' => ConfigChangeOperation.update,
      'revert' => ConfigChangeOperation.revert,
      _ => ConfigChangeOperation.unknown,
    };

ConfigChangeStatus _statusFromRaw(String value) => switch (value) {
      'executing' => ConfigChangeStatus.executing,
      'applied' => ConfigChangeStatus.applied,
      'failed' => ConfigChangeStatus.failed,
      'outcome_unknown' => ConfigChangeStatus.outcomeUnknown,
      _ => ConfigChangeStatus.unknown,
    };

ConfigCurrentStatus _currentStatusFromRaw(String value) => switch (value) {
      'matches_applied' => ConfigCurrentStatus.matchesApplied,
      'different' => ConfigCurrentStatus.different,
      'unavailable' => ConfigCurrentStatus.unavailable,
      _ => ConfigCurrentStatus.unknown,
    };

DateTime? _date(Object? value) =>
    DateTime.tryParse(value as String? ?? '')?.toLocal();

String? _optionalString(Object? value) {
  final text = value as String?;
  if (text == null || text.trim().isEmpty) return null;
  return text.trim();
}

String _stringValue(Object? value) => value?.toString() ?? '';

String _humanize(String value) => value
    .split('_')
    .where((part) => part.isNotEmpty)
    .map((part) => part[0].toUpperCase() + part.substring(1))
    .join(' ');

/// Plain, bounded representation for passive diff values.
String formatConfigValue(Object? value) {
  if (value == null) return 'Not set';
  if (value is bool) return value ? 'Enabled' : 'Disabled';
  if (value is String) return value.isEmpty ? 'Empty' : value;
  if (value is num) return value.toString();
  try {
    final encoded = jsonEncode(value);
    return encoded.length <= 500 ? encoded : '${encoded.substring(0, 497)}…';
  } catch (_) {
    final text = value.toString();
    return text.length <= 500 ? text : '${text.substring(0, 497)}…';
  }
}
