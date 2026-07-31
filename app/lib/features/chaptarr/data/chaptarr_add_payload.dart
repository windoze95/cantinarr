import 'chaptarr_models.dart';

/// Builds the Chaptarr add-book body for adding a single missing format of a
/// title that's already in the library. The title's other format already exists
/// (so the author is tracked); a monitored record added under an existing author
/// stays monitored without needing editions, and searchForNewBook starts a
/// download search. mediaType is the single source of format truth on this fork;
/// the matching ebook/audiobook flag is set to agree with it.
Map<String, dynamic> chaptarrAddFormatBody({
  required String foreignBookId,
  required String title,
  String? titleSlug,
  required BookFormat format,
  required String authorName,
  String? foreignAuthorId,
  required int qualityProfileId,
  required int metadataProfileId,
  required String rootFolderPath,
}) {
  final isAudiobook = format == BookFormat.audiobook;
  return {
    'foreignBookId': foreignBookId,
    'title': title,
    'titleSlug': titleSlug,
    'monitored': true,
    'mediaType': isAudiobook ? 'audiobook' : 'ebook',
    'ebookMonitored': !isAudiobook,
    'audiobookMonitored': isAudiobook,
    'anyEditionOk': false,
    'author': {
      'authorName': authorName,
      'foreignAuthorId': foreignAuthorId,
      'qualityProfileId': qualityProfileId,
      'metadataProfileId': metadataProfileId,
      'rootFolderPath': rootFolderPath,
      'monitored': true,
      'addOptions': {'monitor': 'all', 'searchForMissingBooks': false},
    },
    'addOptions': {'searchForNewBook': true},
  };
}

/// Picks the root folder matching the format (…/audiobooks vs …/ebooks), falling
/// back to the first folder when no path matches.
String chaptarrRootFolderFor(
    BookFormat format, List<ChaptarrRootFolder> folders) {
  final needle = format == BookFormat.audiobook ? 'audiobook' : 'ebook';
  for (final f in folders) {
    if (f.path.toLowerCase().contains(needle)) return f.path;
  }
  return folders.first.path;
}
