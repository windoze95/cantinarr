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

  /// The library's own author identity, as the server stamped it from the
  /// library's author record. This is the id `/detail/author/{id}` resolves,
  /// so it is the only value that may back a tap on the author line. Empty
  /// when the library states no author for this title.
  final String authorForeignId;
  final int year;

  /// The series name the server parsed off the record's raw seriesTitle (the
  /// last-" #" split — see `parseSeriesTitle` server-side). Empty when the
  /// library states no series for this title. The app must never redo that
  /// split itself; this is the one value proven to address
  /// `/api/requests/book-series-detail` for this library.
  final String series;

  /// The raw position string as the library states it ("2", "2A",
  /// "1.5, 1.6, 1.7"), passed through unnormalised. Empty when [series] is.
  final String seriesPosition;

  /// The owned record's cover path (e.g. `/MediaCover/...`), if any. Loads with
  /// the API key, so it shows real art for an owned result without the
  /// login-gated lookup cover. Empty when the record has no cached cover.
  final String cover;

  /// The owned book's foreignBookId, so a surfaced owned result can request its
  /// missing format (the backend completes the existing record). Empty when the
  /// record has none.
  final String foreignBookId;
  final BookOwnership ownership;

  /// Whether Chaptarr could resolve format truth for this title. Older
  /// servers omit the field, which means their rows retain the historical
  /// known-good behavior. A false value must fail closed: the canonical record
  /// still identifies the book, but no format may be offered as requestable.
  final bool statusKnown;

  const OwnedTitle({
    required this.title,
    required this.author,
    this.authorForeignId = '',
    this.year = 0,
    this.series = '',
    this.seriesPosition = '',
    this.cover = '',
    this.foreignBookId = '',
    required this.ownership,
    this.statusKnown = true,
  });

  /// Parses one digest entry: `title`/`author`/`author_foreign_id`/`year`/`series`/
  /// `series_position`/`cover`/`foreign_book_id` plus the `ebook` and
  /// `audiobook` format objects.
  factory OwnedTitle.fromJson(Map<String, dynamic> json) => OwnedTitle(
        title: json['title'] as String? ?? '',
        author: json['author'] as String? ?? '',
        authorForeignId: json['author_foreign_id'] as String? ?? '',
        year: json['year'] as int? ?? 0,
        series: json['series'] as String? ?? '',
        seriesPosition: json['series_position'] as String? ?? '',
        cover: json['cover'] as String? ?? '',
        foreignBookId: json['foreign_book_id'] as String? ?? '',
        ownership: BookOwnership(
          ebook: FormatOwnership.fromJson(
              json['ebook'] as Map<String, dynamic>?),
          audiobook: FormatOwnership.fromJson(
              json['audiobook'] as Map<String, dynamic>?),
        ),
        statusKnown: json['status_known'] as bool? ?? true,
      );
}
