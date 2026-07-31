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

  /// The checklist keys that give a request somewhere to go. Chaptarr is one of
  /// them on purpose: a books-only server is a real deployment, and calling it
  /// broken because it has no Radarr would be wrong.
  static const _libraryKeys = {'radarr', 'sonarr', 'books'};

  bool _isConfigured(String key) =>
      items.any((i) => i.key == key && i.configured);

  bool get _hasAnyLibrary =>
      items.any((i) => _libraryKeys.contains(i.key) && i.configured);

  /// Whether the server is missing something it cannot work without: metadata,
  /// or any library at all. Deliberately not "an essential row is empty" — a
  /// movies-only server never connects Sonarr and is perfectly functional, so
  /// this asks what the server can actually do rather than which rows are
  /// ticked. An empty list (a failed load) is never called broken.
  bool get missingCoreCapability =>
      items.isNotEmpty && (!_isConfigured('tmdb') || !_hasAnyLibrary);

  /// Whether this row is what stands between the server and working at all,
  /// which is what earns a row the alarm treatment instead of the ordinary
  /// "you haven't got to this yet" one.
  ///
  /// Radarr and Sonarr are each individually essential, so an empty one is
  /// only urgent while there is no library at all — otherwise a movies-only
  /// server would wear a permanent alarm on Sonarr while the Settings tile
  /// called the same server merely unfinished, and the two surfaces would
  /// contradict each other. Any other unconfigured essential is urgent on its
  /// own; optional rows never are, however much we'd like them tried.
  bool isUrgent(SetupItem item) {
    if (item.optional || item.configured) return false;
    if (_libraryKeys.contains(item.key)) return !_hasAnyLibrary;
    return true;
  }

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
