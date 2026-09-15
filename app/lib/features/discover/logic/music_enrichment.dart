import 'dart:async';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../request/data/album_ownership.dart';
import '../data/music_models.dart';
import 'discovery_access.dart';

final savedMusicRequestsProvider = FutureProvider.autoDispose
    .family<List<Map<String, dynamic>>, String>((ref, id) async {
  ref.watch(catalogDiscoveryScopeProvider);
  ref.watch(libraryRefreshTickProvider);
  final token = CancelToken();
  ref.onDispose(() => token.cancel());
  final response = await ref.read(backendClientProvider).get(
      '/api/requests/music-saved',
      queryParameters: {'instance_id': id},
      cancelToken: token);
  final rows = ((response.data as Map)['requests'] as List)
      .map((r) => Map<String, dynamic>.from(r as Map))
      .toList();
  if (rows.any((r) => (r['delivery'] as List? ?? [])
      .any((d) => !{'complete', 'cancelled'}.contains(d['state'])))) {
    final timer = Timer(const Duration(seconds: 10), ref.invalidateSelf);
    ref.onDispose(timer.cancel);
    ref.onCancel(timer.cancel);
  }
  return rows;
});

MusicAlbum libraryMusicAlbum(OwnedAlbum owned) => MusicAlbum(
        foreignId: owned.foreignAlbumId,
        title: owned.title,
        artist: owned.artist,
        releaseDate: owned.year > 0 ? '${owned.year}' : '',
        releaseType: owned.releaseType,
        artists: [
          if (owned.foreignArtistId.isNotEmpty)
            MusicArtist(foreignId: owned.foreignArtistId, name: owned.artist)
        ]);

/// Saved aliases are server-verified. Same titles never establish identity.
bool savedMusicMatches(Map<String, dynamic> row, String id) {
  final source = row['catalog_ref'];
  return row['foreign_id'] == id ||
      row['canonical_foreign_id'] == id ||
      (source is Map &&
          source['provider'] == 'musicbrainz' &&
          source['id'] == id);
}

Set<String> verifiedMusicIDs(String id, List<Map<String, dynamic>> saved) {
  final ids = {id};
  bool changed;
  do {
    changed = false;
    for (final row in saved) {
      final canonical = row['canonical_foreign_id'];
      if (canonical is! String || canonical.isEmpty) continue;
      final source = row['catalog_ref'];
      final aliases = <String>{
        canonical,
        if (row['foreign_id'] is String &&
            (row['foreign_id'] as String).isNotEmpty)
          row['foreign_id'] as String,
        if (source is Map &&
            source['provider'] == 'musicbrainz' &&
            source['id'] is String)
          source['id'] as String,
      };
      if (aliases.any(ids.contains)) {
        final previous = ids.length;
        ids.addAll(aliases);
        changed = changed || previous != ids.length;
      }
    }
  } while (changed);
  return ids;
}

String? musicBadge(
    String id, List<OwnedAlbum> library, List<Map<String, dynamic>> saved) {
  final ids = verifiedMusicIDs(id, saved);
  final matches = library.where((r) => ids.contains(r.foreignAlbumId));
  if (matches.any(
      (r) => r.status == 'available' || (r.status == null && r.downloaded))) {
    return 'Available';
  }
  if (matches.any((r) => r.status == 'downloading')) return 'Downloading';
  for (final row
      in saved.where((r) => ids.any((id) => savedMusicMatches(r, id)))) {
    final active = (row['delivery'] as List? ?? [])
        .where((d) => !{'complete', 'cancelled'}.contains(d['state']));
    if (active.any((d) => d['state'] == 'approval')) {
      return 'Waiting for approval';
    }
    if (active.any((d) => {'attention', 'needs_match'}.contains(d['state']))) {
      return 'Needs attention';
    }
    if (active.isNotEmpty || row['status'] == 'pending') {
      return row['status'] == 'pending' && active.isEmpty
          ? 'Waiting for approval'
          : 'Requested';
    }
  }
  if (matches.any((r) => r.monitored)) return 'Requested';
  return null;
}
