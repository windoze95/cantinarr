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

  /// The server permits skipping this item. Kept for compatibility with older
  /// servers that refuse skips for some keys.
  final bool optional;

  /// A server-wide, reversible skip. Configuring the feature takes precedence.
  final bool skipped;

  const SetupItem({
    required this.key,
    required this.title,
    required this.description,
    required this.configured,
    required this.optional,
    this.skipped = false,
  });

  factory SetupItem.fromJson(Map<String, dynamic> json) => SetupItem(
        key: json['key'] as String? ?? '',
        title: json['title'] as String? ?? '',
        description: json['description'] as String? ?? '',
        configured: json['configured'] as bool? ?? false,
        optional: json['optional'] as bool? ?? false,
        skipped: json['skipped'] as bool? ?? false,
      );

  /// Skipped-and-unconfigured: the one state where the skip changes anything.
  /// A skipped item that later becomes configured simply reads as configured.
  bool get dismissed => skipped && !configured;
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

  /// How many unconfigured items an admin acknowledged and skipped. They
  /// leave the progress math entirely — denominator included — so "X of Y
  /// features configured" stays a true sentence about the features this
  /// deployment actually wants.
  int get skippedCount => items.where((i) => i.dismissed).length;

  /// The denominator every progress surface uses: the server's total minus
  /// the skipped items.
  int get effectiveTotal => total - skippedCount;

  int get remaining => effectiveTotal - configured;

  bool get isComplete => remaining == 0;

  String get summary => isComplete
      ? 'Nothing left to set up'
      : '$configured of $effectiveTotal features configured';

  double get progress =>
      effectiveTotal == 0 ? 1.0 : configured / effectiveTotal;

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

  /// Records or clears one checklist skip. Callers refresh afterwards so
  /// every surface derives progress from the same response.
  Future<void> setSkipped(String key, bool skipped) async {
    await _dio.put('/api/admin/setup-status/skips',
        data: {'key': key, 'skipped': skipped});
  }
}

final setupStatusServiceProvider = Provider<SetupStatusService>(
  (ref) => SetupStatusService(backendDio: ref.watch(backendClientProvider)),
);
