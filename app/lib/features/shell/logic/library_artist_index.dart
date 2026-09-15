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

/// The selected instance's actual artists, loaded only while search needs
/// library supplementation. Errors stay visible independently of catalog
/// results; leaving search releases the cache.
final libraryArtistIndexProvider =
    FutureProvider.autoDispose<LibraryArtistIndex>((ref) async {
  final instance = ref.watch(instanceProvider).activeLidarrInstance;
  if (instance == null) return LibraryArtistIndex.empty;

  final service = LidarrApiService(
    backendDio: ref.read(backendClientProvider),
    instanceId: instance.id,
  );
  return LibraryArtistIndex.from(await service.getArtists());
});
