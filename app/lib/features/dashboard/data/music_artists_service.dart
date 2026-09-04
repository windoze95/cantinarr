import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../request/data/album_ownership.dart';

/// The orders the Artists row can be read in.
///
/// The order is applied by the server, not here: the row is capped, so sorting
/// an already-capped list would mean "the most-collected artists,
/// alphabetised" — a row that looks complete while omitting everyone below
/// the cut.
enum ArtistSort {
  mostAlbums('albums', 'Most albums'),
  name('name', 'Name'),
  dateAdded('added', 'Date added');

  const ArtistSort(this.wire, this.label);

  /// The value the API expects.
  final String wire;

  /// What the menu shows.
  final String label;
}

/// One artist the music library holds albums for.
class LibraryArtist {
  /// The MusicBrainz artist id this artist is addressed by. Empty for a
  /// record the library has not keyed yet, which leaves the artist visible
  /// but not openable.
  final String foreignArtistId;
  final String name;

  /// The artist's image path — relative (`/mediacover/...`) for library art,
  /// or an absolute metadata-CDN URL. Resolve it through [lidarrImageSource].
  final String image;

  /// How many albums by this artist the library tracks.
  final int albumCount;

  /// How many of those are complete on disk.
  final int availableCount;

  /// When the artist entered the library. Null when the record carries no
  /// date — it makes no recency claim, and the server sorts it last.
  final DateTime? added;

  const LibraryArtist({
    required this.foreignArtistId,
    required this.name,
    this.image = '',
    this.albumCount = 0,
    this.availableCount = 0,
    this.added,
  });

  factory LibraryArtist.fromJson(Map<String, dynamic> json) => LibraryArtist(
        foreignArtistId: json['foreign_artist_id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        image: json['image'] as String? ?? '',
        albumCount: (json['album_count'] as num?)?.toInt() ?? 0,
        availableCount: (json['available_count'] as num?)?.toInt() ?? 0,
        added: DateTime.tryParse(json['added'] as String? ?? ''),
      );

  /// The card's one-line count, in requester vocabulary. It always says what
  /// the number counted, so "3 of 12 available" can never be misread as a
  /// library that only holds three albums.
  String get countLabel {
    if (albumCount <= 0) return '';
    final albums = albumCount == 1 ? 'album' : 'albums';
    if (availableCount <= 0) return '$albumCount $albums';
    if (availableCount >= albumCount) {
      return '$albumCount $albums · all available';
    }
    return '$availableCount of $albumCount $albums available';
  }
}

/// One page of the Artists row: the artists to show, and how many the library
/// actually holds. The two differ once a library outgrows the row's cap.
class MusicArtistsPage {
  final List<LibraryArtist> artists;

  /// How many artists the library holds, before the row's cap.
  final int total;

  const MusicArtistsPage({required this.artists, this.total = 0});

  /// How many artists this page is not showing.
  int get hiddenCount => total > artists.length ? total - artists.length : 0;
}

/// One artist plus every album of theirs the library tracks.
class MusicArtistDetail {
  final LibraryArtist artist;

  /// The artist's albums, newest first, carrying the same ownership shape the
  /// search digest uses — so the page renders the same pills.
  final List<OwnedAlbum> titles;

  const MusicArtistDetail({required this.artist, required this.titles});

  factory MusicArtistDetail.fromJson(Map<String, dynamic> json) {
    final rawTitles = json['titles'];
    return MusicArtistDetail(
      artist: LibraryArtist.fromJson(
          json['artist'] as Map<String, dynamic>? ?? const {}),
      titles: rawTitles is List
          ? rawTitles
              .whereType<Map<String, dynamic>>()
              .map(OwnedAlbum.fromJson)
              .toList()
          : const [],
    );
  }
}

/// Fetches the music library's artists, so the Music tab can offer a browse
/// row and an artist page.
class MusicArtistsService {
  final Dio _dio;

  MusicArtistsService({required Dio backendDio}) : _dio = backendDio;

  Future<MusicArtistsPage> fetchArtists({
    String? instanceId,
    ArtistSort sort = ArtistSort.mostAlbums,
  }) async {
    final resp = await _dio.get(
      '/api/requests/music-artists',
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
        'sort': sort.wire,
      },
    );
    final data = resp.data;
    final artists = data is Map ? data['artists'] : null;
    if (artists is! List) {
      throw const FormatException('Music artists response is invalid');
    }
    final parsed = artists
        .whereType<Map<String, dynamic>>()
        .map(LibraryArtist.fromJson)
        .toList();
    // A server too old to report a total says nothing about truncation, so
    // the row claims none rather than inventing one.
    final total = (data is Map ? (data['total'] as num?)?.toInt() : null) ?? 0;
    return MusicArtistsPage(
      artists: parsed,
      total: total > parsed.length ? total : parsed.length,
    );
  }

  Future<MusicArtistDetail> fetchArtist(
    String foreignArtistId, {
    String? instanceId,
  }) async {
    final resp = await _dio.get(
      '/api/requests/music-artist',
      queryParameters: {
        'foreign_id': foreignArtistId,
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
      },
    );
    final data = resp.data;
    if (data is! Map<String, dynamic>) {
      throw const FormatException('Music artist response is invalid');
    }
    return MusicArtistDetail.fromJson(data);
  }
}

/// The order the Artists row is currently read in. Deliberately session
/// state, not a stored preference — the same reasoning as the Authors row.
final musicArtistsSortProvider =
    StateProvider<ArtistSort>((ref) => ArtistSort.mostAlbums);

/// The artists of the drawer's active Lidarr library, in the selected order.
///
/// Sort and instance are watched here rather than being family keys on
/// purpose: one provider instance spans every order, so changing the order
/// keeps the previous list on screen while the new one loads.
final musicArtistsProvider =
    FutureProvider.autoDispose<MusicArtistsPage>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeLidarrInstance?.id;
  final sort = ref.watch(musicArtistsSortProvider);
  final dio = ref.read(backendClientProvider);
  return MusicArtistsService(backendDio: dio)
      .fetchArtists(instanceId: instanceId, sort: sort);
});

/// The artist a detail page is pinned to: an explicit instance id plus the
/// artist's MusicBrainz id, so a pinned page can never read another library's
/// answer for the same artist.
typedef MusicArtistRef = ({String? instanceId, String foreignArtistId});

/// One artist's page data. Deliberately uncached server-side — the page is
/// opened to decide what to request, so it must show an album requested
/// seconds ago as Requested.
final musicArtistDetailProvider = FutureProvider.autoDispose
    .family<MusicArtistDetail, MusicArtistRef>((ref, target) async {
  final dio = ref.read(backendClientProvider);
  return MusicArtistsService(backendDio: dio).fetchArtist(
    target.foreignArtistId,
    instanceId: target.instanceId,
  );
});
