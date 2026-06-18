import 'package:dio/dio.dart';

class AiToolsStatus {
  final List<AiToolInfo> tools;
  final AiDebugStatus debug;

  const AiToolsStatus({
    required this.tools,
    required this.debug,
  });

  factory AiToolsStatus.fromJson(Map<String, dynamic> json) {
    final tools = (json['tools'] as List? ?? const [])
        .map((e) => AiToolInfo.fromJson(e as Map<String, dynamic>))
        .toList();
    return AiToolsStatus(
      tools: tools,
      debug: AiDebugStatus.fromJson(
        json['debug'] as Map<String, dynamic>? ?? const {},
      ),
    );
  }
}

class AiDebugStatus {
  final bool enabled;
  final DateTime? enabledUntil;
  final int remainingSeconds;

  const AiDebugStatus({
    required this.enabled,
    this.enabledUntil,
    this.remainingSeconds = 0,
  });

  factory AiDebugStatus.fromJson(Map<String, dynamic> json) {
    final until = json['enabled_until'] as String?;
    return AiDebugStatus(
      enabled: json['enabled'] as bool? ?? false,
      enabledUntil: until == null ? null : DateTime.tryParse(until)?.toLocal(),
      remainingSeconds: json['remaining_seconds'] as int? ?? 0,
    );
  }

  String get remainingLabel {
    if (!enabled || remainingSeconds <= 0) return 'Off';
    final hours = remainingSeconds ~/ 3600;
    final minutes = (remainingSeconds % 3600) ~/ 60;
    if (hours > 0) return '${hours}h ${minutes}m remaining';
    return '${minutes.clamp(1, 59)}m remaining';
  }
}

/// A single AI assistant tool as reported by the backend.
class AiToolInfo {
  final String name;
  final String description;
  final bool enabled;
  final bool adminOnly;

  const AiToolInfo({
    required this.name,
    required this.description,
    required this.enabled,
    required this.adminOnly,
  });

  factory AiToolInfo.fromJson(Map<String, dynamic> json) => AiToolInfo(
        name: json['name'] as String,
        description: json['description'] as String? ?? '',
        enabled: json['enabled'] as bool? ?? true,
        adminOnly: json['admin_only'] as bool? ?? false,
      );

  AiToolInfo copyWith({bool? enabled}) => AiToolInfo(
        name: name,
        description: description,
        enabled: enabled ?? this.enabled,
        adminOnly: adminOnly,
      );

  /// Human-friendly display name derived from the snake_case tool name.
  String get displayName => name
      .split('_')
      .where((w) => w.isNotEmpty)
      .map((w) => w[0].toUpperCase() + w.substring(1))
      .join(' ');
}

/// API service for admin AI tool management.
class AiToolsService {
  final Dio _dio;

  AiToolsService({required Dio backendDio}) : _dio = backendDio;

  /// Lists all AI assistant tools with their enabled state.
  Future<AiToolsStatus> getStatus() async {
    final resp = await _dio.get('/api/admin/ai-tools');
    return AiToolsStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<AiToolInfo>> getTools() async {
    return (await getStatus()).tools;
  }

  /// Enables or disables a single tool.
  Future<void> setEnabled(String name, bool enabled) async {
    await _dio.put('/api/admin/ai-tools/$name', data: {'enabled': enabled});
  }

  Future<AiDebugStatus> setDebug({
    required bool enabled,
    int hours = 1,
  }) async {
    final resp = await _dio.put(
      '/api/admin/ai-tools/debug',
      data: {'enabled': enabled, 'hours': hours},
    );
    return AiDebugStatus.fromJson(resp.data as Map<String, dynamic>);
  }
}
