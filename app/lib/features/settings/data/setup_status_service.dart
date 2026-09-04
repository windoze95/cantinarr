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

  /// An optional item the admin acknowledged and dismissed. The server stamps
  /// it only on optional items, it is stored server-wide (the checklist
  /// grades the server, not a device), and it is reversible in place — so a
  /// feature the deployment deliberately doesn't use stops counting as
  /// unfinished without a persistent nag.
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

  /// The checklist keys that give a request somewhere to go. Chaptarr and
  /// Lidarr are among them on purpose: a books-only or music-only server is a
  /// real deployment, and calling it broken because it has no Radarr would be
  /// wrong.
  static const _libraryKeys = {'radarr', 'sonarr', 'books', 'music'};

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

  /// Records or clears one checklist skip. The server refuses essentials and
  /// unknown keys; callers refresh the status afterwards so every surface
  /// re-derives from the same answer.
  Future<void> setSkipped(String key, bool skipped) async {
    await _dio.put('/api/admin/setup-status/skips',
        data: {'key': key, 'skipped': skipped});
  }
}

final setupStatusServiceProvider = Provider<SetupStatusService>(
  (ref) => SetupStatusService(backendDio: ref.watch(backendClientProvider)),
);
