import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../lidarr/data/lidarr_api_service.dart';
import '../../lidarr/data/lidarr_models.dart';

/// An id-keyed view of the Lidarr library's own artist records.
///
/// Unlike books — whose `foreignAuthorId` is a derived provider-priority
/// string that need not match between a lookup and the library record (see
/// [LibraryAuthorIndex]) — Lidarr's `foreignArtistId` is the MusicBrainz
/// artist id on BOTH sides, so exact id equality is the correct join and no
/// name-keyed ambiguity machinery is needed here.
class LibraryArtistIndex {
  final Map<String, LidarrArtist> _byForeignId;

  const LibraryArtistIndex(this._byForeignId);

  static const empty = LibraryArtistIndex(<String, LidarrArtist>{});

  factory LibraryArtistIndex.from(List<LidarrArtist> libraryArtists) {
    final byId = <String, LidarrArtist>{};
    for (final artist in libraryArtists) {
      final key = artist.foreignArtistId?.trim() ?? '';
      if (key.isEmpty) continue;
      byId[key] = artist;
    }
    return LibraryArtistIndex(byId);
  }

  /// The library record for one MusicBrainz artist id, or null when the
  /// library does not track them.
  LidarrArtist? match(String? foreignArtistId) {
    final key = foreignArtistId?.trim() ?? '';
    if (key.isEmpty) return null;
    return _byForeignId[key];
  }

  /// Library records whose name passes [test], in library order. This is the
  /// search overlay's own-library fallback: when the metadata lookup misses
  /// an artist the library holds, the records the query names are still
  /// surfaced directly.
  List<LidarrArtist> recordsWhere(bool Function(String artistName) test) => [
        for (final record in _byForeignId.values)
          if (test(record.artistName)) record,
      ];
}

/// The active Lidarr instance's own artist records, indexed by MusicBrainz
/// id, for resolving search results to openable library records.
///
/// One unpaginated fetch per instance, issued only once a Music-tab search
/// actually needs it and then held until the library itself changes
/// (`DashboardMusicTab._refreshMusicTruth()` drops the browse rows' truth
/// and the search overlay releases this index with it).
///
/// On failure this yields [LibraryArtistIndex.empty] rather than throwing: an
/// album search must not fail because artist *linking* could not be resolved.
/// Every artist then reads as metadata-only, which is the safe direction. The
/// blank is deliberately not held: only a fetch that succeeded keeps the
/// cache alive, so a Lidarr blip costs the search that saw it rather than
/// every search until the app restarts.
final libraryArtistIndexProvider =
    FutureProvider.autoDispose<LibraryArtistIndex>((ref) async {
  final instance = ref.watch(instanceProvider).activeLidarrInstance;
  if (instance == null) return LibraryArtistIndex.empty;
  final cache = ref.keepAlive();
  final service = LidarrApiService(
    backendDio: ref.read(backendClientProvider),
    instanceId: instance.id,
  );
  try {
    return LibraryArtistIndex.from(await service.getArtists());
  } catch (_) {
    cache.close();
    return LibraryArtistIndex.empty;
  }
});
