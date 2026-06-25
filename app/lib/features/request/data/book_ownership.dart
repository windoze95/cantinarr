/// Ownership model for the "owned-aware book search" feature.
///
/// The backend serves an ownership digest of the form
/// `{"titles":[{"title","author","year","ebook":{...},"audiobook":{...}}]}`.
/// Each format object carries `monitored`/`downloaded` flags. We model these as
/// neutral value types here, deliberately free of any dependency on
/// `request_service.dart` (in particular `BookRequestFormat`) so this file can
/// be imported by the request service without creating an import cycle.
library;

/// Whether the user already has a given format (ebook or audiobook) of a title.
///
/// A format counts as [owned] — and therefore unrequestable — once it is
/// monitored or downloaded.
class FormatOwnership {
  final bool monitored;
  final bool downloaded;

  const FormatOwnership({this.monitored = false, this.downloaded = false});

  /// Owned means the user already has (or is tracking) this format, so it
  /// should not be offered as a new request.
  bool get owned => monitored || downloaded;

  /// Null-safe parse of a `{"monitored":bool,"downloaded":bool}` object. A null
  /// or malformed map yields a default (un-owned) [FormatOwnership].
  factory FormatOwnership.fromJson(Map<String, dynamic>? json) {
    if (json == null) return const FormatOwnership();
    return FormatOwnership(
      monitored: json['monitored'] as bool? ?? false,
      downloaded: json['downloaded'] as bool? ?? false,
    );
  }
}

/// Per-format ownership of a single title (its ebook and audiobook).
class BookOwnership {
  final FormatOwnership ebook;
  final FormatOwnership audiobook;

  const BookOwnership({
    this.ebook = const FormatOwnership(),
    this.audiobook = const FormatOwnership(),
  });

  /// True when either format is owned (monitored or downloaded).
  bool get anyOwned => ebook.owned || audiobook.owned;

  /// True when either format has a downloaded file on disk.
  bool get anyDownloaded => ebook.downloaded || audiobook.downloaded;
}

/// One parsed row of the ownership digest: a title the user already has in some
/// form, with the normalized fields used for fuzzy matching against search
/// lookup results.
class OwnedTitle {
  final String title;
  final String author;
  final int year;

  /// The owned record's cover path (e.g. `/MediaCover/...`), if any. Loads with
  /// the API key, so it shows real art for an owned result without the
  /// login-gated lookup cover. Empty when the record has no cached cover.
  final String cover;

  /// The owned book's foreignBookId, so a surfaced owned result can request its
  /// missing format (the backend completes the existing record). Empty when the
  /// record has none.
  final String foreignBookId;
  final BookOwnership ownership;

  const OwnedTitle({
    required this.title,
    required this.author,
    this.year = 0,
    this.cover = '',
    this.foreignBookId = '',
    required this.ownership,
  });

  /// Parses one digest entry: `title`/`author`/`year`/`cover`/`foreign_book_id`
  /// plus the `ebook` and `audiobook` format objects.
  factory OwnedTitle.fromJson(Map<String, dynamic> json) => OwnedTitle(
        title: json['title'] as String? ?? '',
        author: json['author'] as String? ?? '',
        year: json['year'] as int? ?? 0,
        cover: json['cover'] as String? ?? '',
        foreignBookId: json['foreign_book_id'] as String? ?? '',
        ownership: BookOwnership(
          ebook: FormatOwnership.fromJson(
              json['ebook'] as Map<String, dynamic>?),
          audiobook: FormatOwnership.fromJson(
              json['audiobook'] as Map<String, dynamic>?),
        ),
      );
}
