import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';

/// One entry in the admin setup checklist. The server derives the list live
/// from actual configuration, so "configured" can never go stale. Unknown
/// keys from newer servers still render (generically), which is how future
/// features surface themselves without an app update.
class SetupItem {
  final String key;
  final String title;
  final String description;
  final bool configured;
  final bool optional;

  const SetupItem({
    required this.key,
    required this.title,
    required this.description,
    required this.configured,
    required this.optional,
  });

  factory SetupItem.fromJson(Map<String, dynamic> json) => SetupItem(
        key: json['key'] as String? ?? '',
        title: json['title'] as String? ?? '',
        description: json['description'] as String? ?? '',
        configured: json['configured'] as bool? ?? false,
        optional: json['optional'] as bool? ?? false,
      );
}

class SetupStatus {
  final List<SetupItem> items;
  final int configured;
  final int total;

  const SetupStatus({
    required this.items,
    required this.configured,
    required this.total,
  });

  int get remaining => total - configured;

  factory SetupStatus.fromJson(Map<String, dynamic> json) {
    final items = (json['items'] as List? ?? [])
        .map((e) => SetupItem.fromJson(e as Map<String, dynamic>))
        .toList();
    return SetupStatus(
      items: items,
      configured: json['configured'] as int? ?? 0,
      total: json['total'] as int? ?? items.length,
    );
  }
}

class SetupStatusService {
  final Dio _dio;

  SetupStatusService({required Dio backendDio}) : _dio = backendDio;

  Future<SetupStatus> fetch() async {
    final resp = await _dio.get('/api/admin/setup-status');
    return SetupStatus.fromJson(resp.data as Map<String, dynamic>);
  }
}

final setupStatusServiceProvider = Provider<SetupStatusService>(
  (ref) => SetupStatusService(backendDio: ref.watch(backendClientProvider)),
);
