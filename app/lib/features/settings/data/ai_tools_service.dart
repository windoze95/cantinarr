import 'package:dio/dio.dart';

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
  Future<List<AiToolInfo>> getTools() async {
    final resp = await _dio.get('/api/admin/ai-tools');
    final tools =
        (resp.data as Map<String, dynamic>)['tools'] as List? ?? const [];
    return tools
        .map((e) => AiToolInfo.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Enables or disables a single tool.
  Future<void> setEnabled(String name, bool enabled) async {
    await _dio.put('/api/admin/ai-tools/$name', data: {'enabled': enabled});
  }
}
