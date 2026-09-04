/// Ownership model for the "owned-aware music search" feature.
///
/// The backend serves an ownership digest of the form
/// `{"titles":[{"title","artist","year","foreign_album_id","cover",
/// "monitored","downloaded"}]}`. Music has no format axis, so ownership is a
/// single monitored/downloaded pair per album rather than the per-format
/// struct books carry. Deliberately free of any dependency on
/// `request_service.dart` so this file can be imported by the request service
/// without creating an import cycle.
library;

/// One parsed row of the music ownership digest: an album the user's library
/// already tracks, with the fields used to mark search results as owned.
class OwnedAlbum {
  final String title;
  final String artist;
  final int year;

  /// The owned record's cover path (a `/mediacover/...` proxy path), if any.
  /// Empty when the record has no cached cover.
  final String cover;

  /// The album's MusicBrainz release-group id, so a surfaced owned result can
  /// address the record. Empty when the record has none.
  final String foreignAlbumId;

  final bool monitored;
  final bool downloaded;

  const OwnedAlbum({
    required this.title,
    required this.artist,
    this.year = 0,
    this.cover = '',
    this.foreignAlbumId = '',
    this.monitored = false,
    this.downloaded = false,
  });

  /// Owned means the library already has (or is tracking) this album, so a
  /// search result for it should read as owned rather than requestable.
  bool get owned => monitored || downloaded;

  factory OwnedAlbum.fromJson(Map<String, dynamic> json) => OwnedAlbum(
        title: json['title'] as String? ?? '',
        artist: json['artist'] as String? ?? '',
        year: json['year'] as int? ?? 0,
        cover: json['cover'] as String? ?? '',
        foreignAlbumId: json['foreign_album_id'] as String? ?? '',
        monitored: json['monitored'] as bool? ?? false,
        downloaded: json['downloaded'] as bool? ?? false,
      );
}
