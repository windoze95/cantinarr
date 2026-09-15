import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/storage/preferences.dart';
import '../data/server_url.dart';

/// A shortcut, with no account identity, credentials, or cached auth policy.
class SavedServer {
  final String url;
  final String? name;

  const SavedServer({required this.url, this.name});

  String get label => name ?? url;

  static SavedServer? fromAddress(String address, {String? name}) {
    if (address.trim().isEmpty) return null;
    final url = normalizeServerUrl(address);
    final uri = Uri.tryParse(url);
    // Unusual credential-bearing URLs must never leak into preferences or
    // badges. They can still be entered through the existing manual flow.
    if (uri == null ||
        !uri.hasAuthority ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        (uri.scheme != 'http' && uri.scheme != 'https')) {
      return null;
    }
    final trimmedName = name?.trim();
    return SavedServer(
      url: url,
      name: trimmedName == null || trimmedName.isEmpty ? null : trimmedName,
    );
  }

  Map<String, dynamic> toJson() => {'url': url, if (name != null) 'name': name};
}

/// Most recently signed into first. The key's presence also marks migration
/// complete: an explicitly empty list must never resurrect forgotten servers.
class SavedServersNotifier extends AsyncNotifier<List<SavedServer>> {
  static const storageKey = 'cantinarr_saved_servers_v1';
  static const _storageTimeout = Duration(seconds: 2);
  Future<void> _writes = Future.value();

  @override
  Future<List<SavedServer>> build() async {
    final prefs = await ref
        .read(sharedPreferencesProvider.future)
        .timeout(_storageTimeout);
    final raw = prefs.getString(storageKey);
    if (raw == null) return const [];
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! List) return const [];
      final servers = <SavedServer>[];
      for (final entry in decoded) {
        if (entry is! Map || entry['url'] is! String) continue;
        final server = SavedServer.fromAddress(
          entry['url'] as String,
          name: entry['name'] is String ? entry['name'] as String : null,
        );
        if (server != null && !servers.any((s) => s.url == server.url)) {
          servers.add(server);
        }
      }
      return List.unmodifiable(servers);
    } on FormatException {
      return const [];
    }
  }

  Future<void> remember(SavedServer server) => _update((servers, _) => [
        server,
        ...servers.where((s) => s.url != server.url),
      ]);

  Future<void> migrateLegacySession(SavedServer? server) =>
      _update((servers, initialized) =>
          initialized ? null : [if (server != null) server]);

  Future<void> forget(String url) =>
      _update((servers, _) => servers.where((s) => s.url != url).toList());

  /// Undo restores the shortcut's position without undoing other removals or
  /// promoting it over a server signed into since the removal.
  Future<void> undoForget(SavedServer server, List<SavedServer> previous) =>
      _update((servers, _) {
        if (servers.any((s) => s.url == server.url)) return null;
        final next = [...servers];
        final oldIndex = previous.indexWhere((s) => s.url == server.url);
        final preceding = previous.take(oldIndex).map((s) => s.url).toSet();
        final following = previous.skip(oldIndex + 1).map((s) => s.url).toSet();
        final after = next.lastIndexWhere((s) => preceding.contains(s.url));
        final before = next.indexWhere((s) => following.contains(s.url));
        final index = after >= 0
            ? after + 1
            : before >= 0
                ? before
                : next.length;
        next.insert(index, server);
        return next;
      });

  Future<void> _update(
    List<SavedServer>? Function(List<SavedServer> servers, bool initialized)
        change,
  ) {
    final write = _writes.then((_) async {
      final servers = await future;
      final prefs = await ref.read(sharedPreferencesProvider.future);
      final next = change(servers, prefs.containsKey(storageKey));
      if (next == null) return;
      final saved = await prefs
          .setString(
              storageKey, jsonEncode(next.map((s) => s.toJson()).toList()))
          .timeout(_storageTimeout);
      if (!saved) throw StateError('Could not save server shortcuts.');
      state = AsyncData(List.unmodifiable(next));
    });
    // Serialize read-modify-write operations without a failed write poisoning
    // later operations. Callers still receive and handle the original error.
    _writes = write.catchError((Object _) {});
    return write;
  }
}

final savedServersProvider =
    AsyncNotifierProvider<SavedServersNotifier, List<SavedServer>>(
  SavedServersNotifier.new,
);
